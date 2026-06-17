package controllers

import (
	"bytes"
	"context"
	"errors"
	"log"
	"time"

	"github.com/pascalku/pullsecret-operator/operator/annotations"
	"github.com/pascalku/pullsecret-operator/operator/credentials"
	"github.com/pascalku/pullsecret-operator/operator/registry"
	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ClusterPullSecretReconciler struct {
	Client   client.Client
	Recorder record.EventRecorder
	// RegistryValidator, when set, actively checks the resolved credentials against
	// each referenced registry to detect expired/invalid credentials. Optional.
	RegistryValidator registry.Validator
}

const clusterFinalizer = "pullsecret.pascal-kutscha.de/finalizer"

// resyncInterval is how often credentials are re-validated even without a triggering
// event, so the status and metrics reflect e.g. a deleted source secret promptly.
const resyncInterval = 5 * time.Minute

func (r *ClusterPullSecretReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	cps := &v1.ClusterPullSecret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: req.Name}, cps); err != nil {
		if apierrors.IsNotFound(err) {
			clearSyncedMetric(req.Name)
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// If the object is being deleted, clean up managed Secrets and remove finalizer
	if !cps.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, cps)
	}

	// Ensure finalizer exists
	if !controllerutil.ContainsFinalizer(cps, clusterFinalizer) {
		controllerutil.AddFinalizer(cps, clusterFinalizer)
		if err := r.Client.Update(ctx, cps); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Obtain credentials using the shared helper
	val, keyName, err := credentials.GetCredentialBytes(ctx, r.Client, cps.Spec.Credentials)
	if err != nil {
		log.Printf("failed to get credentials for ClusterPullSecret %s: %v", cps.Name, err)
		r.setStatus(ctx, cps, false, reasonForError(err))

		// Permanent spec errors will not resolve until the resource changes (which
		// re-triggers reconciliation), so avoid hot error-retry loops; just resync
		// periodically. Resolvable errors (missing/invalid source secret, transient
		// API errors) are retried with backoff.
		if isPermanentCredentialError(err) {
			return reconcile.Result{RequeueAfter: resyncInterval}, nil
		}
		return reconcile.Result{}, err
	}

	// List all secrets managed by this ClusterPullSecret
	list := &corev1.SecretList{}
	if err := r.Client.List(ctx, list); err != nil {
		return reconcile.Result{}, err
	}

	// Filter and update managed secrets
	for _, s := range list.Items {
		if s.Annotations == nil {
			continue
		}
		managedBy, ok := s.Annotations[annotations.AnnotationSecretManagedBy.String()]
		if !ok || managedBy != cps.Name {
			continue
		}

		sec := s
		changed := false
		if sec.Data == nil || !bytes.Equal(sec.Data[keyName], val) {
			if sec.Data == nil {
				sec.Data = map[string][]byte{}
			}
			sec.Data[keyName] = val
			changed = true
		}
		if sec.Type != corev1.SecretTypeDockerConfigJson {
			sec.Type = corev1.SecretTypeDockerConfigJson
			changed = true
		}

		if changed {
			if err := r.Client.Update(ctx, &sec); err != nil {
				log.Printf("failed to update secret %s/%s: %v", sec.Namespace, sec.Name, err)
				continue
			}
			r.Recorder.Eventf(cps, "Normal", "Updated", "Updated pull secret %s/%s", sec.Namespace, sec.Name)
		}
	}

	// Update status. Structurally the credentials are valid; if active registry
	// validation is enabled, an explicit auth rejection downgrades the state so that
	// expired/wrong credentials surface in status and metrics.
	cps.Status.ObservedGeneration = cps.Generation
	synced := true
	reason := ""
	if r.RegistryValidator != nil {
		statuses, authFailed := r.runRegistryValidation(ctx, cps.Name, val)
		cps.Status.RegistryAuth = statuses
		if authFailed {
			synced = false
			reason = "registry-auth-failed"
		}
	}
	r.setStatus(ctx, cps, synced, reason)

	// Re-validate periodically so the status/metrics catch out-of-band changes
	// (e.g. a deleted source secret or expired credentials) even without an event.
	return reconcile.Result{RequeueAfter: resyncInterval}, nil
}

// runRegistryValidation actively checks the credentials against each referenced registry
// and returns the per-registry status plus whether any registry explicitly rejected them.
func (r *ClusterPullSecretReconciler) runRegistryValidation(ctx context.Context, name string, dockerConfigJSON []byte) ([]v1.RegistryAuthStatus, bool) {
	results := r.RegistryValidator.Validate(ctx, dockerConfigJSON)
	now := metav1.Now()
	authFailed := false
	statuses := make([]v1.RegistryAuthStatus, 0, len(results))
	for _, res := range results {
		authed := res.State == registry.StateOK
		statuses = append(statuses, v1.RegistryAuthStatus{
			Registry:      res.Registry,
			Authenticated: authed,
			Reason:        res.Reason,
			LastCheckTime: &now,
		})
		switch res.State {
		case registry.StateOK:
			setRegistryAuthMetric(name, res.Registry, true)
		case registry.StateAuthFailed:
			authFailed = true
			setRegistryAuthMetric(name, res.Registry, false)
		}
	}
	return statuses, authFailed
}

