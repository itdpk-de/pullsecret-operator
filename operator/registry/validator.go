// Package registry performs best-effort, active validation of registry credentials by
// attempting an authenticated request against each registry referenced by a
// .dockerconfigjson document. It distinguishes "definitely wrong/expired credentials"
// (an explicit 401) from "could not reach the registry" so that blocked egress does not
// produce false alarms.
package registry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// State is the outcome of validating a single registry's credentials.
type State string

const (
	// StateOK means the registry accepted the credentials.
	StateOK State = "OK"
	// StateAuthFailed means the registry explicitly rejected the credentials (HTTP 401).
	StateAuthFailed State = "AuthFailed"
	// StateUnreachable means the registry could not be reached or returned an
	// unexpected status; the credentials are neither confirmed nor refuted.
	StateUnreachable State = "Unreachable"
)

// Result is the validation outcome for one registry.
type Result struct {
	Registry string
	State    State
	Reason   string
}

// Validator validates the credentials embedded in a .dockerconfigjson document.
type Validator interface {
	Validate(ctx context.Context, dockerConfigJSON []byte) []Result
}

// HTTPValidator validates credentials by talking to the Docker Registry v2 API.
type HTTPValidator struct {
	Client *http.Client
}

// NewHTTPValidator returns an HTTPValidator with a sensible default timeout. It honors
// HTTP(S)_PROXY/NO_PROXY from the environment. If caBundlePath is non-empty, the PEM file
// at that path is added to the system trust pool so private registries with a custom CA
// can be validated.
//
// Note: this is a best-effort check. Registries that hand out anonymous tokens even for
// invalid credentials may report OK; treat StateOK as "not definitively broken" rather
// than a hard guarantee.
func NewHTTPValidator(timeout time.Duration, caBundlePath string) *HTTPValidator {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone() // keeps Proxy: ProxyFromEnvironment
	if caBundlePath != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if pem, err := os.ReadFile(caBundlePath); err != nil {
			log.Printf("registry validator: could not read CA bundle %s: %v", caBundlePath, err)
		} else if !pool.AppendCertsFromPEM(pem) {
			log.Printf("registry validator: no valid certificates in CA bundle %s", caBundlePath)
		} else {
			transport.TLSClientConfig = &tls.Config{RootCAs: pool}
		}
	}
	return &HTTPValidator{Client: &http.Client{Timeout: timeout, Transport: transport}}
}

type dockerConfig struct {
	Auths map[string]struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Auth     string `json:"auth"`
	} `json:"auths"`
}

// Validate parses the document and checks every registry entry. Entries without usable
// credentials are skipped (not reported).
func (v *HTTPValidator) Validate(ctx context.Context, dockerConfigJSON []byte) []Result {
	var cfg dockerConfig
	if err := json.Unmarshal(dockerConfigJSON, &cfg); err != nil {
		return []Result{{Registry: "", State: StateUnreachable, Reason: "invalid .dockerconfigjson"}}
	}

	var results []Result
	for reg, a := range cfg.Auths {
		user, pass := a.Username, a.Password
		if user == "" && a.Auth != "" {
			user, pass = decodeAuth(a.Auth)
		}
		if user == "" && pass == "" {
			continue
		}
		results = append(results, v.validateOne(ctx, reg, user, pass))
	}
	return results
}

func (v *HTTPValidator) validateOne(ctx context.Context, registry, user, pass string) Result {
	base := normalizeRegistry(registry)
	res := Result{Registry: registry}

	v2 := base + "/v2/"
	resp, err := v.do(ctx, http.MethodGet, v2, "")
	if err != nil {
		res.State, res.Reason = StateUnreachable, err.Error()
		return res
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		res.State = StateOK
		return res
	case http.StatusUnauthorized:
		// fall through to the challenge handling below
	default:
		res.State, res.Reason = StateUnreachable, fmt.Sprintf("unexpected status %d from %s", resp.StatusCode, v2)
		return res
	}

	challenge := parseAuthHeader(resp.Header.Get("WWW-Authenticate"))
	switch challenge.scheme {
	case "bearer":
		return v.checkBearer(ctx, registry, challenge, user, pass)
	case "basic", "":
		return v.checkBasic(ctx, registry, v2, user, pass)
	default:
		res.State, res.Reason = StateUnreachable, "unsupported auth scheme "+challenge.scheme
		return res
	}
}

