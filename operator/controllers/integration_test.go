package controllers

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/pascalku/pullsecret-operator/operator/annotations"
	"github.com/pascalku/pullsecret-operator/operator/credentials"
	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TestIntegration exercises the real reconcilers and CRD against an envtest apiserver.
// It is skipped unless KUBEBUILDER_ASSETS points at the kube-apiserver/etcd binaries
// (set it via `setup-envtest use -p path <version>`).
func TestIntegration(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run `setup-envtest use` first")
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "pullsecret", "templates")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = testEnv.Stop() })

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	k8s, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	ctx := context.Background()

	const validCfg = `{"auths":{"ghcr.io":{"auth":"dXNlcjpwYXNz"}}}`

	t.Run("CEL rejects two credential sources", func(t *testing.T) {
		bad := &v1.ClusterPullSecret{
			ObjectMeta: metav1.ObjectMeta{Name: "bad"},
			Spec: v1.ClusterPullSecretSpec{Credentials: v1.PullSecretCredentials{
				DockerConfigJSON: validCfg,
				EncryptedData:    base64.StdEncoding.EncodeToString([]byte(validCfg)),
			}},
		}
		if err := k8s.Create(ctx, bad); err == nil {
			t.Fatal("expected CEL validation to reject two credential sources, but create succeeded")
		}
	})

	// Source secret with individual fields + a ClusterPullSecret referencing it.
	srcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "reg-creds"},
		Data: map[string][]byte{
			"server":   []byte("ghcr.io"),
			"username": []byte("user"),
			"password": []byte("pass"),
		},
	}
	if err := k8s.Create(ctx, srcSecret); err != nil {
		t.Fatalf("create source secret: %v", err)
	}

	cps := &v1.ClusterPullSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "ghcr"},
		Spec: v1.ClusterPullSecretSpec{Credentials: v1.PullSecretCredentials{
			RegistryCredentials: &v1.RegistryCredentialsRef{Namespace: "default", SecretName: "reg-creds"},
		}},
	}
	if err := k8s.Create(ctx, cps); err != nil {
		t.Fatalf("create clusterpullsecret: %v", err)
	}

	recorder := record.NewFakeRecorder(100)
	wr := &WorkloadReconciler{
		Client:   k8s,
		Recorder: recorder,
		Name:     "deployment",
		GVK:      schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
		New:      func() client.Object { return &appsv1.Deployment{} },
	}
	cr := &ClusterPullSecretReconciler{Client: k8s, Recorder: recorder}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "default",
			Name:        "web",
			Annotations: map[string]string{annotations.AnnotationClusterPullSecrets.String(): "ghcr"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "ghcr.io/x/y:latest"}}},
			},
		},
	}
	if err := k8s.Create(ctx, dep); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	secretKey := client.ObjectKey{Namespace: "default", Name: credentials.SecretPrefix + "ghcr"}

	t.Run("workload reconciler creates a valid dockerconfigjson secret", func(t *testing.T) {
		if _, err := wr.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dep)}); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &corev1.Secret{}
		if err := k8s.Get(ctx, secretKey, got); err != nil {
			t.Fatalf("expected generated secret: %v", err)
		}
		if got.Type != corev1.SecretTypeDockerConfigJson {
			t.Fatalf("secret type = %v, want dockerconfigjson", got.Type)
		}
		if _, ok := got.Data[credentials.DockerConfigKeyName]; !ok {
			t.Fatalf("secret missing %s key; keys=%v", credentials.DockerConfigKeyName, keysOf(got.Data))
		}
		if got.Annotations[annotations.AnnotationSecretManagedBy.String()] != "ghcr" {
			t.Fatalf("missing managed-by annotation")
		}
		if len(got.OwnerReferences) == 0 || got.OwnerReferences[0].Name != "web" {
			t.Fatalf("missing owner reference to deployment")
		}
	})

	t.Run("clusterpullsecret reconciler reports Synced", func(t *testing.T) {
		if _, err := cr.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "ghcr"}}); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &v1.ClusterPullSecret{}
		if err := k8s.Get(ctx, client.ObjectKey{Name: "ghcr"}, got); err != nil {
			t.Fatal(err)
		}
		if !got.Status.Synced || got.Status.Phase != v1.PhaseSynced {
			t.Fatalf("status = synced:%v phase:%q, want synced:true phase:Synced", got.Status.Synced, got.Status.Phase)
		}
	})

	t.Run("deleted managed secret is recreated on reconcile", func(t *testing.T) {
		got := &corev1.Secret{}
		if err := k8s.Get(ctx, secretKey, got); err != nil {
			t.Fatalf("precondition: secret should exist: %v", err)
		}
		if err := k8s.Delete(ctx, got); err != nil {
			t.Fatalf("delete secret: %v", err)
		}
		if _, err := wr.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dep)}); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if err := k8s.Get(ctx, secretKey, &corev1.Secret{}); err != nil {
			t.Fatalf("expected secret to be recreated: %v", err)
		}
	})

	t.Run("removing the annotation cleans up the secret", func(t *testing.T) {
		cur := &appsv1.Deployment{}
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(dep), cur); err != nil {
			t.Fatal(err)
		}
		cur.Annotations = map[string]string{}
		if err := k8s.Update(ctx, cur); err != nil {
			t.Fatalf("update deployment: %v", err)
		}
		if _, err := wr.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dep)}); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		err := k8s.Get(ctx, secretKey, &corev1.Secret{})
		if !apierrors.IsNotFound(err) {
			t.Fatalf("expected secret to be deleted, got err=%v", err)
		}
	})
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
