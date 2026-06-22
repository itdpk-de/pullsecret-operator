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
// Exactly one of secretRef, dockerConfigJson, registryCredentials or encryptedData
// (deprecated) must be set.
// +kubebuilder:validation:XValidation:rule="[has(self.secretRef), has(self.dockerConfigJson), has(self.registryCredentials), has(self.encryptedData)].filter(x, x).size() == 1",message="exactly one of secretRef, dockerConfigJson, registryCredentials or encryptedData must be set"
type PullSecretCredentials struct {
	// SecretRef points to a Secret whose referenced key already contains valid
	// .dockerconfigjson content. The value is copied verbatim into the generated Secret.
	// +optional
	SecretRef *SecretKeyReference `json:"secretRef,omitempty"`

	// DockerConfigJson holds inline .dockerconfigjson content, either as raw JSON or
	// base64-encoded JSON (the operator accepts both). This value is NOT encrypted and is
	// readable by anyone who can read the ClusterPullSecret; for sensitive credentials use
	// secretRef or registryCredentials and protect the referenced Secret at rest.
	// +optional
	DockerConfigJSON string `json:"dockerConfigJson,omitempty"`

	// RegistryCredentials references a Secret whose keys hold individual credential fields
	// (server, username, password, email). The operator assembles the .dockerconfigjson
	// automatically, so no credential data needs to be embedded in the ClusterPullSecret spec.
	// +optional
	RegistryCredentials *RegistryCredentialsRef `json:"registryCredentials,omitempty"`

	// EncryptedData is a deprecated alias for dockerConfigJson and is treated identically
	// (it is NOT decrypted). Use dockerConfigJson instead.
	//
	// Deprecated: use dockerConfigJson.
	// +optional
	EncryptedData string `json:"encryptedData,omitempty"`
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

	// RegistryAuth reports the result of the active login check against each registry
	// referenced by the credentials. Populated only when registry validation is enabled.
	// +optional
	// +listType=atomic
	RegistryAuth []RegistryAuthStatus `json:"registryAuth,omitempty"`
}

// RegistryAuthStatus reports whether a specific registry accepted the credentials.
type RegistryAuthStatus struct {
	// Registry is the registry server the credentials were tested against.
	Registry string `json:"registry"`

	// Authenticated is true if the registry accepted the credentials. A false value
	// with a reason of an explicit rejection (HTTP 401/403) indicates expired or wrong
	// credentials; an "unreachable" reason means the check was inconclusive.
	Authenticated bool `json:"authenticated"`

	// Reason explains the outcome (empty on success).
	// +optional
	Reason string `json:"reason,omitempty"`

	// LastCheckTime is when this registry was last checked.
	// +optional
	LastCheckTime *metav1.Time `json:"lastCheckTime,omitempty"`
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