func (v *HTTPValidator) checkBasic(ctx context.Context, registry, v2, user, pass string) Result {
	res := Result{Registry: registry}
	resp, err := v.do(ctx, http.MethodGet, v2, basicAuth(user, pass))
	if err != nil {
		res.State, res.Reason = StateUnreachable, err.Error()
		return res
	}
	resp.Body.Close()
	return classify(registry, resp.StatusCode)
}

func (v *HTTPValidator) checkBearer(ctx context.Context, registry string, c authChallenge, user, pass string) Result {
	res := Result{Registry: registry}
	if c.realm == "" {
		res.State, res.Reason = StateUnreachable, "bearer challenge without realm"
		return res
	}
	u, err := url.Parse(c.realm)
	if err != nil {
		res.State, res.Reason = StateUnreachable, "invalid bearer realm"
		return res
	}
	q := u.Query()
	if c.service != "" {
		q.Set("service", c.service)
	}
	if c.scope != "" {
		q.Set("scope", c.scope)
	}
	u.RawQuery = q.Encode()

	resp, err := v.do(ctx, http.MethodGet, u.String(), basicAuth(user, pass))
	if err != nil {
		res.State, res.Reason = StateUnreachable, err.Error()
		return res
	}
	resp.Body.Close()
	return classify(registry, resp.StatusCode)
}

func classify(registry string, status int) Result {
	res := Result{Registry: registry}
	switch {
	case status >= 200 && status < 300:
		res.State = StateOK
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		res.State, res.Reason = StateAuthFailed, fmt.Sprintf("registry rejected credentials (HTTP %d)", status)
	default:
		res.State, res.Reason = StateUnreachable, fmt.Sprintf("unexpected status %d", status)
	}
	return res
}

func (v *HTTPValidator) do(ctx context.Context, method, rawurl, authz string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawurl, nil)
	if err != nil {
		return nil, err
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	return v.Client.Do(req)
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func decodeAuth(auth string) (string, string) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth))
	if err != nil {
		return "", ""
	}
	user, pass, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", ""
	}
	return user, pass
}

// normalizeRegistry turns a docker config registry key into a base URL (scheme + host).
// The legacy Docker Hub key is mapped to the real v2 endpoint.
func normalizeRegistry(reg string) string {
	if reg == "https://index.docker.io/v1/" || reg == "index.docker.io" || reg == "docker.io" {
		return "https://registry-1.docker.io"
	}
	scheme := "https"
	host := reg
	if i := strings.Index(reg, "://"); i >= 0 {
		scheme = reg[:i]
		host = reg[i+3:]
	}
	// Drop any path/port-less trailing slash segments, keep host[:port].
	host = strings.TrimSuffix(host, "/")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	return scheme + "://" + host
}

type authChallenge struct {
	scheme  string
	realm   string
	service string
	scope   string
}

// parseAuthHeader parses a WWW-Authenticate header such as:
//
//	Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="..."
func parseAuthHeader(h string) authChallenge {
	c := authChallenge{}
	h = strings.TrimSpace(h)
	if h == "" {
		return c
	}
	parts := strings.SplitN(h, " ", 2)
	c.scheme = strings.ToLower(parts[0])
	if len(parts) < 2 {
		return c
	}
	for _, kv := range splitParams(parts[1]) {
		k, val, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"`)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "realm":
			c.realm = val
		case "service":
			c.service = val
		case "scope":
			c.scope = val
		}
	}
	return c
}

// splitParams splits comma-separated key="value" pairs, respecting quotes.
func splitParams(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			cur.WriteRune(r)
		case r == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
