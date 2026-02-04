package controllers

import (
	"bytes"
	"context"
	"encoding/base64"
	"log"

	"github.com/pascalku/pullsecret-operator/operator/annotations"
	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// If the object is being deleted, clean up managed Secrets and remove finalizer
	if !cps.DeletionTimestamp.IsZero() {
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

		// remove finalizer
		finalizers := make([]string, 0, len(cps.Finalizers))
		for _, f := range cps.Finalizers {
			if f == clusterFinalizer {
				continue
			}
			finalizers = append(finalizers, f)
		}
		cps.Finalizers = finalizers
		if err := r.Client.Update(ctx, cps); err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	// ensure finalizer exists
	hasFinalizer := false
	for _, f := range cps.Finalizers {
		if f == clusterFinalizer {
			hasFinalizer = true
			break
		}
	}
	if !hasFinalizer {
		cps.Finalizers = append(cps.Finalizers, clusterFinalizer)
		if err := r.Client.Update(ctx, cps); err != nil {
			return reconcile.Result{}, err
		}
	}

	// obtain credentials either from EncryptedData or SecretRef (backwards compatible)
	var val []byte
	var keyName string
	if cps.Spec.Credentials.EncryptedData != "" {
		decoded, err := base64.StdEncoding.DecodeString(cps.Spec.Credentials.EncryptedData)
		if err != nil {
			log.Printf("failed to decode encryptedData for ClusterPullSecret %s: %v", cps.Name, err)
			cps.Status.Synced = false
			cps.Status.Reason = "invalid-encrypted-data"
			_ = r.Client.Status().Update(ctx, cps)
			return reconcile.Result{}, nil
		}
		val = decoded
		keyName = ".dockerconfigjson"
	} else if cps.Spec.Credentials.SecretRef != nil {
		ref := cps.Spec.Credentials.SecretRef
		src := &corev1.Secret{}
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, src); err != nil {
			log.Printf("source secret %s/%s not found: %v", ref.Namespace, ref.Name, err)
			cps.Status.Synced = false
			cps.Status.Reason = "source-secret-not-found"
			_ = r.Client.Status().Update(ctx, cps)
			return reconcile.Result{}, nil
		}

		v, ok := src.Data[ref.Key]
		if !ok {
			log.Printf("key %s not found in secret %s/%s", ref.Key, ref.Namespace, ref.Name)
			cps.Status.Synced = false
			cps.Status.Reason = "source-secret-key-missing"
			_ = r.Client.Status().Update(ctx, cps)
			return reconcile.Result{}, nil
		}
		val = v
		keyName = ref.Key
	} else {
		log.Printf("no credentials specified for ClusterPullSecret %s, skipping", cps.Name)
		cps.Status.Synced = false
		cps.Status.Reason = "no-credentials"
		_ = r.Client.Status().Update(ctx, cps)
		return reconcile.Result{}, nil
	}

	// list all secrets and update those managed by this ClusterPullSecret
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

		// work on a copy to avoid pointer-to-loop issues
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

	cps.Status.ObservedGeneration = cps.Generation
	cps.Status.Synced = true
	cps.Status.Reason = ""
	_ = r.Client.Status().Update(ctx, cps)

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
