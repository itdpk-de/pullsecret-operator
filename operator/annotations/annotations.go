package annotations

import (
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	AnnotationClusterPullSecrets Annotation = "pullsecret.pascal-kutscha.de/cluster-pull-secrets"
	AnnotationSecretManagedBy    Annotation = "pullsecret.pascal-kutscha.de/managed-by-cluster-pull-secret"
)

type Annotation string

func GetClusterPullSecrets(annotations map[string]string) []string {
	if annotations == nil {
		return nil
	}

	raw, ok := annotations[string(AnnotationClusterPullSecrets)]
	if !ok {
		return nil
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		result = append(result, name)
	}

	return result
}
func (l Annotation) Get(object client.Object) string {
	return l.GetOrDefault(object, "")
}

func (l Annotation) GetOrDefault(object client.Object, defaultValue string) string {
	if annotations := object.GetAnnotations(); annotations != nil {
		if a, ok := annotations[string(l)]; ok {
			return a
		}
		return defaultValue
	}
	return defaultValue
}

func (l Annotation) Exists(object client.Object) bool {
	if annotations := object.GetAnnotations(); annotations != nil {
		_, ok := annotations[string(l)]
		return ok
	}
	return false
}

func (l Annotation) String() string {
	return string(l)
}
