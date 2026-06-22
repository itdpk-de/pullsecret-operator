package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func dockerCfg(reg, user, pass string) []byte {
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	m := map[string]any{
		"auths": map[string]any{
			reg: map[string]any{"auth": auth},
		},
	}
	b, _ := json.Marshal(m)
	return b
}

func TestValidateBasicAuthSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", "Basic realm=\"registry\"")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if u == "user" && p == "pass" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	v := NewHTTPValidator(2*time.Second, "")
	results := v.Validate(context.Background(), dockerCfg(srv.URL, "user", "pass"))
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].State != StateOK {
		t.Fatalf("state = %v (%s), want OK", results[0].State, results[0].Reason)
	}
}

func TestValidateBasicAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Basic realm=\"registry\"")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	v := NewHTTPValidator(2*time.Second, "")
	results := v.Validate(context.Background(), dockerCfg(srv.URL, "user", "wrong"))
	if results[0].State != StateAuthFailed {
		t.Fatalf("state = %v, want AuthFailed", results[0].State)
	}
}

func TestValidateBearerSuccess(t *testing.T) {
	var tokenURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			u, p, _ := r.BasicAuth()
			if u == "user" && p == "pass" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"token":"abc"}`))
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// /v2/ challenge
		w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%q,service=\"reg\"", tokenURL))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	tokenURL = srv.URL + "/token"

	v := NewHTTPValidator(2*time.Second, "")
	results := v.Validate(context.Background(), dockerCfg(srv.URL, "user", "pass"))
	if results[0].State != StateOK {
		t.Fatalf("state = %v (%s), want OK", results[0].State, results[0].Reason)
	}
}

func TestValidateBearerAuthFailed(t *testing.T) {
	var tokenURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%q,service=\"reg\"", tokenURL))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	tokenURL = srv.URL + "/token"

	v := NewHTTPValidator(2*time.Second, "")
	results := v.Validate(context.Background(), dockerCfg(srv.URL, "user", "bad"))
	if results[0].State != StateAuthFailed {
		t.Fatalf("state = %v, want AuthFailed", results[0].State)
	}
}

func TestValidateUnreachable(t *testing.T) {
	v := NewHTTPValidator(500*time.Millisecond, "")
	// Reserved TEST-NET-1 address, should not connect.
	results := v.Validate(context.Background(), dockerCfg("http://192.0.2.1:9", "user", "pass"))
	if results[0].State != StateUnreachable {
		t.Fatalf("state = %v, want Unreachable", results[0].State)
	}
}

func TestParseAuthHeader(t *testing.T) {
	c := parseAuthHeader(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:x:pull"`)
	if c.scheme != "bearer" || c.realm != "https://auth.docker.io/token" || c.service != "registry.docker.io" {
		t.Fatalf("parsed = %+v", c)
	}
}

func TestNormalizeRegistry(t *testing.T) {
	cases := map[string]string{
		"ghcr.io":                      "https://ghcr.io",
		"https://index.docker.io/v1/":  "https://registry-1.docker.io",
		"http://localhost:5000":        "http://localhost:5000",
		"https://my.reg.io/some/path":  "https://my.reg.io",
	}
	for in, want := range cases {
		if got := normalizeRegistry(in); got != want {
			t.Errorf("normalizeRegistry(%q) = %q, want %q", in, got, want)
		}
	}
}
