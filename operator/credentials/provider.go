package credentials

import (
	"context"
	"encoding/base64"

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

// GetCredentialBytes retrieves credential bytes from a ClusterPullSecret's credentials.
// It returns the credential data, the key name to use, and any error.
// It supports both encryptedData and secretRef methods.
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

	return nil, "", ErrNoCredentials{}
}
