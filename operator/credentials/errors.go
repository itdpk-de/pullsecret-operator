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

// ErrNoCredentials is returned when neither encryptedData nor secretRef is provided
type ErrNoCredentials struct{}

func (e ErrNoCredentials) Error() string {
	return "no credentials specified: neither encryptedData nor secretRef is provided"
}
