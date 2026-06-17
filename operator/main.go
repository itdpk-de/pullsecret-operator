// Package main starts the ClusterPullSecret operator controller.
// This operator manages Docker registry pull secrets across a Kubernetes cluster,
// automatically synchronizing credentials to namespaces based on Deployment annotations.
package main

import (
	"log"
	"os"

	"github.com/pascalku/pullsecret-operator/operator/controllers"
	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
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

	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "kube-leader-pullsecret.pascal-kutscha.de",
		LeaderElectionNamespace: leaderElectionNamespace,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: healthAddr,
	})
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatalf("Failed to set up health check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatalf("Failed to set up ready check: %v", err)
	}

	if err := (&controllers.ClusterPullSecretReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("Error creating ClusterPullSecret reconciler: %v", err)
	}
	if err := (&controllers.DeploymentReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("Error creating Deployment reconciler: %v", err)
	}

	log.Println("Starting manager...")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("Manager exited non-zero: %v", err)
	}
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
