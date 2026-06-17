package credentials

import "fmt"

// ErrKeyNotFound is returned when a key is not found in a secret
type ErrKeyNotFound struct {
	Key        string
	SecretName string
	Namespace  string
}

func (e ErrKeyNotFound) Error() string {
	return fmt.Sprintf("key %s not found in secret %s/%s", e.Key, e.Namespace, e.SecretName)
}

// ErrMissingField is returned when a required credential field key is not found in
// a secret, or its value is empty.
type ErrMissingField struct {
	Field      string
	SecretName string
	Namespace  string
}

func (e ErrMissingField) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("required credential field %q is missing or empty in secret %s/%s", e.Field, e.Namespace, e.SecretName)
	}
	return fmt.Sprintf("a required credential field is missing from secret %s/%s", e.Namespace, e.SecretName)
}

// ErrInvalidDockerConfig is returned when the resolved credential bytes are not
// valid .dockerconfigjson content. The kube-apiserver rejects Secrets of type
// kubernetes.io/dockerconfigjson whose .dockerconfigjson value is not valid JSON,
// so we fail fast with a clear reason instead.
type ErrInvalidDockerConfig struct {
	Reason string
}

func (e ErrInvalidDockerConfig) Error() string {
	if e.Reason != "" {
		return "credential data is not valid .dockerconfigjson: " + e.Reason
	}
	return "credential data is not valid .dockerconfigjson"
}

// ErrMultipleCredentials is returned when more than one credential source
// (encryptedData, secretRef, registryCredentials) is set on a single
// ClusterPullSecret. Exactly one must be provided.
type ErrMultipleCredentials struct{}

func (e ErrMultipleCredentials) Error() string {
	return "multiple credential sources specified: exactly one of encryptedData, secretRef, or registryCredentials must be provided"
}

// ErrNoCredentials is returned when neither encryptedData, secretRef, nor registryCredentials is provided
type ErrNoCredentials struct{}

func (e ErrNoCredentials) Error() string {
	return "no credentials specified: one of encryptedData, secretRef, or registryCredentials must be provided"
}
