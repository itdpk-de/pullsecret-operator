package controllers

import (
	"bytes"
	"context"
	"errors"
	"log"

	"github.com/pascalku/pullsecret-operator/operator/annotations"
	"github.com/pascalku/pullsecret-operator/operator/credentials"
	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type DeploymentReconciler struct {
	Client   client.Client
	Recorder record.EventRecorder
}

func (r *DeploymentReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	deployment := &appsv1.Deployment{}
	if err := r.Client.Get(ctx, req.NamespacedName, deployment); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !deployment.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	pullSecrets := annotations.GetClusterPullSecrets(deployment.Annotations)
	desired := make(map[string]bool, len(pullSecrets))
	for _, name := range pullSecrets {
		desired[name] = true
	}

	// Release/delete secrets this deployment previously owned but no longer references
	// (e.g. the annotation was edited or removed) so stale credentials don't linger.
	if err := r.cleanupOrphanedSecrets(ctx, deployment, desired); err != nil {
		log.Printf("failed to clean up orphaned secrets for deployment %s/%s: %v", deployment.Namespace, deployment.Name, err)
	}

	var errs []error
	for _, name := range pullSecrets {
		cps := &v1.ClusterPullSecret{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: name}, cps); err != nil {
			if apierrors.IsNotFound(err) {
				log.Printf("ClusterPullSecret %s not found, skipping.", name)
				continue
			}
			log.Printf("error fetching ClusterPullSecret %s: %v", name, err)
			errs = append(errs, err)
			continue
		}

		// Use the shared credential helper
		val, keyName, err := credentials.GetCredentialBytes(ctx, r.Client, cps.Spec.Credentials)
		if err != nil {
			log.Printf("failed to get credentials for ClusterPullSecret %s: %v", name, err)
			errs = append(errs, err)
			continue
		}

		// Target secret lives in the deployment's namespace and uses the ClusterPullSecret's name
		targetKey := client.ObjectKey{Namespace: deployment.Namespace, Name: credentials.SecretPrefix + name}
		target := &corev1.Secret{}
		err = r.Client.Get(ctx, targetKey, target)

		if apierrors.IsNotFound(err) {
			// Create new secret with owner reference
			newSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: deployment.Namespace,
					Name:      credentials.SecretPrefix + name,
					Annotations: map[string]string{
						annotations.AnnotationSecretManagedBy.String(): name,
					},
					OwnerReferences: []metav1.OwnerReference{
						deploymentOwnerRef(deployment),
					},
				},
				Type: corev1.SecretTypeDockerConfigJson,
				Data: map[string][]byte{
					keyName: val,
				},
			}
			if err := r.Client.Create(ctx, newSecret); err != nil {
				log.Printf("failed to create secret %s/%s: %v", deployment.Namespace, credentials.SecretPrefix+name, err)
				errs = append(errs, err)
				continue
			}

			r.Recorder.Eventf(deployment, "Normal", "Created", "Created pull secret %s/%s", deployment.Namespace, credentials.SecretPrefix+name)
			continue
		} else if err != nil {
			log.Printf("error fetching secret %s/%s: %v", deployment.Namespace, credentials.SecretPrefix+name, err)
			errs = append(errs, err)
			continue
		}

		// Update existing secret
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

		// Ensure owner reference to deployment exists
		if !hasOwnerReference(target.OwnerReferences, deployment.UID) {
			target.OwnerReferences = append(target.OwnerReferences, deploymentOwnerRef(deployment))
			changed = true
		}

		if changed {
			if err := r.Client.Update(ctx, target); err != nil {
				log.Printf("failed to update secret %s/%s: %v", deployment.Namespace, credentials.SecretPrefix+name, err)
				errs = append(errs, err)
				continue
			}
			r.Recorder.Eventf(deployment, "Normal", "Updated", "Updated pull secret %s/%s", deployment.Namespace, credentials.SecretPrefix+name)
		}
	}

	return reconcile.Result{}, errors.Join(errs...)
}

// cleanupOrphanedSecrets removes this deployment's claim on managed pull secrets in its
// namespace that it no longer references. The deployment's owner reference is dropped; if
// no owners remain the secret is deleted, otherwise it is left for its other owners.
func (r *DeploymentReconciler) cleanupOrphanedSecrets(ctx context.Context, deployment *appsv1.Deployment, desired map[string]bool) error {
	list := &corev1.SecretList{}
	if err := r.Client.List(ctx, list, client.InNamespace(deployment.Namespace)); err != nil {
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
		if !hasOwnerReference(s.OwnerReferences, deployment.UID) {
			continue
		}

		s.OwnerReferences = removeOwnerReference(s.OwnerReferences, deployment.UID)
		if len(s.OwnerReferences) == 0 {
			if err := r.Client.Delete(ctx, s); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, err)
				continue
			}
			r.Recorder.Eventf(deployment, "Normal", "Deleted", "Deleted orphaned pull secret %s/%s", s.Namespace, s.Name)
		} else {
			if err := r.Client.Update(ctx, s); err != nil {
				errs = append(errs, err)
				continue
			}
			r.Recorder.Eventf(deployment, "Normal", "Released", "Released ownership of pull secret %s/%s", s.Namespace, s.Name)
		}
	}

	return errors.Join(errs...)
}

func deploymentOwnerRef(deployment *appsv1.Deployment) metav1.OwnerReference {
	controller := false
	return metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       deployment.Name,
		UID:        deployment.UID,
		Controller: &controller,
	}
}

// hasOwnerReference checks if the given UID exists in the owner references list
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

func (r *DeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("clusterpullsecret-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Complete(r)
}
