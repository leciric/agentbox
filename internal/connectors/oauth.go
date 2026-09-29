package connectors

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The OAuth client, as the MCP authorization spec (2025-06-18) has it: an MCP
// server is an OAuth protected resource (RFC 9728) that names its
// authorization server, whose metadata (RFC 8414) says where to register a
// client (RFC 7591), where to send the user, and where to trade the code for
// tokens — with PKCE and the resource indicator (RFC 8707) on the way. Nothing
// here stores anything: the Service keeps what it finds.

// ResourceMetadata is an MCP server's protected resource metadata.
type ResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

// ServerMetadata is an authorization server's metadata, the parts used here.
type ServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ScopesSupported                   []string `json:"scopes_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

// Discovery is what finding a server's authorization out gives: the
// authorization server, the resource to ask a token for, and the scope.
type Discovery struct {
	Server ServerMetadata
	// Resource is sent as RFC 8707's resource parameter: the server's
	// canonical URI, from its metadata when it has some.
	Resource string
	// Scope is what to ask for: what the server's 401 said, or else what its
	// resource metadata supports. Empty asks for the server's default.
	Scope string
}

// Client is a registered OAuth client.
type Client struct {
	ID     string
	Secret string
	// AuthMethod is how it authenticates to the token endpoint: "none",
	// "client_secret_post" or "client_secret_basic".
	AuthMethod string
}

// Token is what a token endpoint answered.
type Token struct {
	Access  string
	Refresh string
	Expires time.Time // zero when the server didn't say
	Scope   string
}

// ErrNoAuth is a server that answered without asking for a sign-in: it needs
// no connector auth at all.
var ErrNoAuth = errors.New("the server answered without asking for a sign-in: add the connector with --auth none")

// ErrRegistration is an authorization server that won't register AgentBox as
// a client — no registration endpoint, or one that refuses — which is what the
// secret fallback is for.
var ErrRegistration = errors.New("the server won't register AgentBox as an OAuth client")

// OAuth makes the requests. HTTP is the client they go through.
type OAuth struct {
	HTTP *http.Client
	// ClientName is what the user sees on the server's consent page.
	ClientName string
	// Insecure allows plain http to hosts other than loopback. Only tests set it.
	Insecure bool
}

func (o OAuth) http() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// maxMetadata bounds what is read from any OAuth or metadata response.
const maxMetadata = 1 << 20

// CheckURL refuses anything but https, and plain http to this machine: a token
// goes wherever these URLs say, so none of them may be sniffable.
func CheckURL(raw string) error { return OAuth{}.checkURL(raw) }

func (o OAuth) checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q isn't a URL: %w", raw, err)
	}
	switch {
	case u.Host == "":
		return fmt.Errorf("%q has no host: use a full URL, like https://mcp.notion.com/mcp", raw)
	case u.User != nil:
		return fmt.Errorf("%q has a user in it: put credentials in a secret instead", raw)
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && (o.Insecure || isLoopback(u.Hostname())):
		return nil
	}
	return fmt.Errorf("%q isn't https: a connector's tokens only travel encrypted", raw)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Discover finds how a server wants to be signed in to. It asks the server
// first, unauthenticated, and follows its 401; a server that doesn't point to
// its metadata is looked up at the well-known paths; one with no metadata at
// all is taken to be its own authorization server, as the 2025-03-26 spec had
// it.
func (o OAuth) Discover(ctx context.Context, server string) (Discovery, error) {
	if err := o.checkURL(server); err != nil {
		return Discovery{}, err
	}
	metadataURL, scope, err := o.challenge(ctx, server)
	if err != nil {
		return Discovery{}, err
	}
	var candidates []string
	if metadataURL != "" {
		candidates = []string{metadataURL}
	} else {
		candidates = wellKnown(server, "oauth-protected-resource")
	}
	var prm ResourceMetadata
	found := false
	for _, u := range candidates {
		if err := o.checkURL(u); err != nil {
			return Discovery{}, fmt.Errorf("the server's resource metadata: %w", err)
		}
		ok, err := o.getJSON(ctx, u, &prm)
		if err != nil {
			return Discovery{}, fmt.Errorf("the server's resource metadata at %s: %w", u, err)
		}
		if ok {
			found = true
			break
		}
	}
	d := Discovery{Resource: canonical(server), Scope: scope}
	var issuer string
	if found {
		if err := sameResource(server, prm.Resource); err != nil {
			return Discovery{}, err
		}
		if prm.Resource != "" {
			d.Resource = prm.Resource
		}
		if len(prm.AuthorizationServers) == 0 {
			return Discovery{}, fmt.Errorf("%s's metadata names no authorization server", server)
		}
		issuer = prm.AuthorizationServers[0]
		if d.Scope == "" {
			d.Scope = strings.Join(prm.ScopesSupported, " ")
		}
	} else {
		u, _ := url.Parse(server)
		issuer = u.Scheme + "://" + u.Host
	}
	if err := o.checkURL(issuer); err != nil {
		return Discovery{}, fmt.Errorf("the authorization server: %w", err)
	}
	meta, err := o.serverMetadata(ctx, issuer, !found)
	if err != nil {
		return Discovery{}, err
	}
	meta.Issuer = cmp.Or(meta.Issuer, issuer)
	if len(meta.CodeChallengeMethodsSupported) > 0 && !slices.Contains(meta.CodeChallengeMethodsSupported, "S256") {
		return Discovery{}, fmt.Errorf("%s doesn't support PKCE with S256, which the MCP spec requires", issuer)
	}
	for _, u := range []string{meta.AuthorizationEndpoint, meta.TokenEndpoint} {
		if err := o.checkURL(u); err != nil {
			return Discovery{}, fmt.Errorf("the authorization server's endpoints: %w", err)
		}
	}
	d.Server = meta
	return d, nil
}

// initialize is the request that finds out whether a server wants a sign-in.
const initialize = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"agentbox","version":"0"}}}`

