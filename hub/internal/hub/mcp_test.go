package hub

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"agentbox/hubapi"
)

// form posts a form to the hub, as a browser on the hub's own page would
// (with origin) or as an app's server would (without), following nothing.
func (h *testHub) form(path string, values url.Values, origin bool) (*http.Response, string) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.http.URL+path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin {
		req.Header.Set("Origin", h.http.URL)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func pkce(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type oauthTokens struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Type    string `json:"token_type"`
	Expires int    `json:"expires_in"`
	Error   string `json:"error"`
}

func (h *testHub) token(values url.Values) (oauthTokens, int) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.http.URL+hubapi.PathOAuthToken, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return decode[oauthTokens](h.t, resp, resp.StatusCode), resp.StatusCode
}

// mcpStatus calls the MCP endpoint with a token and answers the status and
// what the environment saw.
func (h *testHub) mcpStatus(token string) (int, map[string]string, http.Header) {
	h.t.Helper()
	resp := h.do("POST", hubapi.PathMCP, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	return resp.StatusCode, decode[map[string]string](h.t, resp, resp.StatusCode), resp.Header
}

// TestAnAppOverMCP walks an app through what Claude or ChatGPT does with a
// remote MCP server: discovery, registration, the authorization page with a
// pairing's code, the token exchange with PKCE, calls, a refresh — and then
// the pairing's end.
func TestAnAppOverMCP(t *testing.T) {
	h := newTestHub(t)
	session := h.signIn("owner@example.com")
	env := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", session, hubapi.HubCreateEnvironmentRequest{Name: "vps"}), 201)
	other := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", session, hubapi.HubCreateEnvironmentRequest{Name: "laptop"}), 201)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.connectEnvironment(ctx, env.Token)

	// Pairings are the environment's: a device's session makes none.
	req := hubapi.HubMCPPairingRequest{Label: "Claude", Access: hubapi.MCPAccessChat, Projects: []string{"shop"}}
	if resp := h.do("POST", hubapi.PathMCPPairings, session, req); resp.StatusCode != 401 {
		t.Errorf("a device made a pairing: %d", resp.StatusCode)
	}
	for _, bad := range []hubapi.HubMCPPairingRequest{{Label: "Claude", Access: "root"}, {Access: hubapi.MCPAccessRead}} {
		if resp := h.do("POST", hubapi.PathMCPPairings, env.Token, bad); resp.StatusCode != 400 {
			t.Errorf("made the pairing %+v: %d", bad, resp.StatusCode)
		}
	}
	pairing := decode[hubapi.HubMCPPairing](t, h.do("POST", hubapi.PathMCPPairings, env.Token, req), 201)
	if pairing.Code == "" || pairing.Token != "" || pairing.URL != h.http.URL+hubapi.PathMCP || !strings.HasPrefix(pairing.ID, "mcp_") {
		t.Fatalf("the pairing = %+v", pairing)
	}

	// Discovery: no token is answered with where to find the metadata.
	status, _, header := h.mcpStatus("")
	if challenge := header.Get("WWW-Authenticate"); status != 401 || !strings.Contains(challenge, `resource_metadata="`+h.http.URL+hubapi.PathOAuthResource+hubapi.PathMCP+`"`) {
		t.Errorf("no token: %d, %q", status, challenge)
	}
	resource := decode[map[string]any](t, h.do("GET", hubapi.PathOAuthResource+hubapi.PathMCP, "", nil), 200)
	if resource["resource"] != h.http.URL+hubapi.PathMCP {
		t.Errorf("the resource metadata = %v", resource)
	}
	meta := decode[map[string]any](t, h.do("GET", hubapi.PathOAuthServer, "", nil), 200)
	if meta["authorization_endpoint"] != h.http.URL+hubapi.PathMCPAuthorizePage || meta["token_endpoint"] != h.http.URL+hubapi.PathOAuthToken || meta["registration_endpoint"] != h.http.URL+hubapi.PathOAuthRegister {
		t.Errorf("the server metadata = %v", meta)
	}

	// Registration.
	for _, uri := range []string{"http://evil.example/cb", "javascript:alert(1)", "https://claude.example/cb#frag", ""} {
		if resp := h.do("POST", hubapi.PathOAuthRegister, "", map[string]any{"client_name": "Evil", "redirect_uris": []string{uri}}); resp.StatusCode != 400 {
			t.Errorf("registered the redirect URI %q: %d", uri, resp.StatusCode)
		}
	}
	const callback = "https://claude.example/api/mcp/auth_callback"
	client := decode[map[string]any](t, h.do("POST", hubapi.PathOAuthRegister, "", map[string]any{"client_name": "Claude", "redirect_uris": []string{callback}}), 201)
	clientID := client["client_id"].(string)

	// The authorization page.
	const verifier = "a-verifier-of-at-least-forty-three-characters-long"
	params := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {callback},
		"code_challenge": {pkce(verifier)}, "code_challenge_method": {"S256"}, "state": {"xyz"},
	}
	get := func(p url.Values) (int, string, string) {
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := c.Get(h.http.URL + hubapi.PathMCPAuthorizePage + "?" + p.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body), resp.Header.Get("Location")
	}
	if status, body, _ := get(params); status != 200 || !strings.Contains(body, "Connect Claude to AgentBox") || !strings.Contains(body, "claude.example") {
		t.Errorf("the authorization page: %d %s", status, body)
	}
	// What can't be trusted is never redirected to.
	with := func(name, value string) url.Values {
		p := url.Values{}
		for k, v := range params {
			p[k] = v
		}
		p.Set(name, value)
		return p
	}
	if status, _, location := get(with("redirect_uri", "https://evil.example/cb")); status != 400 || location != "" {
		t.Errorf("an unregistered redirect URI: %d, to %q", status, location)
	}
	if status, _, location := get(with("client_id", "app_nobody")); status != 400 || location != "" {
		t.Errorf("an unknown client: %d, to %q", status, location)
	}
	if status, _, location := get(with("code_challenge_method", "plain")); status != 303 || !strings.Contains(location, "error=invalid_request") {
		t.Errorf("without S256: %d, to %q", status, location)
	}

	post := func(step, code string, origin bool) (*http.Response, string) {
		p := with("step", step)
		p.Set("pairing_code", code)
		return h.form(hubapi.PathMCPAuthorizePage, p, origin)
	}
	if resp, _ := post("check", pairing.Code, false); resp.StatusCode != 403 {
		t.Errorf("a form posted from elsewhere: %d", resp.StatusCode)
	}
	if resp, body := post("check", "BBBB-BBBB", true); resp.StatusCode != 200 || !strings.Contains(body, "isn&#39;t right") {
		t.Errorf("a wrong code: %d %s", resp.StatusCode, body)
	}
	resp, body := post("check", strings.ToLower(pairing.Code), true)
	if resp.StatusCode != 200 || !strings.Contains(body, "Let Claude use AgentBox?") || !strings.Contains(body, "shop") || !strings.Contains(body, "Chat with the lead") {
		t.Fatalf("the right code: %d %s", resp.StatusCode, body)
	}
	if resp, _ := post("deny", pairing.Code, true); resp.StatusCode != 303 || !strings.Contains(resp.Header.Get("Location"), "error=access_denied") {
		t.Errorf("cancelling: %d, to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = post("allow", pairing.Code, true)
	back, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 303 || !strings.HasPrefix(back.String(), callback+"?") || back.Query().Get("state") != "xyz" || back.Query().Get("code") == "" {
		t.Fatalf("allowing: %d, to %q", resp.StatusCode, back)
	}
	if resp, body := post("allow", pairing.Code, true); resp.StatusCode != 200 || !strings.Contains(body, "isn&#39;t right") {
		t.Errorf("the code a second time: %d %s", resp.StatusCode, body)
	}

	// The exchange: a wrong verifier burns the code.
	exchange := url.Values{"grant_type": {"authorization_code"}, "code": {back.Query().Get("code")}, "client_id": {clientID}, "redirect_uri": {callback}}
	exchange.Set("code_verifier", "not-the-verifier")
	if tokens, status := h.token(exchange); status != 400 || tokens.Error != "invalid_grant" {
		t.Errorf("a wrong verifier: %d %+v", status, tokens)
	}
	exchange.Set("code_verifier", verifier)
	if tokens, status := h.token(exchange); status != 400 || tokens.Error != "invalid_grant" {
		t.Errorf("a code tried before: %d %+v", status, tokens)
	}
	renewed := decode[hubapi.HubMCPPairing](t, h.do("POST", hubapi.PathMCPPairings+"/"+pairing.ID+"/code", env.Token, nil), 200)
	if renewed.Code == "" || renewed.Code == pairing.Code {
		t.Fatalf("a new code = %+v", renewed)
	}
	if resp := h.do("POST", hubapi.PathMCPPairings+"/"+pairing.ID+"/code", other.Token, nil); resp.StatusCode != 404 {
		t.Errorf("another environment renewed the code: %d", resp.StatusCode)
	}
	resp, _ = post("allow", renewed.Code, true)
	back, _ = url.Parse(resp.Header.Get("Location"))
	exchange.Set("code", back.Query().Get("code"))
	tokens, status := h.token(exchange)
	if status != 200 || !strings.HasPrefix(tokens.Access, hubapi.MCPTokenPrefix) || !strings.HasPrefix(tokens.Refresh, hubapi.MCPRefreshPrefix) || tokens.Type != "Bearer" || tokens.Expires != 3600 {
		t.Fatalf("the tokens: %d %+v", status, tokens)
	}

	// Calls reach the environment, at its MCP path, named by pairing.
	status, saw, _ := h.mcpStatus(tokens.Access)
	if status != 200 || saw["pairing"] != pairing.ID || saw["authorization"] != "" || saw["method"] != "POST" {
		t.Errorf("a call: %d, the environment saw %v", status, saw)
	}
	if status, _, _ := h.mcpStatus(tokens.Refresh); status != 401 {
		t.Errorf("a refresh token as a bearer: %d", status)
	}
	if status, _, _ := h.mcpStatus(session); status != 401 {
		t.Errorf("a device's session at the MCP endpoint: %d", status)
	}
	// A device can reach the path through the API proxy, but can't say
	// which pairing it is.
	resp = h.do("POST", hubapi.EnvironmentAPI(env.Environment.ID)+hubapi.EnvironmentMCPPath, session, map[string]any{}, hubapi.HeaderMCPPairing, pairing.ID)
	if saw := decode[map[string]string](t, resp, 200); saw["pairing"] != "" {
		t.Errorf("a device named a pairing: the environment saw %v", saw)
	}
	listed := decode[[]hubapi.HubMCPPairing](t, h.do("GET", hubapi.PathMCPPairings, env.Token, nil), 200)
	if len(listed) != 1 || len(listed[0].Clients) != 1 || listed[0].Clients[0] != "Claude" || listed[0].LastUsedAt.IsZero() || listed[0].Code != "" {
		t.Errorf("the pairings = %+v", listed)
	}
	if listed := decode[[]hubapi.HubMCPPairing](t, h.do("GET", hubapi.PathMCPPairings, other.Token, nil), 200); len(listed) != 0 {
		t.Errorf("another environment's pairings = %+v", listed)
	}

	// A refresh replaces both tokens.
	refreshed, status := h.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.Refresh}, "client_id": {clientID}})
	if status != 200 || refreshed.Access == tokens.Access || refreshed.Refresh == tokens.Refresh {
		t.Fatalf("a refresh: %d %+v", status, refreshed)
	}
	if status, _, _ := h.mcpStatus(tokens.Access); status != 401 {
		t.Errorf("the access token from before the refresh: %d", status)
	}
	if _, status := h.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.Refresh}}); status != 400 {
		t.Errorf("a refresh token a second time: %d", status)
	}
	if status, _, _ := h.mcpStatus(refreshed.Access); status != 200 {
		t.Errorf("the new access token: %d", status)
	}

	// A pairing with a bearer token, for an app with no OAuth.
	static := decode[hubapi.HubMCPPairing](t, h.do("POST", hubapi.PathMCPPairings, env.Token, hubapi.HubMCPPairingRequest{Label: "Poke", Access: hubapi.MCPAccessRead, Token: true}), 201)
	if status, saw, _ := h.mcpStatus(static.Token); status != 200 || saw["pairing"] != static.ID {
		t.Errorf("a bearer token: %d %v", status, saw)
	}

	// Revoking: only by its environment, and it ends every token.
	if resp := h.do("DELETE", hubapi.PathMCPPairings+"/"+pairing.ID, other.Token, nil); resp.StatusCode != 404 {
		t.Errorf("another environment revoked it: %d", resp.StatusCode)
	}
	if resp := h.do("DELETE", hubapi.PathMCPPairings+"/"+pairing.ID, env.Token, nil); resp.StatusCode != 204 {
		t.Fatalf("revoking: %d", resp.StatusCode)
	}
	if status, _, header := h.mcpStatus(refreshed.Access); status != 401 || !strings.Contains(header.Get("WWW-Authenticate"), "invalid_token") {
		t.Errorf("a revoked pairing's token: %d", status)
	}
	if _, status := h.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshed.Refresh}}); status != 400 {
		t.Errorf("a revoked pairing's refresh token: %d", status)
	}

	// An environment that goes takes its pairings with it; one that's
	// offline says so.
	cancel()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if status, _, _ := h.mcpStatus(static.Token); status == 503 {
			break
		}
	}
	if status, _, _ := h.mcpStatus(static.Token); status != 503 {
		t.Errorf("an offline environment: %d", status)
	}
	if resp := h.do("DELETE", "/v1/environments/"+env.Environment.ID, session, nil); resp.StatusCode != 204 {
		t.Fatalf("deleting the environment: %d", resp.StatusCode)
	}
	if status, _, _ := h.mcpStatus(static.Token); status != 401 {
		t.Errorf("a deleted environment's pairing: %d", status)
	}
}

