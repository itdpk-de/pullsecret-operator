// Package main starts the ClusterPullSecret operator controller.
// This operator manages Docker registry pull secrets across a Kubernetes cluster,
// automatically synchronizing credentials to namespaces based on Deployment annotations.
package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/pascalku/pullsecret-operator/operator/controllers"
	"github.com/pascalku/pullsecret-operator/operator/registry"
	podwebhook "github.com/pascalku/pullsecret-operator/operator/webhook"
	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func init() {
	utilruntime.Must(v1.AddToScheme(scheme.Scheme))
}

// main initializes and runs the operator manager.
// It sets up controllers for ClusterPullSecret and Deployment resources,
// and handles graceful shutdown via OS signals.
func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	log.Println("Starting ClusterPullSecret operator...")

	enableLeaderElection := os.Getenv("ENABLE_LEADER_ELECTION") == "true"
	leaderElectionNamespace := getEnvDefault("LEADER_ELECTION_NAMESPACE", "kube-system")
	metricsAddr := getEnvDefault("METRICS_BIND_ADDRESS", ":8080")
	healthAddr := getEnvDefault("HEALTH_PROBE_BIND_ADDRESS", ":8081")
	webhookEnabled := os.Getenv("WEBHOOK_ENABLED") == "true"

	opts := manager.Options{
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "kube-leader-pullsecret.pascal-kutscha.de",
		LeaderElectionNamespace: leaderElectionNamespace,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: healthAddr,
	}
	if webhookEnabled {
		opts.WebhookServer = webhook.NewServer(webhook.Options{
			Port:    getEnvInt("WEBHOOK_PORT", 9443),
			CertDir: getEnvDefault("WEBHOOK_CERT_DIR", "/tmp/k8s-webhook-server/serving-certs"),
		})
	}

	mgr, err := manager.New(config.GetConfigOrDie(), opts)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatalf("Failed to set up health check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatalf("Failed to set up ready check: %v", err)
	}

	// Active registry-login validation is enabled by default; it requires network
	// egress from the operator to the referenced registries. Disable via
	// REGISTRY_VALIDATION_ENABLED=false in air-gapped environments.
	var registryValidator registry.Validator
	if os.Getenv("REGISTRY_VALIDATION_ENABLED") != "false" {
		timeout := time.Duration(getEnvInt("REGISTRY_VALIDATION_TIMEOUT_SECONDS", 10)) * time.Second
		registryValidator = registry.NewHTTPValidator(timeout)
		log.Println("Active registry-login validation enabled")
	}

	if err := (&controllers.ClusterPullSecretReconciler{
		Client:            mgr.GetClient(),
		RegistryValidator: registryValidator,
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("Error creating ClusterPullSecret reconciler: %v", err)
	}
	// Register a workload reconciler for every built-in kind that can reference
	// imagePullSecrets, so the annotation works beyond Deployments.
	for _, w := range workloadKinds() {
		if err := (&controllers.WorkloadReconciler{
			Client: mgr.GetClient(),
			Name:   w.name,
			GVK:    w.gvk,
			New:    w.new,
		}).SetupWithManager(mgr); err != nil {
			log.Fatalf("Error creating %s reconciler: %v", w.name, err)
		}
	}

	// Optional mutating webhook that injects imagePullSecrets into Pods. It mutates the
	// Pod (not the workload) so GitOps controllers do not see drift.
	if webhookEnabled {
		mgr.GetWebhookServer().Register("/mutate-v1-pod", &admission.Webhook{
			Handler: &podwebhook.PodInjector{
				Client:  mgr.GetClient(),
				Decoder: admission.NewDecoder(scheme.Scheme),
			},
		})
		log.Println("Pod imagePullSecrets injection webhook enabled")
	}

	log.Println("Starting manager...")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("Manager exited non-zero: %v", err)
	}
}

type workloadKind struct {
	name string
	gvk  schema.GroupVersionKind
	new  func() client.Object
}

// workloadKinds lists the built-in workload types the operator reconciles for the
// cluster-pull-secrets annotation.
func workloadKinds() []workloadKind {
	return []workloadKind{
		{"deployment", appsv1.SchemeGroupVersion.WithKind("Deployment"), func() client.Object { return &appsv1.Deployment{} }},
		{"statefulset", appsv1.SchemeGroupVersion.WithKind("StatefulSet"), func() client.Object { return &appsv1.StatefulSet{} }},
		{"daemonset", appsv1.SchemeGroupVersion.WithKind("DaemonSet"), func() client.Object { return &appsv1.DaemonSet{} }},
		{"replicaset", appsv1.SchemeGroupVersion.WithKind("ReplicaSet"), func() client.Object { return &appsv1.ReplicaSet{} }},
		{"job", batchv1.SchemeGroupVersion.WithKind("Job"), func() client.Object { return &batchv1.Job{} }},
		{"cronjob", batchv1.SchemeGroupVersion.WithKind("CronJob"), func() client.Object { return &batchv1.CronJob{} }},
	}
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