// challenge sends the server an unauthenticated initialize and reads its 401:
// where its resource metadata is, and the scope it wants.
func (o OAuth) challenge(ctx context.Context, server string) (metadataURL, scope string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server, strings.NewReader(initialize))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := o.http().Do(req)
	if err != nil {
		return "", "", fmt.Errorf("couldn't reach %s: %w", server, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxMetadata))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		params := authParams(resp.Header.Values("WWW-Authenticate"))
		return params["resource_metadata"], params["scope"], nil
	case resp.StatusCode < 300:
		return "", "", ErrNoAuth
	}
	// Anything else — a 405 from a server that only takes GET, a 404 — says
	// nothing either way: the well-known paths still might.
	return "", "", nil
}

var authParam = regexp.MustCompile(`([A-Za-z_]+)="([^"]*)"`)

// authParams reads the quoted parameters of a Bearer challenge.
func authParams(headers []string) map[string]string {
	out := map[string]string{}
	for _, h := range headers {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(h)), "bearer") {
			continue
		}
		for _, m := range authParam.FindAllStringSubmatch(h, -1) {
			out[m[1]] = m[2]
		}
	}
	return out
}

// wellKnown is where a URL's metadata may be: with the URL's path after the
// well-known name first (RFC 8414 and 9728), then at the root.
func wellKnown(raw, name string) []string {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	root := u.Scheme + "://" + u.Host + "/.well-known/" + name
	if path := strings.TrimRight(u.EscapedPath(), "/"); path != "" {
		return []string{root + path, root}
	}
	return []string{root}
}

// serverMetadata reads an authorization server's metadata: RFC 8414 first,
// then OpenID Connect discovery. legacy allows a server with neither, with
// the default endpoints the 2025-03-26 spec gave one.
func (o OAuth) serverMetadata(ctx context.Context, issuer string, legacy bool) (ServerMetadata, error) {
	u, err := url.Parse(issuer)
	if err != nil {
		return ServerMetadata{}, err
	}
	root := u.Scheme + "://" + u.Host
	path := strings.TrimRight(u.EscapedPath(), "/")
	candidates := []string{root + "/.well-known/oauth-authorization-server" + path, root + "/.well-known/openid-configuration" + path}
	if path != "" {
		candidates = append(candidates, root+path+"/.well-known/openid-configuration")
	}
	var meta ServerMetadata
	for _, c := range candidates {
		ok, err := o.getJSON(ctx, c, &meta)
		if err != nil {
			return ServerMetadata{}, fmt.Errorf("the authorization server's metadata at %s: %w", c, err)
		}
		if ok {
			if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
				return ServerMetadata{}, fmt.Errorf("%s's metadata has no authorization or token endpoint", issuer)
			}
			return meta, nil
		}
	}
	if !legacy {
		return ServerMetadata{}, fmt.Errorf("%s publishes no authorization server metadata", issuer)
	}
	return ServerMetadata{
		Issuer:                root,
		AuthorizationEndpoint: root + "/authorize",
		TokenEndpoint:         root + "/token",
		RegistrationEndpoint:  root + "/register",
	}, nil
}

