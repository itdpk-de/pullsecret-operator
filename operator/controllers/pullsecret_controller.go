package controllers

import (
	"bytes"
	"context"
	"errors"
	"log"

	"github.com/pascalku/pullsecret-operator/operator/annotations"
	"github.com/pascalku/pullsecret-operator/operator/credentials"
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
}

const clusterFinalizer = "pullsecret.pascal-kutscha.de/finalizer"

func (r *ClusterPullSecretReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	cps := &v1.ClusterPullSecret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: req.Name}, cps); err != nil {
		if apierrors.IsNotFound(err) {
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
		
		reason := "credential-error"
		var keyNotFoundErr credentials.ErrKeyNotFound
		var noCredsErr credentials.ErrNoCredentials
		if errors.As(err, &keyNotFoundErr) {
			reason = "source-secret-key-missing"
		} else if errors.As(err, &noCredsErr) {
			reason = "no-credentials"
		} else if apierrors.IsNotFound(err) {
			reason = "source-secret-not-found"
		}
		
		cps.Status.Synced = false
		cps.Status.Reason = reason
		if err := r.Client.Status().Update(ctx, cps); err != nil {
			log.Printf("failed to update status for ClusterPullSecret %s: %v", cps.Name, err)
		}
		return reconcile.Result{}, nil
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

	// Update status
	now := metav1.Now()
	cps.Status.ObservedGeneration = cps.Generation
	cps.Status.Synced = true
	cps.Status.Reason = ""
	cps.Status.LastSyncTime = &now
	if err := r.Client.Status().Update(ctx, cps); err != nil {
		log.Printf("failed to update status for ClusterPullSecret %s: %v", cps.Name, err)
	}

	return reconcile.Result{}, nil
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

	// Remove finalizer
	controllerutil.RemoveFinalizer(cps, clusterFinalizer)
	if err := r.Client.Update(ctx, cps); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ClusterPullSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("clusterpullsecret-controller")
	err := ctrl.NewControllerManagedBy(mgr).
		For(&v1.ClusterPullSecret{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
			secret, ok := object.(*corev1.Secret)
			if !ok {
				return nil
			}

			if secret.Annotations == nil {
				return nil
			}
			name, ok := secret.Annotations[annotations.AnnotationSecretManagedBy.String()]
			if !ok {
				return nil
			}

			return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: name}}}
		})).
		Complete(r)

	return err
}
