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

// ErrMissingField is returned when a required credential field key is not found in a secret
type ErrMissingField struct {
	SecretName string
	Namespace  string
}

func (e ErrMissingField) Error() string {
	return fmt.Sprintf("a required credential field is missing from secret %s/%s", e.Namespace, e.SecretName)
}

// ErrNoCredentials is returned when neither encryptedData, secretRef, nor registryCredentials is provided
type ErrNoCredentials struct{}

func (e ErrNoCredentials) Error() string {
	return "no credentials specified: one of encryptedData, secretRef, or registryCredentials must be provided"
}