// setStatus updates the ClusterPullSecret status and the synced metric in one place.
func (r *ClusterPullSecretReconciler) setStatus(ctx context.Context, cps *v1.ClusterPullSecret, synced bool, reason string) {
	now := metav1.Now()
	cps.Status.Synced = synced
	cps.Status.Reason = reason
	cps.Status.LastValidationTime = &now
	if synced {
		cps.Status.Phase = v1.PhaseSynced
		cps.Status.LastSyncTime = &now
	} else {
		cps.Status.Phase = v1.PhaseOutOfSync
	}

	setSyncedMetric(cps.Name, synced)

	if err := r.Client.Status().Update(ctx, cps); err != nil {
		log.Printf("failed to update status for ClusterPullSecret %s: %v", cps.Name, err)
	}
}

// reasonForError maps a credential error to a short, stable status reason suitable
// for dashboards and alerting.
func reasonForError(err error) string {
	var keyNotFoundErr credentials.ErrKeyNotFound
	var missingFieldErr credentials.ErrMissingField
	var invalidErr credentials.ErrInvalidDockerConfig
	var multiErr credentials.ErrMultipleCredentials
	var noCredsErr credentials.ErrNoCredentials
	switch {
	case errors.As(err, &keyNotFoundErr):
		return "source-secret-key-missing"
	case errors.As(err, &missingFieldErr):
		return "registry-credentials-field-missing"
	case errors.As(err, &invalidErr):
		return "invalid-dockerconfigjson"
	case errors.As(err, &multiErr):
		return "multiple-credential-sources"
	case errors.As(err, &noCredsErr):
		return "no-credentials"
	case apierrors.IsNotFound(err):
		return "source-secret-not-found"
	default:
		return "credential-error"
	}
}

// isPermanentCredentialError reports whether the error is a spec-level misconfiguration
// that will not resolve on retry (only on a resource change, which re-triggers reconcile).
func isPermanentCredentialError(err error) bool {
	var multiErr credentials.ErrMultipleCredentials
	var noCredsErr credentials.ErrNoCredentials
	var invalidErr credentials.ErrInvalidDockerConfig
	return errors.As(err, &multiErr) || errors.As(err, &noCredsErr) || errors.As(err, &invalidErr)
}

// handleDeletion cleans up all secrets managed by this ClusterPullSecret
func (r *ClusterPullSecretReconciler) handleDeletion(ctx context.Context, cps *v1.ClusterPullSecret) (reconcile.Result, error) {
	// List and delete all managed secrets
	list := &corev1.SecretList{}
	if err := r.Client.List(ctx, list); err != nil {
		return reconcile.Result{}, err
	}

	for _, s := range list.Items {
		if s.Annotations == nil {
			continue
		}
		managedBy, ok := s.Annotations[annotations.AnnotationSecretManagedBy.String()]
		if !ok || managedBy != cps.Name {
			continue
		}

		sec := s
		if err := r.Client.Delete(ctx, &sec); err != nil {
			log.Printf("failed to delete managed secret %s/%s: %v", sec.Namespace, sec.Name, err)
			// continue trying to delete other secrets
		}
	}

	clearSyncedMetric(cps.Name)

	// Remove finalizer
	controllerutil.RemoveFinalizer(cps, clusterFinalizer)
	if err := r.Client.Update(ctx, cps); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ClusterPullSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("clusterpullsecret-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.ClusterPullSecret{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.secretToClusterPullSecrets)).
		Complete(r)
}

// secretToClusterPullSecrets maps a Secret event to the ClusterPullSecrets that should
// be reconciled: the one that manages a generated secret, plus any whose credential
// source references the changed secret (so credential rotation propagates).
func (r *ClusterPullSecretReconciler) secretToClusterPullSecrets(ctx context.Context, object client.Object) []reconcile.Request {
	secret, ok := object.(*corev1.Secret)
	if !ok {
		return nil
	}

	seen := map[string]struct{}{}
	var reqs []reconcile.Request
	add := func(name string) {
		if name == "" {
			return
		}
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKey{Name: name}})
	}

	// 1. A generated/managed secret -> its owning ClusterPullSecret.
	if secret.Annotations != nil {
		add(secret.Annotations[annotations.AnnotationSecretManagedBy.String()])
	}

	// 2. A source secret -> any ClusterPullSecret referencing it (credential rotation).
	cpsList := &v1.ClusterPullSecretList{}
	if err := r.Client.List(ctx, cpsList); err != nil {
		log.Printf("failed to list ClusterPullSecrets for secret %s/%s: %v", secret.Namespace, secret.Name, err)
		return reqs
	}
	for i := range cpsList.Items {
		creds := cpsList.Items[i].Spec.Credentials
		if ref := creds.SecretRef; ref != nil && ref.Namespace == secret.Namespace && ref.Name == secret.Name {
			add(cpsList.Items[i].Name)
			continue
		}
		if ref := creds.RegistryCredentials; ref != nil && ref.Namespace == secret.Namespace && ref.SecretName == secret.Name {
			add(cpsList.Items[i].Name)
		}
	}

	return reqs
}
