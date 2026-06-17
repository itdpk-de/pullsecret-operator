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

// PullSecretCredentials references the secret holding registry credentials or embeds encrypted data.
type PullSecretCredentials struct {
	// SecretRef points to a Secret containing the credential material (backwards compatible).
	// +optional
	SecretRef *SecretKeyReference `json:"secretRef,omitempty"`

	// EncryptedData contains the credentials encrypted and base64-encoded.
	// The decrypted value must be the raw .dockerconfigjson content.
	// Recommended: encrypt with KMS or SOPS and store base64(encoded(ciphertext)).
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

	// Key is the key within the Secret data.
	// Example: token, password, .dockerconfigjson
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

//
// Status
//

// ClusterPullSecretStatus defines the observed state of ClusterPullSecret.
type ClusterPullSecretStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Synced indicates whether the pull secret is currently valid and usable.
	Synced bool `json:"synced,omitempty"`

	// Reason provides a human-readable explanation when the secret is not synced.
	Reason string `json:"reason,omitempty"`

	// LastSyncTime is the last time the secret was successfully reconciled.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
}

//
// Root objects
//

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cps

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