// getJSON reads a metadata document. A 404 (or other 4xx) is "not here",
// which is not an error: the caller tries the next place.
func (o OAuth) getJSON(ctx context.Context, u string, into any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	resp, err := o.http().Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadata))
	if err != nil {
		return false, err
	}
	switch {
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return false, nil
	case resp.StatusCode != http.StatusOK:
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return false, fmt.Errorf("not JSON metadata: %w", err)
	}
	return true, nil
}

// canonical is a server URL as RFC 8707 wants a resource: no fragment, and a
// lowercase scheme and host.
func canonical(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment, u.RawFragment = "", ""
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	return u.String()
}

// sameResource checks that a server's metadata is about that server: the same
// origin, and a path the server's is under. Anything else would have the user
// sign in for one server and hand the token to another.
func sameResource(server, resource string) error {
	if resource == "" {
		return nil
	}
	s, err1 := url.Parse(canonical(server))
	r, err2 := url.Parse(canonical(resource))
	if err1 != nil || err2 != nil {
		return fmt.Errorf("the server's metadata names the resource %q, which isn't a URL", resource)
	}
	sp, rp := strings.TrimRight(s.Path, "/"), strings.TrimRight(r.Path, "/")
	if s.Scheme != r.Scheme || s.Host != r.Host || (sp != rp && !strings.HasPrefix(sp, rp+"/")) {
		return fmt.Errorf("the metadata at %s is for %s, not for it", server, resource)
	}
	return nil
}

