package controllers

import (
	"context"
	"os"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// TestLeaderElectionRuntime starts a manager with leader election against a real
// (envtest) apiserver and verifies it actually acquires leadership and writes the Lease.
func TestLeaderElectionRuntime(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run `setup-envtest use` first")
	}

	testEnv := &envtest.Environment{}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = testEnv.Stop() })

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	const leaseID = "pullsecret-le-test"
	mgr, err := manager.New(cfg, manager.Options{
		Scheme:                        scheme,
		LeaderElection:                true,
		LeaderElectionID:              leaseID,
		LeaderElectionNamespace:       "default",
		LeaderElectionReleaseOnCancel: true,
		Metrics:                       metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:        "0",
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	startErr := make(chan error, 1)
	go func() { startErr <- mgr.Start(ctx) }()

	// mgr.Elected() is closed once this manager wins the election.
	select {
	case <-mgr.Elected():
	case err := <-startErr:
		cancel()
		t.Fatalf("manager stopped before becoming leader: %v", err)
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("did not acquire leadership within 30s")
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	lease := &coordinationv1.Lease{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "default", Name: leaseID}, lease); err != nil {
		cancel()
		t.Fatalf("expected leader-election Lease %q: %v", leaseID, err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		cancel()
		t.Fatalf("lease has no holder identity")
	}
	t.Logf("acquired leadership; lease holder = %s", *lease.Spec.HolderIdentity)

	// Graceful shutdown should release the lease (ReleaseOnCancel).
	cancel()
	select {
	case <-startErr:
	case <-time.After(10 * time.Second):
		t.Fatal("manager did not stop within 10s after cancel")
	}
}
