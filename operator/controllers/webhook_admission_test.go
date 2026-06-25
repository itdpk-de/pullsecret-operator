package controllers

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/itdpk-de/pullsecret-operator/operator/annotations"
	podwebhook "github.com/itdpk-de/pullsecret-operator/operator/webhook"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// TestWebhookAdmission verifies the full TLS admission path end-to-end: the envtest
// apiserver calls our mutating webhook over TLS, and the PodInjector injects
// imagePullSecrets into the persisted Pod.
func TestWebhookAdmission(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run `setup-envtest use` first")
	}

	path := "/mutate-v1-pod"
	failPolicy := admissionregistrationv1.Ignore
	sideEffects := admissionregistrationv1.SideEffectClassNone
	scope := admissionregistrationv1.NamespacedScope
	webhookCfg := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "pullsecret-pod-injector-test"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{{
			Name:                    "pods.pullsecret.itdpk.de",
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &sideEffects,
			FailurePolicy:           &failPolicy,
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service: &admissionregistrationv1.ServiceReference{
					Name:      "pullsecret-webhook",
					Namespace: "default",
					Path:      &path,
				},
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods"},
					Scope:       &scope,
				},
			}},
		}},
	}

	testEnv := &envtest.Environment{
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			MutatingWebhooks: []*admissionregistrationv1.MutatingWebhookConfiguration{webhookCfg},
		},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = testEnv.Stop() })
	woi := testEnv.WebhookInstallOptions

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	mgr, err := manager.New(cfg, manager.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    woi.LocalServingHost,
			Port:    woi.LocalServingPort,
			CertDir: woi.LocalServingCertDir,
		}),
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	mgr.GetWebhookServer().Register(path, &admission.Webhook{
		Handler: &podwebhook.PodInjector{Client: mgr.GetClient(), Decoder: admission.NewDecoder(scheme)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	startErr := make(chan error, 1)
	go func() { startErr <- mgr.Start(ctx) }()

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	// The apiserver only calls the webhook once the TLS server is up. Retry creating
	// Pods (unique names) until one comes back injected, or fail after the deadline.
	annKey := annotations.AnnotationClusterPullSecrets.String()
	var injected *corev1.Pod
	deadline := time.Now().Add(40 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		select {
		case err := <-startErr:
			t.Fatalf("manager stopped: %v", err)
		default:
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "default",
				Name:        fmt.Sprintf("inject-%d", i),
				Annotations: map[string]string{annKey: "ghcr"},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "ghcr.io/x/y:latest"}}},
		}
		if err := c.Create(ctx, pod); err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if len(pod.Spec.ImagePullSecrets) > 0 {
			injected = pod
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if injected == nil {
		t.Fatal("webhook did not inject imagePullSecrets within the deadline")
	}
	if injected.Spec.ImagePullSecrets[0].Name != "pullsecret-ghcr" {
		t.Fatalf("imagePullSecrets = %v, want [pullsecret-ghcr]", injected.Spec.ImagePullSecrets)
	}
	t.Logf("webhook injected imagePullSecrets over TLS: %v", injected.Spec.ImagePullSecrets)
}