func TestMCPAuthorizePageEscapes(t *testing.T) {
	h := newTestHub(t)
	client := decode[map[string]any](t, h.do("POST", hubapi.PathOAuthRegister, "", map[string]any{"client_name": `<script>alert(1)</script>`, "redirect_uris": []string{"https://app.example/cb"}}), 201)
	resp, err := http.Get(h.http.URL + hubapi.PathMCPAuthorizePage + "?" + url.Values{
		"response_type": {"code"}, "client_id": {client["client_id"].(string)}, "redirect_uri": {"https://app.example/cb"},
		"code_challenge": {"x"}, "code_challenge_method": {"S256"}, "state": {`"><script>alert(2)</script>`},
	}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if regexp.MustCompile(`<script>alert`).Match(body) {
		t.Errorf("the page runs what the app sent: %s", body)
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("the page can be framed")
	}
}

func TestMCPStore(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx, now := context.Background(), time.Now()
		if err := s.CreateUser(ctx, User{ID: "usr_1", Email: "a@example.com", Name: "A", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateEnvironment(ctx, Environment{ID: "env_1", OwnerID: "usr_1", Name: "vps", TokenHash: "h", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		p := MCPPairing{ID: "mcp_1", EnvironmentID: "env_1", Label: "Claude", Access: hubapi.MCPAccessRead, Projects: []string{"a", "b"}, CreatedAt: now}
		if err := s.CreateMCPPairing(ctx, p); err != nil {
			t.Fatal(err)
		}
		if got, err := s.MCPPairing(ctx, "mcp_1"); err != nil || got.Label != "Claude" || len(got.Projects) != 2 {
			t.Errorf("MCPPairing = %+v, %v", got, err)
		}
		if err := s.CreateOAuthClient(ctx, OAuthClient{ID: "app_1", Name: "Claude", RedirectURIs: []string{"https://x/cb"}, CreatedAt: now.Add(-48 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateOAuthClient(ctx, OAuthClient{ID: "app_2", Name: "Unused", RedirectURIs: []string{"https://y/cb"}, CreatedAt: now.Add(-48 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		for _, tok := range []MCPToken{
			{TokenHash: "live", Kind: mcpTokenRefresh, ClientID: "app_1", ExpiresAt: now.Add(time.Hour)},
			{TokenHash: "old", Kind: mcpTokenAccess, ClientID: "app_1", ExpiresAt: now.Add(-time.Hour)},
			{TokenHash: "forever", Kind: mcpTokenStatic},
		} {
			tok.PairingID, tok.CreatedAt = "mcp_1", now
			if err := s.CreateMCPToken(ctx, tok); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.MCPToken(ctx, "old", now); err != ErrNotFound {
			t.Errorf("an expired token: %v", err)
		}
		if tok, err := s.MCPToken(ctx, "forever", now.Add(1000*time.Hour)); err != nil || tok.Kind != mcpTokenStatic {
			t.Errorf("a token that doesn't expire: %+v, %v", tok, err)
		}
		if tok, err := s.TakeMCPToken(ctx, "live", now); err != nil || tok.ClientID != "app_1" || tok.PairingID != "mcp_1" {
			t.Errorf("TakeMCPToken = %+v, %v", tok, err)
		}
		if _, err := s.TakeMCPToken(ctx, "live", now); err != ErrNotFound {
			t.Errorf("taking a token twice: %v", err)
		}
		if names, err := s.MCPPairingClients(ctx, "mcp_1"); err != nil || len(names) != 1 || names[0] != "Claude" {
			t.Errorf("MCPPairingClients = %v, %v", names, err)
		}
		if err := s.PruneOAuthClients(ctx, now.Add(-24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.CountOAuthClients(ctx); n != 1 {
			t.Errorf("%d clients after pruning, want the one with a token", n)
		}
		if err := s.DeleteMCPPairing(ctx, "env_2", "mcp_1"); err != ErrNotFound {
			t.Errorf("deleting another environment's pairing: %v", err)
		}
		if err := s.DeleteMCPPairing(ctx, "env_1", "mcp_1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.MCPToken(ctx, "forever", now); err != ErrNotFound {
			t.Errorf("a deleted pairing's token: %v", err)
		}
	})
}