// Register registers AgentBox with an authorization server, for one redirect
// URI. It registers as a public client when the server allows one — PKCE is
// what protects the code — and takes a client secret when it doesn't.
func (o OAuth) Register(ctx context.Context, meta ServerMetadata, redirectURI, scope string) (Client, error) {
	if meta.RegistrationEndpoint == "" {
		return Client{}, fmt.Errorf("%w: %s has no registration endpoint", ErrRegistration, meta.Issuer)
	}
	if err := o.checkURL(meta.RegistrationEndpoint); err != nil {
		return Client{}, err
	}
	method := tokenAuthMethod(meta.TokenEndpointAuthMethodsSupported)
	body := map[string]any{
		"client_name":                cmp.Or(o.ClientName, "AgentBox"),
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": method,
	}
	if scope != "" {
		body["scope"] = scope
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Client{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RegistrationEndpoint, bytes.NewReader(raw))
	if err != nil {
		return Client{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := o.http().Do(req)
	if err != nil {
		return Client{}, fmt.Errorf("registering with %s: %w", meta.Issuer, err)
	}
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadata))
	if err != nil {
		return Client{}, err
	}
	if resp.StatusCode >= 300 {
		return Client{}, fmt.Errorf("%w: %s answered HTTP %d to registering (%s)", ErrRegistration, meta.Issuer, resp.StatusCode, oauthError(answer))
	}
	var reg struct {
		ClientID                string `json:"client_id"`
		ClientSecret            string `json:"client_secret"`
		TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
	}
	if err := json.Unmarshal(answer, &reg); err != nil || reg.ClientID == "" {
		return Client{}, fmt.Errorf("%s's registration answer has no client_id", meta.Issuer)
	}
	c := Client{ID: reg.ClientID, Secret: reg.ClientSecret, AuthMethod: cmp.Or(reg.TokenEndpointAuthMethod, method)}
	if c.AuthMethod != "none" && c.Secret == "" {
		c.AuthMethod = "none"
	}
	return c, nil
}

// tokenAuthMethod picks how to authenticate to the token endpoint: as a public
// client if the server allows it (and when it doesn't say, since RFC 8414's
// default is client_secret_basic but RFC 7591's registration default is too,
// and a public client is what MCP expects), or with a secret in the body, or
// in a basic header.
func tokenAuthMethod(supported []string) string {
	switch {
	case len(supported) == 0 || slices.Contains(supported, "none"):
		return "none"
	case slices.Contains(supported, "client_secret_post"):
		return "client_secret_post"
	}
	return "client_secret_basic"
}

// PKCE is a code verifier and its S256 challenge.
func PKCE() (verifier, challenge string) {
	verifier = randomString(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// AuthorizationURL is the page the user signs in at.
func AuthorizationURL(d Discovery, c Client, redirectURI, state, challenge string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.ID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {d.Resource},
	}
	if d.Scope != "" {
		q.Set("scope", d.Scope)
	}
	sep := "?"
	if strings.Contains(d.Server.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return d.Server.AuthorizationEndpoint + sep + q.Encode()
}

// Exchange trades an authorization code for tokens.
func (o OAuth) Exchange(ctx context.Context, tokenEndpoint string, c Client, code, verifier, redirectURI, resource string) (Token, error) {
	return o.token(ctx, tokenEndpoint, c, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"resource":      {resource},
	})
}

// Refresh trades a refresh token for new tokens. A server that doesn't rotate
// its refresh tokens answers without one: the caller keeps the old one.
func (o OAuth) Refresh(ctx context.Context, tokenEndpoint string, c Client, refresh, resource string) (Token, error) {
	return o.token(ctx, tokenEndpoint, c, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"resource":      {resource},
	})
}

// ErrGrant is a token endpoint refusing the grant itself — an expired or
// revoked refresh token, a used code — which only signing in again fixes.
var ErrGrant = errors.New("the authorization server refused the grant")

func (o OAuth) token(ctx context.Context, endpoint string, c Client, form url.Values) (Token, error) {
	if err := o.checkURL(endpoint); err != nil {
		return Token{}, err
	}
	switch c.AuthMethod {
	case "client_secret_post":
		form.Set("client_id", c.ID)
		form.Set("client_secret", c.Secret)
	case "client_secret_basic":
	default:
		form.Set("client_id", c.ID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.AuthMethod == "client_secret_basic" {
		req.SetBasicAuth(url.QueryEscape(c.ID), url.QueryEscape(c.Secret))
	}
	resp, err := o.http().Do(req)
	if err != nil {
		return Token{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadata))
	if err != nil {
		return Token{}, err
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
			return Token{}, fmt.Errorf("%w: %s", ErrGrant, oauthError(body))
		}
		return Token{}, fmt.Errorf("the token endpoint answered HTTP %d: %s", resp.StatusCode, oauthError(body))
	}
	var t struct {
		AccessToken  string          `json:"access_token"`
		TokenType    string          `json:"token_type"`
		RefreshToken string          `json:"refresh_token"`
		ExpiresIn    json.RawMessage `json:"expires_in"`
		Scope        string          `json:"scope"`
	}
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" {
		return Token{}, errors.New("the token endpoint's answer has no access_token")
	}
	if t.TokenType != "" && !strings.EqualFold(t.TokenType, "bearer") {
		return Token{}, fmt.Errorf("the token endpoint gave a %q token, and MCP servers take bearer tokens", t.TokenType)
	}
	out := Token{Access: t.AccessToken, Refresh: t.RefreshToken, Scope: t.Scope}
	// expires_in is a number, but some servers send it as a string.
	var seconds float64
	if json.Unmarshal(t.ExpiresIn, &seconds) != nil {
		var s string
		if json.Unmarshal(t.ExpiresIn, &s) == nil {
			_, _ = fmt.Sscan(s, &seconds)
		}
	}
	if seconds > 0 {
		out.Expires = time.Now().Add(time.Duration(seconds) * time.Second)
	}
	return out, nil
}

// oauthError is an OAuth error answer in a line: error and description, or
// the start of whatever came back.
func oauthError(body []byte) string {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		if e.Description != "" {
			return e.Error + ": " + e.Description
		}
		return e.Error
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	if text == "" {
		return "no details"
	}
	return text
}
