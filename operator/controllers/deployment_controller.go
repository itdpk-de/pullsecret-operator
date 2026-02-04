package controllers

import (
	"bytes"
	"context"
	"encoding/base64"
	"log"

	"github.com/pascalku/pullsecret-operator/operator/annotations"
	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		log.Printf("FTPServer %s/%s not found, ignoring.", req.Namespace, req.Name)
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
			if errors.IsNotFound(err) {
				log.Printf("ClusterPullSecret %s not found, skipping.", name)
				continue
			}
			log.Printf("error fetching ClusterPullSecret %s: %v", name, err)
			continue
		}

		// obtain credential bytes either from EncryptedData (preferred) or SecretRef
		var val []byte
		var keyName string
		if cps.Spec.Credentials.EncryptedData != "" {
			decoded, err := base64.StdEncoding.DecodeString(cps.Spec.Credentials.EncryptedData)
			if err != nil {
				log.Printf("failed to decode encryptedData for ClusterPullSecret %s: %v", name, err)
				continue
			}
			val = decoded
			keyName = ".dockerconfigjson"
		} else if cps.Spec.Credentials.SecretRef != nil {
			ref := cps.Spec.Credentials.SecretRef
			src := &corev1.Secret{}
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, src); err != nil {
				log.Printf("source secret %s/%s not found: %v", ref.Namespace, ref.Name, err)
				continue
			}

			v, ok := src.Data[ref.Key]
			if !ok {
				log.Printf("key %s not found in secret %s/%s", ref.Key, ref.Namespace, ref.Name)
				continue
			}
			val = v
			keyName = ref.Key
		} else {
			log.Printf("no credentials specified for ClusterPullSecret %s, skipping", name)
			continue
		}

		// target secret lives in the deployment's namespace and uses the ClusterPullSecret's name
		target := &corev1.Secret{}
		targetKey := client.ObjectKey{Namespace: deployment.Namespace, Name: "pullsecret-" + name}
		err := r.Client.Get(ctx, targetKey, target)
		if errors.IsNotFound(err) {
			newSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: deployment.Namespace,
					Name:      "pullsecret-" + name,
					Annotations: map[string]string{
						annotations.AnnotationSecretManagedBy.String(): name,
					},
				},
				Type: corev1.SecretTypeDockerConfigJson,
				Data: map[string][]byte{
					keyName: val,
				},
			}
			if err := r.Client.Create(ctx, newSecret); err != nil {
				log.Printf("failed to create secret %s/%s: %v", deployment.Namespace, "pullsecret-"+name, err)
				continue
			}
			// patch owner reference to include this deployment
			ret := &corev1.Secret{}
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: deployment.Namespace, Name: "pullsecret-" + name}, ret); err == nil {
				owner := metav1.OwnerReference{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       deployment.Name,
					UID:        deployment.UID,
					Controller: func() *bool { b := false; return &b }(),
				}
				ret.OwnerReferences = append(ret.OwnerReferences, owner)
				if err := r.Client.Update(ctx, ret); err != nil {
					log.Printf("failed to patch ownerrefs for secret %s/%s: %v", deployment.Namespace, "pullsecret-"+name, err)
				}
			}

			r.Recorder.Eventf(deployment, "Normal", "Created", "Created pull secret %s/%s", deployment.Namespace, "pullsecret-"+name)
			continue
		} else if err != nil {
			log.Printf("error fetching secret %s/%s: %v", deployment.Namespace, "pullsecret-"+name, err)
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
		if target.Annotations[string(annotations.AnnotationSecretManagedBy)] != name {
			target.Annotations[string(annotations.AnnotationSecretManagedBy)] = name
			changed = true
		}

		// ensure owner reference to deployment exists
		ownerFound := false
		for _, or := range target.OwnerReferences {
			if or.UID == deployment.UID && or.Kind == "Deployment" && or.Name == deployment.Name {
				ownerFound = true
				break
			}
		}
		if !ownerFound {
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
				log.Printf("failed to update secret %s/%s: %v", deployment.Namespace, "pullsecret-"+name, err)
				continue
			}
			r.Recorder.Eventf(deployment, "Normal", "Updated", "Updated pull secret %s/%s", deployment.Namespace, "pullsecret-"+name)
		}
	}

	return reconcile.Result{}, nil
}

func (r *DeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("clusterpullsecret-controller")
	err := ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Complete(r)

	return err
}
