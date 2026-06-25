package webhook

import (
	"context"
	"encoding/json"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const annKey = "pullsecret.itdpk.de/cluster-pull-secrets"

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	return s
}

func newInjector(objs ...client.Object) *PodInjector {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	return &PodInjector{Client: c, Decoder: admission.NewDecoder(s)}
}

func applyPatch(raw []byte, resp admission.Response) ([]byte, error) {
	pb, err := json.Marshal(resp.Patches)
	if err != nil {
		return nil, err
	}
	patch, err := jsonpatch.DecodePatch(pb)
	if err != nil {
		return nil, err
	}
	return patch.Apply(raw)
}

func request(pod *corev1.Pod) admission.Request {
	raw, _ := json.Marshal(pod)
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Namespace: pod.Namespace,
		Object:    runtime.RawExtension{Raw: raw},
	}}
}

func injectedNames(t *testing.T, resp admission.Response, base *corev1.Pod) []string {
	t.Helper()
	if !resp.Allowed {
		t.Fatalf("response not allowed: %+v", resp.Result)
	}
	raw, _ := json.Marshal(base)
	patched, err := applyPatch(raw, resp)
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}
	var pod corev1.Pod
	if err := json.Unmarshal(patched, &pod); err != nil {
		t.Fatalf("unmarshal patched: %v", err)
	}
	names := make([]string, 0, len(pod.Spec.ImagePullSecrets))
	for _, s := range pod.Spec.ImagePullSecrets {
		names = append(names, s.Name)
	}
	return names
}

func TestInjectFromPodAnnotation(t *testing.T) {
	p := newInjector()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p", Annotations: map[string]string{annKey: "my-registry"}},
	}
	resp := p.Handle(context.Background(), request(pod))
	names := injectedNames(t, resp, pod)
	if len(names) != 1 || names[0] != "pullsecret-my-registry" {
		t.Fatalf("got %v, want [pullsecret-my-registry]", names)
	}
}

func TestInjectFromOwnerDeployment(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "web", UID: "dep-uid", Annotations: map[string]string{annKey: "reg-a,reg-b"}},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "web-123", UID: "rs-uid", OwnerReferences: []metav1.OwnerReference{
			{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "dep-uid"},
		}},
	}
	p := newInjector(dep, rs)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "web-123-abc", OwnerReferences: []metav1.OwnerReference{
			{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-123", UID: "rs-uid"},
		}},
	}
	resp := p.Handle(context.Background(), request(pod))
	names := injectedNames(t, resp, pod)
	if len(names) != 2 || names[0] != "pullsecret-reg-a" || names[1] != "pullsecret-reg-b" {
		t.Fatalf("got %v, want [pullsecret-reg-a pullsecret-reg-b]", names)
	}
}

func TestNoAnnotationNoPatch(t *testing.T) {
	p := newInjector()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p"}}
	resp := p.Handle(context.Background(), request(pod))
	if !resp.Allowed {
		t.Fatalf("expected allowed")
	}
	if len(resp.Patches) != 0 {
		t.Fatalf("expected no patches, got %v", resp.Patches)
	}
}

func TestDoesNotDuplicateExisting(t *testing.T) {
	p := newInjector()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p", Annotations: map[string]string{annKey: "my-registry"}},
		Spec:       corev1.PodSpec{ImagePullSecrets: []corev1.LocalObjectReference{{Name: "pullsecret-my-registry"}}},
	}
	resp := p.Handle(context.Background(), request(pod))
	if !resp.Allowed {
		t.Fatalf("expected allowed")
	}
	if len(resp.Patches) != 0 {
		t.Fatalf("expected no patches (already present), got %v", resp.Patches)
	}
}
