package credentials

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DockerConfigKeyName is the key name for Docker config JSON in secrets.
	// Secrets of type kubernetes.io/dockerconfigjson MUST store their payload
	// under exactly this key, so every credential source normalises to it.
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
// It always returns the data keyed under DockerConfigKeyName (.dockerconfigjson) so the
// generated Secret is a valid kubernetes.io/dockerconfigjson Secret regardless of source.
// It supports dockerConfigJson/encryptedData, secretRef, and registryCredentials methods,
// exactly one of which must be set. The reader is used for source-secret lookups and may
// be an uncached API reader (source secrets are intentionally not cached).
func GetCredentialBytes(ctx context.Context, c client.Reader, creds v1.PullSecretCredentials) ([]byte, string, error) {
	// Exactly one credential source must be provided.
	sources := 0
	if creds.SecretRef != nil {
		sources++
	}
	if creds.RegistryCredentials != nil {
		sources++
	}
	if creds.DockerConfigJSON != "" {
		sources++
	}
	if creds.EncryptedData != "" {
		sources++
	}
	switch {
	case sources == 0:
		return nil, "", ErrNoCredentials{}
	case sources > 1:
		return nil, "", ErrMultipleCredentials{}
	}

	// dockerConfigJson and its deprecated alias encryptedData are the inline source.
	inline := creds.DockerConfigJSON
	if inline == "" {
		inline = creds.EncryptedData
	}

	// dockerConfigJson / encryptedData: inline .dockerconfigjson content.
	if inline != "" {
		decoded, err := decodeInlineDockerConfig(inline)
		if err != nil {
			return nil, "", err
		}
		return decoded, DockerConfigKeyName, nil
	}

	// secretRef: the referenced value must already be valid .dockerconfigjson content.
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
		if err := validateDockerConfigJSON(v); err != nil {
			return nil, "", err
		}
		// Normalise to the canonical .dockerconfigjson key; the kube-apiserver
		// requires that key for kubernetes.io/dockerconfigjson Secrets.
		return v, DockerConfigKeyName, nil
	}

	// registryCredentials: build .dockerconfigjson from individual fields stored in a Secret.
	ref := creds.RegistryCredentials

	serverKey := defaultString(ref.ServerKey, "server")
	usernameKey := defaultString(ref.UsernameKey, "username")
	passwordKey := defaultString(ref.PasswordKey, "password")
	emailKey := defaultString(ref.EmailKey, "email")

	src := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.SecretName}, src); err != nil {
		return nil, "", err
	}

	server := strings.TrimSpace(string(src.Data[serverKey]))
	if server == "" {
		return nil, "", ErrMissingField{Field: serverKey, SecretName: ref.SecretName, Namespace: ref.Namespace}
	}
	username := strings.TrimSpace(string(src.Data[usernameKey]))
	if username == "" {
		return nil, "", ErrMissingField{Field: usernameKey, SecretName: ref.SecretName, Namespace: ref.Namespace}
	}
	// The password may legitimately contain leading/trailing whitespace, so only
	// trim for the emptiness check while preserving the raw value.
	password := string(src.Data[passwordKey])
	if strings.TrimSpace(password) == "" {
		return nil, "", ErrMissingField{Field: passwordKey, SecretName: ref.SecretName, Namespace: ref.Namespace}
	}
	// email is optional.
	email := strings.TrimSpace(string(src.Data[emailKey]))

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

// decodeInlineDockerConfig accepts inline credentials as either raw .dockerconfigjson or
// base64-encoded .dockerconfigjson (tolerating whitespace/newlines from YAML block
// scalars) and returns the validated raw JSON bytes.
func decodeInlineDockerConfig(s string) ([]byte, error) {
	trimmed := strings.TrimSpace(s)
	// Raw JSON object provided directly.
	if strings.HasPrefix(trimmed, "{") {
		if err := validateDockerConfigJSON([]byte(trimmed)); err != nil {
			return nil, err
		}
		return []byte(trimmed), nil
	}
	// Otherwise treat as base64-encoded JSON.
	cleaned := strings.Join(strings.Fields(s), "")
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, ErrInvalidDockerConfig{Reason: "value is neither valid JSON nor valid base64"}
	}
	if err := validateDockerConfigJSON(decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// validateDockerConfigJSON ensures data is a well-formed JSON object, mirroring the
// kube-apiserver validation for kubernetes.io/dockerconfigjson Secrets so we surface a
// clear error before attempting to write a Secret the API server would reject.
func validateDockerConfigJSON(data []byte) error {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return ErrInvalidDockerConfig{Reason: err.Error()}
	}
	return nil
}

func defaultString(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
