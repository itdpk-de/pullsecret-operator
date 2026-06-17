package credentials

import (
	"context"
	"encoding/base64"
	"encoding/json"

	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DockerConfigKeyName is the key name for Docker config JSON in secrets
	DockerConfigKeyName = ".dockerconfigjson"
	// SecretPrefix is the prefix for generated secrets
	SecretPrefix = "pullsecret-"
)

// dockerConfigAuth holds the per-registry authentication fields.
type dockerConfigAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
	Auth     string `json:"auth"`
}

// dockerConfig is the structure expected by the .dockerconfigjson Secret key.
type dockerConfig struct {
	Auths map[string]dockerConfigAuth `json:"auths"`
}

// GetCredentialBytes retrieves credential bytes from a ClusterPullSecret's credentials.
// It returns the credential data, the key name to use, and any error.
// It supports encryptedData, secretRef, and registryCredentials methods.
func GetCredentialBytes(ctx context.Context, c client.Client, creds v1.PullSecretCredentials) ([]byte, string, error) {
	// Prefer encryptedData if provided
	if creds.EncryptedData != "" {
		decoded, err := base64.StdEncoding.DecodeString(creds.EncryptedData)
		if err != nil {
			return nil, "", err
		}
		return decoded, DockerConfigKeyName, nil
	}

	// Fall back to secretRef
	if creds.SecretRef != nil {
		ref := creds.SecretRef
		src := &corev1.Secret{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, src); err != nil {
			return nil, "", err
		}

		v, ok := src.Data[ref.Key]
		if !ok {
			return nil, "", ErrKeyNotFound{Key: ref.Key, SecretName: ref.Name, Namespace: ref.Namespace}
		}
		return v, ref.Key, nil
	}

	// Build .dockerconfigjson from individual credential fields stored in a Secret
	if creds.RegistryCredentials != nil {
		ref := creds.RegistryCredentials

		serverKey := ref.ServerKey
		if serverKey == "" {
			serverKey = "server"
		}
		usernameKey := ref.UsernameKey
		if usernameKey == "" {
			usernameKey = "username"
		}
		passwordKey := ref.PasswordKey
		if passwordKey == "" {
			passwordKey = "password"
		}
		emailKey := ref.EmailKey
		if emailKey == "" {
			emailKey = "email"
		}

		src := &corev1.Secret{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.SecretName}, src); err != nil {
			return nil, "", err
		}

		serverBytes, ok := src.Data[serverKey]
		if !ok {
			return nil, "", ErrMissingField{SecretName: ref.SecretName, Namespace: ref.Namespace}
		}
		usernameBytes, ok := src.Data[usernameKey]
		if !ok {
			return nil, "", ErrMissingField{SecretName: ref.SecretName, Namespace: ref.Namespace}
		}
		passwordBytes, ok := src.Data[passwordKey]
		if !ok {
			return nil, "", ErrMissingField{SecretName: ref.SecretName, Namespace: ref.Namespace}
		}

		// Convert to strings once; email is optional.
		server := string(serverBytes)
		username := string(usernameBytes)
		password := string(passwordBytes)
		email := string(src.Data[emailKey])

		auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		cfg := dockerConfig{
			Auths: map[string]dockerConfigAuth{
				server: {
					Username: username,
					Password: password,
					Email:    email,
					Auth:     auth,
				},
			},
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			return nil, "", err
		}
		return data, DockerConfigKeyName, nil
	}

	return nil, "", ErrNoCredentials{}
}
