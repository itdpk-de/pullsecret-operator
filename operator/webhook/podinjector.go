// Package webhook implements a mutating admission webhook that injects imagePullSecrets
// into Pods based on the cluster-pull-secrets annotation. It mutates the Pod, not the
// owning workload, so it is invisible to GitOps controllers (Argo CD/Flux) and does not
// cause reconciliation loops.
package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/itdpk-de/pullsecret-operator/operator/annotations"
	"github.com/itdpk-de/pullsecret-operator/operator/credentials"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// maxOwnerDepth bounds the owner-reference walk (Pod -> ReplicaSet -> Deployment, or
// Pod -> Job -> CronJob), guarding against pathological/cyclic owner chains.
const maxOwnerDepth = 4

// PodInjector injects imagePullSecrets into Pods.
type PodInjector struct {
	Client  client.Client
	Decoder admission.Decoder
}

// Handle implements admission.Handler.
func (p *PodInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	if err := p.Decoder.Decode(req, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	namespace := pod.Namespace
	if namespace == "" {
		namespace = req.Namespace
	}

	names := p.resolve(ctx, namespace, pod.GetAnnotations(), pod.GetOwnerReferences(), 0)
	if len(names) == 0 {
		return admission.Allowed("no cluster-pull-secrets annotation")
	}

	existing := make(map[string]bool, len(pod.Spec.ImagePullSecrets))
	for _, s := range pod.Spec.ImagePullSecrets {
		existing[s.Name] = true
	}

	changed := false
	for _, n := range names {
		secretName := credentials.SecretPrefix + n
		if existing[secretName] {
			continue
		}
		pod.Spec.ImagePullSecrets = append(pod.Spec.ImagePullSecrets, corev1.LocalObjectReference{Name: secretName})
		existing[secretName] = true
		changed = true
	}
	if !changed {
		return admission.Allowed("imagePullSecrets already present")
	}

	marshaled, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaled)
}

// resolve returns the desired ClusterPullSecret names: the Pod's own annotation if
// present, otherwise the annotation found by walking up the owner chain to the
// originating built-in workload.
func (p *PodInjector) resolve(ctx context.Context, namespace string, ann map[string]string, owners []metav1.OwnerReference, depth int) []string {
	if names := annotations.GetClusterPullSecrets(ann); len(names) > 0 {
		return names
	}
	if depth >= maxOwnerDepth {
		return nil
	}
	for _, ref := range owners {
		ownerAnn, ownerOwners, ok := p.lookupOwner(ctx, namespace, ref)
		if !ok {
			continue
		}
		if names := p.resolve(ctx, namespace, ownerAnn, ownerOwners, depth+1); len(names) > 0 {
			return names
		}
	}
	return nil
}

// lookupOwner fetches a supported owner object and returns its annotations and owner
// references. Only built-in workload kinds are followed.
func (p *PodInjector) lookupOwner(ctx context.Context, namespace string, ref metav1.OwnerReference) (map[string]string, []metav1.OwnerReference, bool) {
	var obj client.Object
	switch ref.APIVersion + "/" + ref.Kind {
	case "apps/v1/Deployment":
		obj = &appsv1.Deployment{}
	case "apps/v1/StatefulSet":
		obj = &appsv1.StatefulSet{}
	case "apps/v1/DaemonSet":
		obj = &appsv1.DaemonSet{}
	case "apps/v1/ReplicaSet":
		obj = &appsv1.ReplicaSet{}
	case "batch/v1/Job":
		obj = &batchv1.Job{}
	case "batch/v1/CronJob":
		obj = &batchv1.CronJob{}
	default:
		return nil, nil, false
	}

	if err := p.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, obj); err != nil {
		return nil, nil, false
	}
	return obj.GetAnnotations(), obj.GetOwnerReferences(), true
}
