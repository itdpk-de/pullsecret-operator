// Package v1 contains the API Schema definitions for the pullsecret.itdpk.de v1 API group.
// +kubebuilder:object:generate=true
// +groupName=pullsecret.itdpk.de
package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "pullsecret.itdpk.de", Version: "v1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	// Register the types with the SchemeBuilder
	SchemeBuilder.Register(&ClusterPullSecret{}, &ClusterPullSecretList{})
}
