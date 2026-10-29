// Package registry asks an OCI registry whether it is there and whether the
// login AgentBox would push with is accepted.
//
// It does not push anything: the agent image is built and pushed by the docker or
// podman command line (internal/agentimage), and this is what says the address
// and the credential are right before that runs. The check is the distribution
// API's own: GET /v2/, follow the challenge it answers with, and ask again with
// what the challenge produced.
package registry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is one registry, as AgentBox is configured for it.
type Client struct {
	// URL is the repository prefix, like hub.example.com/you. The API itself is
	// at that host, not under the prefix.
	URL string
	// Username and Token are the login. Both empty is a registry that needs
	// none.
	Username string
	Token    string
	// CA is the registry's PEM certificate, when a private CA signed it.
	CA []byte
	// Insecure accepts a certificate no CA can verify, and falls back to http
	// for a registry that has no TLS at all. A local cluster only.
	Insecure bool
	// HTTP, when set, is what the requests go through. A test sets its own; nil
	// builds one from CA and Insecure.
	HTTP *http.Client
}

// Result is what a check found.
type Result struct {
	// OK says the registry answered and the login, when there is one, was
	// accepted.
	OK bool
	// Detail is what to tell the user: the address that answered, what it needs,
	// or the error.
	Detail string
}

// Timeout bounds one check when its context has no deadline of its own.
const Timeout = 30 * time.Second

// Check asks the registry whether it is there and whether the login works.
func (c Client) Check(ctx context.Context) Result {
	host, err := c.host()
	if err != nil {
		return Result{Detail: err.Error()}
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, Timeout)
		defer cancel()
	}
	client := c.client()
	var unreachable error
	for _, scheme := range c.schemes() {
		base := scheme + "://" + host
		status, challenge, err := c.request(ctx, client, base+"/v2/", "")
		if err != nil {
			unreachable = fmt.Errorf("%s: %w", base, err)
			continue
		}
		switch status {
		case http.StatusOK:
			if c.hasLogin() {
				return Result{OK: true, Detail: base + " answered, and pulls without a login: the stored login wasn't needed"}
			}
			return Result{OK: true, Detail: base + " answered, and pulls without a login"}
		case http.StatusUnauthorized:
			return c.login(ctx, client, base, challenge)
		default:
			return Result{Detail: fmt.Sprintf("%s answered %s (%d)", base, http.StatusText(status), status)}
		}
	}
	if unreachable == nil {
		unreachable = errors.New("the registry didn't answer")
	}
	return Result{Detail: unreachable.Error()}
}

// login follows the challenge a registry answers /v2/ with, and asks again with
// what it produced.
func (c Client) login(ctx context.Context, client *http.Client, base, header string) Result {
	scheme, params := parseChallenge(header)
	switch scheme {
	case "bearer":
		// A registry that serves public images hands out an anonymous token, so
		// this works with no login too, and then says so.
		token, err := c.bearerToken(ctx, client, params)
		if err != nil {
			return Result{Detail: err.Error()}
		}
		status, _, err := c.request(ctx, client, base+"/v2/", "Bearer "+token)
		if err != nil {
			return Result{Detail: fmt.Sprintf("%s: %v", base, err)}
		}
		if status == http.StatusOK {
			if c.hasLogin() {
				return Result{OK: true, Detail: base + " answered, and accepted the login"}
			}
			return Result{OK: true, Detail: base + " answered, and pulls without a login"}
		}
		return Result{Detail: refusedLogin(c, base, status)}
	case "basic":
		status, _, err := c.request(ctx, client, base+"/v2/", "Basic "+basicAuth(c.Username, c.Token))
		if err != nil {
			return Result{Detail: fmt.Sprintf("%s: %v", base, err)}
		}
		if status == http.StatusOK {
			return Result{OK: true, Detail: base + " answered, and accepted the login"}
		}
		return Result{Detail: refusedLogin(c, base, status)}
	}
	return Result{Detail: fmt.Sprintf("%s answered %s: %s", base, http.StatusText(http.StatusUnauthorized), header)}
}

