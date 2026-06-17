package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterPullSecretSpec defines the desired state of ClusterPullSecret.
type ClusterPullSecretSpec struct {
	// Credentials defines secure access to the registry or container registries.
	// It may reference an existing Secret (secretRef) or contain encrypted credential data.
	Credentials PullSecretCredentials `json:"credentials"`
}

// PullSecretCredentials selects the source of the registry credentials.
// Exactly one of secretRef, encryptedData or registryCredentials must be set.
// +kubebuilder:validation:XValidation:rule="[has(self.secretRef), has(self.encryptedData), has(self.registryCredentials)].filter(x, x).size() == 1",message="exactly one of secretRef, encryptedData or registryCredentials must be set"
type PullSecretCredentials struct {
	// SecretRef points to a Secret whose referenced key already contains valid
	// .dockerconfigjson content. The value is copied verbatim into the generated Secret.
	// +optional
	SecretRef *SecretKeyReference `json:"secretRef,omitempty"`

	// EncryptedData contains base64-encoded .dockerconfigjson content.
	//
	// NOTE: despite the historical name, the operator does NOT decrypt this value;
	// it only base64-decodes it, and the result must be valid .dockerconfigjson.
	// For real encryption-at-rest, keep the credentials in a Secret and reference it
	// via secretRef or registryCredentials, protecting that Secret with SOPS, Sealed
	// Secrets, or a secrets manager.
	// +optional
	EncryptedData string `json:"encryptedData,omitempty"`

	// RegistryCredentials references a Secret whose keys hold individual credential fields
	// (server, username, password, email). The operator assembles the .dockerconfigjson
	// automatically, so no credential data needs to be embedded in the ClusterPullSecret spec.
	// +optional
	RegistryCredentials *RegistryCredentialsRef `json:"registryCredentials,omitempty"`
}

// RegistryCredentialsRef references a Kubernetes Secret that contains individual registry
// credential fields. The operator reads these fields and builds the .dockerconfigjson format,
// keeping sensitive data confined to the Secret and out of the ClusterPullSecret spec.
type RegistryCredentialsRef struct {
	// SecretName is the name of the Secret containing the credential fields.
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName"`

	// Namespace is the namespace of the Secret.
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// ServerKey is the key in the Secret whose value is the registry server address (e.g. "ghcr.io").
	// Defaults to "server" when not set.
	// +optional
	ServerKey string `json:"serverKey,omitempty"`

	// UsernameKey is the key in the Secret whose value is the registry username.
	// Defaults to "username" when not set.
	// +optional
	UsernameKey string `json:"usernameKey,omitempty"`

	// PasswordKey is the key in the Secret whose value is the registry password.
	// Defaults to "password" when not set.
	// +optional
	PasswordKey string `json:"passwordKey,omitempty"`

	// EmailKey is the key in the Secret whose value is the registry email address.
	// Defaults to "email" when not set. The email field is optional in the resulting config.
	// +optional
	EmailKey string `json:"emailKey,omitempty"`
}

// SecretKeyReference identifies a key within a Secret.
type SecretKeyReference struct {
	// Name is the name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace is the namespace of the Secret.
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// Key is the key within the Secret data whose value is valid .dockerconfigjson
	// content (commonly ".dockerconfigjson"). The value is copied verbatim, so it must
	// be a serialized ~/.docker/config.json, not a bare token or password.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

//
// Status
//

// ClusterPullSecretPhase is a coarse, human-readable summary of the sync state,
// suitable for dashboards and alerting.
type ClusterPullSecretPhase string

const (
	// PhaseSynced means the credentials are valid and all managed secrets are up to date.
	PhaseSynced ClusterPullSecretPhase = "Synced"
	// PhaseOutOfSync means the credentials could not be resolved/validated, so managed
	// secrets may be stale or missing.
	PhaseOutOfSync ClusterPullSecretPhase = "OutOfSync"
)

// ClusterPullSecretStatus defines the observed state of ClusterPullSecret.
type ClusterPullSecretStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase is a coarse, human-readable summary of the sync state (Synced/OutOfSync).
	// +optional
	Phase ClusterPullSecretPhase `json:"phase,omitempty"`

	// Synced indicates whether the pull secret is currently valid and usable.
	Synced bool `json:"synced,omitempty"`

	// Reason provides a human-readable explanation when the secret is not synced.
	Reason string `json:"reason,omitempty"`

	// LastSyncTime is the last time the secret was successfully reconciled.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// LastValidationTime is the last time the credentials were validated, regardless
	// of outcome. Useful to detect a stalled controller.
	// +optional
	LastValidationTime *metav1.Time `json:"lastValidationTime,omitempty"`
}

//
// Root objects
//

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cps
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Synced",type=boolean,JSONPath=`.status.synced`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.reason`
// +kubebuilder:printcolumn:name="Last-Sync",type=date,JSONPath=`.status.lastSyncTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterPullSecret is the Schema for the clusterpullsecrets API.
type ClusterPullSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterPullSecretSpec   `json:"spec,omitempty"`
	Status ClusterPullSecretStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterPullSecretList contains a list of ClusterPullSecret.
type ClusterPullSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterPullSecret `json:"items"`
}
