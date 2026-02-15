package controllers

import (
	"bytes"
	"context"
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
		log.Printf("Deployment %s/%s not found, ignoring.", req.Namespace, req.Name)
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	pullSecrets := annotations.GetClusterPullSecrets(deployment.Annotations)

	if !deployment.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	if len(pullSecrets) == 0 {
		return reconcile.Result{}, nil
	}

	for _, name := range pullSecrets {
		cps := &v1.ClusterPullSecret{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: name}, cps); err != nil {
			if apierrors.IsNotFound(err) {
				log.Printf("ClusterPullSecret %s not found, skipping.", name)
				continue
			}
			log.Printf("error fetching ClusterPullSecret %s: %v", name, err)
			continue
		}

		// Use the shared credential helper
		val, keyName, err := credentials.GetCredentialBytes(ctx, r.Client, cps.Spec.Credentials)
		if err != nil {
			log.Printf("failed to get credentials for ClusterPullSecret %s: %v", name, err)
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
						{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       deployment.Name,
							UID:        deployment.UID,
							Controller: func() *bool { b := false; return &b }(),
						},
					},
				},
				Type: corev1.SecretTypeDockerConfigJson,
				Data: map[string][]byte{
					keyName: val,
				},
			}
			if err := r.Client.Create(ctx, newSecret); err != nil {
				log.Printf("failed to create secret %s/%s: %v", deployment.Namespace, credentials.SecretPrefix+name, err)
				continue
			}

			r.Recorder.Eventf(deployment, "Normal", "Created", "Created pull secret %s/%s", deployment.Namespace, credentials.SecretPrefix+name)
			continue
		} else if err != nil {
			log.Printf("error fetching secret %s/%s: %v", deployment.Namespace, credentials.SecretPrefix+name, err)
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
			owner := metav1.OwnerReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       deployment.Name,
				UID:        deployment.UID,
				Controller: func() *bool { b := false; return &b }(),
			}
			target.OwnerReferences = append(target.OwnerReferences, owner)
			changed = true
		}

		if changed {
			if err := r.Client.Update(ctx, target); err != nil {
				log.Printf("failed to update secret %s/%s: %v", deployment.Namespace, credentials.SecretPrefix+name, err)
				continue
			}
			r.Recorder.Eventf(deployment, "Normal", "Updated", "Updated pull secret %s/%s", deployment.Namespace, credentials.SecretPrefix+name)
		}
	}

	return reconcile.Result{}, nil
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

func (r *DeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("clusterpullsecret-controller")
	err := ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Complete(r)

	return err
}
