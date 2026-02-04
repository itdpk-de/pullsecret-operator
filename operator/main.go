package main

import (
	"context"
	"log"

	"github.com/pascalku/pullsecret-operator/operator/controllers"
	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func init() {
	utilruntime.Must(v1.AddToScheme(scheme.Scheme))
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	log.Println("Starting ClusterPullSecret operator...")
	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		LeaderElection:          false,
		LeaderElectionID:        "kube-leader-pullsecret.pascal-kutscha.de",
		LeaderElectionNamespace: "kube-system",
	})
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}

	if err := (&controllers.ClusterPullSecretReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("Error creating reconciler: %v", err)
	}
	if err := (&controllers.DeploymentReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("Error creating reconciler: %v", err)
	}

	log.Println("Starting manager...")
	if err := mgr.Start(context.Background()); err != nil {
		log.Fatalf("Manager exited non-zero: %v", err)
	}
}
