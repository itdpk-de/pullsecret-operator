package credentials

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	v1 "github.com/pascalku/pullsecret-operator/pkg/apis/pullsecret.pascal-kutscha.de/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const validDockerConfig = `{"auths":{"ghcr.io":{"auth":"dXNlcjpwYXNz"}}}`

func newClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func secret(ns, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Data:       data,
	}
}

func TestEncryptedData(t *testing.T) {
	c := newClient()
	enc := base64.StdEncoding.EncodeToString([]byte(validDockerConfig))
	creds := v1.PullSecretCredentials{EncryptedData: enc}

	got, key, err := GetCredentialBytes(context.Background(), c, creds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != DockerConfigKeyName {
		t.Fatalf("key = %q, want %q", key, DockerConfigKeyName)
	}
	if string(got) != validDockerConfig {
		t.Fatalf("got %q, want %q", got, validDockerConfig)
	}
}

func TestEncryptedDataToleratesWhitespace(t *testing.T) {
	c := newClient()
	enc := base64.StdEncoding.EncodeToString([]byte(validDockerConfig))
	// Simulate a YAML block scalar that wraps the base64 across lines.
	multiline := enc[:8] + "\n  " + enc[8:] + "\n"
	creds := v1.PullSecretCredentials{EncryptedData: multiline}

	got, _, err := GetCredentialBytes(context.Background(), c, creds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != validDockerConfig {
		t.Fatalf("got %q, want %q", got, validDockerConfig)
	}
}

func TestEncryptedDataInvalidJSON(t *testing.T) {
	c := newClient()
	enc := base64.StdEncoding.EncodeToString([]byte("not-json-ciphertext"))
	creds := v1.PullSecretCredentials{EncryptedData: enc}

	_, _, err := GetCredentialBytes(context.Background(), c, creds)
	var invalid ErrInvalidDockerConfig
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want ErrInvalidDockerConfig", err)
	}
}

func TestSecretRefNormalisesKey(t *testing.T) {
	// A secretRef pointing at an arbitrary key must still produce a Secret keyed
	// under .dockerconfigjson, otherwise the kube-apiserver rejects it.
	src := secret("default", "creds", map[string][]byte{"token": []byte(validDockerConfig)})
	c := newClient(src)
	creds := v1.PullSecretCredentials{
		SecretRef: &v1.SecretKeyReference{Namespace: "default", Name: "creds", Key: "token"},
	}

	got, key, err := GetCredentialBytes(context.Background(), c, creds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != DockerConfigKeyName {
		t.Fatalf("key = %q, want %q", key, DockerConfigKeyName)
	}
	if string(got) != validDockerConfig {
		t.Fatalf("got %q, want %q", got, validDockerConfig)
	}
}

func TestSecretRefInvalidJSON(t *testing.T) {
	src := secret("default", "creds", map[string][]byte{".dockerconfigjson": []byte("plain-token")})
	c := newClient(src)
	creds := v1.PullSecretCredentials{
		SecretRef: &v1.SecretKeyReference{Namespace: "default", Name: "creds", Key: ".dockerconfigjson"},
	}

	_, _, err := GetCredentialBytes(context.Background(), c, creds)
	var invalid ErrInvalidDockerConfig
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want ErrInvalidDockerConfig", err)
	}
}

func TestRegistryCredentialsBuildsConfig(t *testing.T) {
	src := secret("default", "reg", map[string][]byte{
		"server":   []byte("ghcr.io"),
		"username": []byte("user"),
		"password": []byte("pass"),
	})
	c := newClient(src)
	creds := v1.PullSecretCredentials{
		RegistryCredentials: &v1.RegistryCredentialsRef{Namespace: "default", SecretName: "reg"},
	}

	got, key, err := GetCredentialBytes(context.Background(), c, creds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != DockerConfigKeyName {
		t.Fatalf("key = %q, want %q", key, DockerConfigKeyName)
	}

	var cfg dockerConfig
	if err := json.Unmarshal(got, &cfg); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	auth, ok := cfg.Auths["ghcr.io"]
	if !ok {
		t.Fatalf("no auth entry for ghcr.io in %s", got)
	}
	wantAuth := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if auth.Auth != wantAuth {
		t.Fatalf("auth = %q, want %q", auth.Auth, wantAuth)
	}
}

func TestRegistryCredentialsMissingField(t *testing.T) {
	src := secret("default", "reg", map[string][]byte{
		"server":   []byte("ghcr.io"),
		"username": []byte("user"),
		// password missing
	})
	c := newClient(src)
	creds := v1.PullSecretCredentials{
		RegistryCredentials: &v1.RegistryCredentialsRef{Namespace: "default", SecretName: "reg"},
	}

	_, _, err := GetCredentialBytes(context.Background(), c, creds)
	var missing ErrMissingField
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want ErrMissingField", err)
	}
	if missing.Field != "password" {
		t.Fatalf("missing field = %q, want password", missing.Field)
	}
}

func TestMultipleSources(t *testing.T) {
	c := newClient()
	creds := v1.PullSecretCredentials{
		EncryptedData: base64.StdEncoding.EncodeToString([]byte(validDockerConfig)),
		SecretRef:     &v1.SecretKeyReference{Namespace: "default", Name: "creds", Key: "x"},
	}

	_, _, err := GetCredentialBytes(context.Background(), c, creds)
	var multi ErrMultipleCredentials
	if !errors.As(err, &multi) {
		t.Fatalf("err = %v, want ErrMultipleCredentials", err)
	}
}

func TestNoSources(t *testing.T) {
	c := newClient()
	_, _, err := GetCredentialBytes(context.Background(), c, v1.PullSecretCredentials{})
	var none ErrNoCredentials
	if !errors.As(err, &none) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
}