// refusedLogin says what a rejected login means, without ever quoting it.
func refusedLogin(c Client, base string, status int) string {
	if !c.hasLogin() {
		return base + " needs a login, and none is stored: agentbox registry rotate " + c.URL
	}
	return fmt.Sprintf("%s refused the stored login (%s): agentbox registry rotate", base, http.StatusText(status))
}

// bearerToken trades the login for the token a registry's challenge asks for.
func (c Client) bearerToken(ctx context.Context, client *http.Client, params map[string]string) (string, error) {
	realm := params["realm"]
	if realm == "" {
		return "", errors.New("the registry wants a token and didn't say where from")
	}
	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("the registry's token address %q isn't a URL: %w", realm, err)
	}
	// The login goes to whatever address the challenge names, which may be
	// another host (Docker Hub's is auth.docker.io): over http it would cross
	// the network in the clear, so only a registry the user marked insecure
	// may name one.
	if tokenURL.Scheme != "https" && !(c.Insecure && tokenURL.Scheme == "http") {
		return "", fmt.Errorf("the registry wants a token from %s, which isn't https: AgentBox sends the login only over https, unless the registry is added with --insecure", tokenURL.Redacted())
	}
	query := tokenURL.Query()
	for _, name := range []string{"service", "scope"} {
		if value := params[name]; value != "" {
			query.Set(name, value)
		}
	}
	tokenURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", err
	}
	if c.hasLogin() {
		req.SetBasicAuth(c.Username, c.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("asking %s for a token: %w", tokenURL.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", errors.New(refusedLogin(c, tokenURL.Host, resp.StatusCode))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("asking %s for a token: %s", tokenURL.Host, resp.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	// A token can be large; a body larger than this is not one.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("the token %s answered with: %w", tokenURL.Host, err)
	}
	if body.Token != "" {
		return body.Token, nil
	}
	if body.AccessToken != "" {
		return body.AccessToken, nil
	}
	return "", fmt.Errorf("%s answered without a token", tokenURL.Host)
}

// request is one GET of the distribution API, answering with the status and the
// challenge a 401 carries.
func (c Client) request(ctx context.Context, client *http.Client, url, authorization string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	// The body is a status, never an image: read it out so the connection can be
	// used again.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, resp.Header.Get("Www-Authenticate"), nil
}

// host is the registry's API host: the first segment of the repository prefix.
func (c Client) host() (string, error) {
	prefix := strings.TrimSuffix(strings.TrimSpace(c.URL), "/")
	if prefix == "" {
		return "", errors.New("this registry has no url: set one with agentbox registry rotate or add it again")
	}
	host, _, _ := strings.Cut(prefix, "/")
	if host == "" {
		return "", fmt.Errorf("the registry url %q has no host", c.URL)
	}
	// docker.io is the name images are written with; its API is elsewhere.
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	return host, nil
}

// schemes is what to try: https, then http when the registry is marked
// insecure. A plain http registry is the one case where the second is right.
func (c Client) schemes() []string {
	if c.Insecure {
		return []string{"https", "http"}
	}
	return []string{"https"}
}

func (c Client) hasLogin() bool { return c.Username != "" || c.Token != "" }

// client is the HTTP client a check uses: the registry's own CA when it has
// one, no verification when it is insecure, and a timeout either way.
func (c Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.Insecure {
		tlsConfig.InsecureSkipVerify = true
	}
	if len(c.CA) > 0 {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(c.CA) {
			tlsConfig.RootCAs = pool
		}
	}
	return &http.Client{
		Timeout:   Timeout,
		Transport: &http.Transport{TLSClientConfig: tlsConfig, Proxy: http.ProxyFromEnvironment},
	}
}

// parseChallenge splits a WWW-Authenticate header into its scheme and its
// parameters: Bearer realm="…",service="…",scope="…".
func parseChallenge(header string) (string, map[string]string) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(header), " ")
	params := make(map[string]string)
	for _, part := range strings.Split(rest, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		params[strings.ToLower(strings.TrimSpace(name))] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return strings.ToLower(scheme), params
}

// basicAuth is the base64 an Authorization: Basic header carries.
func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}
