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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// WorkloadReconciler ensures the pull secrets referenced by a workload's annotation
// exist in the workload's namespace. It is generic over the workload kind (Deployment,
// StatefulSet, DaemonSet, ReplicaSet, Job, CronJob, ...) so the same logic serves every
// type that can reference imagePullSecrets.
type WorkloadReconciler struct {
	Client   client.Client
	Recorder record.EventRecorder

	// Name is the controller name (also used for logging).
	Name string
	// GVK identifies the workload kind, used to build owner references.
	GVK schema.GroupVersionKind
	// New returns a fresh, empty instance of the workload type.
	New func() client.Object
}

func (r *WorkloadReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	obj := r.New()
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !obj.GetDeletionTimestamp().IsZero() {
		return reconcile.Result{}, nil
	}

	namespace := obj.GetNamespace()
	pullSecrets := annotations.GetClusterPullSecrets(obj.GetAnnotations())
	desired := make(map[string]bool, len(pullSecrets))
	for _, name := range pullSecrets {
		desired[name] = true
	}

	// Release/delete secrets this workload previously owned but no longer references.
	if err := r.cleanupOrphanedSecrets(ctx, obj, desired); err != nil {
		log.Printf("[%s] failed to clean up orphaned secrets for %s/%s: %v", r.Name, namespace, obj.GetName(), err)
	}

	var errs []error
	for _, name := range pullSecrets {
		cps := &v1.ClusterPullSecret{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: name}, cps); err != nil {
			if apierrors.IsNotFound(err) {
				log.Printf("[%s] ClusterPullSecret %s not found, skipping.", r.Name, name)
				continue
			}
			log.Printf("[%s] error fetching ClusterPullSecret %s: %v", r.Name, name, err)
			errs = append(errs, err)
			continue
		}

		val, keyName, err := credentials.GetCredentialBytes(ctx, r.Client, cps.Spec.Credentials)
		if err != nil {
			log.Printf("[%s] failed to get credentials for ClusterPullSecret %s: %v", r.Name, name, err)
			errs = append(errs, err)
			continue
		}

		secretName := credentials.SecretPrefix + name
		target := &corev1.Secret{}
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, target)

		if apierrors.IsNotFound(err) {
			newSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      secretName,
					Annotations: map[string]string{
						annotations.AnnotationSecretManagedBy.String(): name,
					},
					OwnerReferences: []metav1.OwnerReference{r.ownerRef(obj)},
				},
				Type: corev1.SecretTypeDockerConfigJson,
				Data: map[string][]byte{keyName: val},
			}
			if err := r.Client.Create(ctx, newSecret); err != nil {
				log.Printf("[%s] failed to create secret %s/%s: %v", r.Name, namespace, secretName, err)
				errs = append(errs, err)
				continue
			}
			r.Recorder.Eventf(obj, "Normal", "Created", "Created pull secret %s/%s", namespace, secretName)
			continue
		} else if err != nil {
			log.Printf("[%s] error fetching secret %s/%s: %v", r.Name, namespace, secretName, err)
			errs = append(errs, err)
			continue
		}

		changed := false
		if target.Data == nil || !bytes.Equal(target.Data[keyName], val) {
			if target.Data == nil {
				target.Data = map[string][]byte{}
			}
			target.Data[keyName] = val
			changed = true
		}
		if target.Type != corev1.SecretTypeDockerConfigJson {
			target.Type = corev1.SecretTypeDockerConfigJson
			changed = true
		}
		if target.Annotations == nil {
			target.Annotations = map[string]string{}
		}
		if target.Annotations[annotations.AnnotationSecretManagedBy.String()] != name {
			target.Annotations[annotations.AnnotationSecretManagedBy.String()] = name
			changed = true
		}
		if !hasOwnerReference(target.OwnerReferences, obj.GetUID()) {
			target.OwnerReferences = append(target.OwnerReferences, r.ownerRef(obj))
			changed = true
		}

		if changed {
			if err := r.Client.Update(ctx, target); err != nil {
				log.Printf("[%s] failed to update secret %s/%s: %v", r.Name, namespace, secretName, err)
				errs = append(errs, err)
				continue
			}
			r.Recorder.Eventf(obj, "Normal", "Updated", "Updated pull secret %s/%s", namespace, secretName)
		}
	}

	return reconcile.Result{}, errors.Join(errs...)
}

// cleanupOrphanedSecrets removes this workload's claim on managed pull secrets in its
// namespace that it no longer references. The workload's owner reference is dropped; if
// no owners remain the secret is deleted, otherwise it is left for its other owners.
func (r *WorkloadReconciler) cleanupOrphanedSecrets(ctx context.Context, obj client.Object, desired map[string]bool) error {
	list := &corev1.SecretList{}
	if err := r.Client.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
		return err
	}

	var errs []error
	for i := range list.Items {
		s := &list.Items[i]
		if s.Annotations == nil {
			continue
		}
		cpsName, ok := s.Annotations[annotations.AnnotationSecretManagedBy.String()]
		if !ok || desired[cpsName] {
			continue
		}
		if !hasOwnerReference(s.OwnerReferences, obj.GetUID()) {
			continue
		}

		s.OwnerReferences = removeOwnerReference(s.OwnerReferences, obj.GetUID())
		if len(s.OwnerReferences) == 0 {
			if err := r.Client.Delete(ctx, s); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, err)
				continue
			}
			r.Recorder.Eventf(obj, "Normal", "Deleted", "Deleted orphaned pull secret %s/%s", s.Namespace, s.Name)
		} else {
			if err := r.Client.Update(ctx, s); err != nil {
				errs = append(errs, err)
				continue
			}
			r.Recorder.Eventf(obj, "Normal", "Released", "Released ownership of pull secret %s/%s", s.Namespace, s.Name)
		}
	}

	return errors.Join(errs...)
}

func (r *WorkloadReconciler) ownerRef(obj client.Object) metav1.OwnerReference {
	controller := false
	return metav1.OwnerReference{
		APIVersion: r.GVK.GroupVersion().String(),
		Kind:       r.GVK.Kind,
		Name:       obj.GetName(),
		UID:        obj.GetUID(),
		Controller: &controller,
	}
}

func (r *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("clusterpullsecret-workload-controller")
	return ctrl.NewControllerManagedBy(mgr).
		Named(r.Name).
		For(r.New()).
		Complete(r)
}

// hasOwnerReference checks if the given UID exists in the owner references list.
func hasOwnerReference(refs []metav1.OwnerReference, uid types.UID) bool {
	for _, ref := range refs {
		if ref.UID == uid {
			return true
		}
	}
	return false
}

// removeOwnerReference returns refs with any entry matching uid removed.
func removeOwnerReference(refs []metav1.OwnerReference, uid types.UID) []metav1.OwnerReference {
	out := make([]metav1.OwnerReference, 0, len(refs))
	for _, ref := range refs {
		if ref.UID == uid {
			continue
		}
		out = append(out, ref)
	}
	return out
}
