// Package connectorstest is a fake remote MCP server with its own OAuth
// authorization server, shaped like Notion's (https://mcp.notion.com/mcp, as
// checked on 29 September 2026): the MCP endpoint at /mcp answers 401 with
// resource_metadata, the protected resource metadata is at
// /.well-known/oauth-protected-resource/mcp, the authorization server is the
// same origin, registration is open to any client, public clients are allowed,
// and PKCE is S256. It checks what a real server would — PKCE, the redirect
// URI, the resource — so the tests that use it prove the flow, not just the
// plumbing. Refresh tokens rotate, as Notion's do.
package connectorstest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"time"
)

// Server is the fake. Its fields are for tests to set before use or read
// after; take Lock around reading ones that change.
type Server struct {
	*httptest.Server

	// RefuseRegistration answers every registration 403 Forbidden, as
	// Figma's registration endpoint does for clients it hasn't approved.
	RefuseRegistration bool
	// TokenTTL is the access tokens' expires_in; zero leaves it out.
	TokenTTL time.Duration

	mu        sync.Mutex
	clients   map[string]client
	codes     map[string]grant
	access    map[string]bool
	refresh   map[string]bool
	sessions  map[string]bool
	Refreshes int
	// Seen is the Authorization header of every request /mcp accepted, and
	// Methods the JSON-RPC method of each.
	Seen    []string
	Methods []string
	// Deleted counts sessions ended with DELETE.
	Deleted int
}

type client struct {
	redirects  []string
	authMethod string
	secret     string
}

type grant struct {
	client, redirect, challenge, resource string
}

// New starts the fake. Close it when done.
func New() *Server {
	s := &Server{
		clients: map[string]client{}, codes: map[string]grant{}, access: map[string]bool{},
		refresh: map[string]bool{}, sessions: map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.mcp)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.resourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.resourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.serverMetadata)
	mux.HandleFunc("POST /register", s.register)
	mux.HandleFunc("GET /authorize", s.authorize)
	mux.HandleFunc("POST /token", s.token)
	s.Server = httptest.NewServer(mux)
	return s
}

// MCP is the server's MCP endpoint, which is what a connector's URL is.
func (s *Server) MCP() string { return s.URL + "/mcp" }

func (s *Server) Lock()   { s.mu.Lock() }
func (s *Server) Unlock() { s.mu.Unlock() }

// Revoke makes every access token the server gave out invalid, as a server
// that revokes early does: the next request is refused, and has to refresh.
func (s *Server) Revoke() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.access = map[string]bool{}
}

// RevokeAll makes the refresh tokens invalid too: only signing in again helps.
func (s *Server) RevokeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.access, s.refresh = map[string]bool{}, map[string]bool{}
}

// SignIn is the user's browser: it opens the authorization URL, which this
// fake approves at once, and follows the redirect back to the client's
// callback. It answers with the callback page's status and text. It keeps
// no connection open: a sign-in's own listener is closed once it is over, and
// the next may listen on the same port.
func SignIn(authorizationURL string) (int, string, error) {
	browser := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := browser.Get(authorizationURL)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), err
}

func random() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) resourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.MCP(),
		"authorization_servers":    []string{s.URL},
		"scopes_supported":         []string{"default"},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Notion MCP (fake)",
	})
}

func (s *Server) serverMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                s.URL,
		"authorization_endpoint":                s.URL + "/authorize",
		"token_endpoint":                        s.URL + "/token",
		"registration_endpoint":                 s.URL + "/register",
		"scopes_supported":                      []string{"default"},
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
		"code_challenge_methods_supported":      []string{"plain", "S256"},
	})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if s.RefuseRegistration {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.RedirectURIs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	c := client{redirects: req.RedirectURIs, authMethod: req.AuthMethod}
	if c.authMethod == "" {
		c.authMethod = "client_secret_basic"
	}
	id := random()
	answer := map[string]any{"client_id": id, "redirect_uris": req.RedirectURIs, "token_endpoint_auth_method": c.authMethod}
	if c.authMethod != "none" {
		c.secret = random()
		answer["client_secret"] = c.secret
	}
	s.mu.Lock()
	s.clients[id] = c
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, answer)
}

// authorize approves at once, as if the user had signed in and allowed it.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	c, ok := s.clients[q.Get("client_id")]
	s.mu.Unlock()
	switch {
	case !ok:
		http.Error(w, "unknown client", http.StatusBadRequest)
		return
	case !slices.Contains(c.redirects, q.Get("redirect_uri")):
		http.Error(w, "redirect_uri isn't registered", http.StatusBadRequest)
		return
	case q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "":
		http.Error(w, "PKCE with S256 is required", http.StatusBadRequest)
		return
	case q.Get("resource") != s.MCP():
		http.Error(w, "resource must be "+s.MCP(), http.StatusBadRequest)
		return
	}
	code := random()
	s.mu.Lock()
	s.codes[code] = grant{client: q.Get("client_id"), redirect: q.Get("redirect_uri"), challenge: q.Get("code_challenge"), resource: q.Get("resource")}
	s.mu.Unlock()
	back := q.Get("redirect_uri") + "?code=" + code + "&state=" + q.Get("state") + "&iss=" + s.URL
	http.Redirect(w, r, back, http.StatusFound)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	id := r.PostForm.Get("client_id")
	secret := r.PostForm.Get("client_secret")
	if user, pass, ok := r.BasicAuth(); ok {
		id, secret = user, pass
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clients[id]
	if !ok || (c.authMethod != "none" && c.secret != secret) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	if r.PostForm.Get("resource") != s.MCP() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_target"})
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		g, ok := s.codes[r.PostForm.Get("code")]
		delete(s.codes, r.PostForm.Get("code"))
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || g.client != id || g.redirect != r.PostForm.Get("redirect_uri") ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "code, redirect_uri or code_verifier is wrong"})
			return
		}
	case "refresh_token":
		old := r.PostForm.Get("refresh_token")
		if !s.refresh[old] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "refresh token revoked"})
			return
		}
		delete(s.refresh, old) // rotated: it can't be used twice
		s.Refreshes++
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	access, refresh := "at-"+random(), "rt-"+random()
	s.access[access], s.refresh[refresh] = true, true
	answer := map[string]any{"access_token": access, "token_type": "bearer", "refresh_token": refresh, "scope": "default"}
	if s.TokenTTL > 0 {
		answer["expires_in"] = int(s.TokenTTL.Seconds())
	}
	writeJSON(w, http.StatusOK, answer)
}

// mcp is the streamable HTTP endpoint: initialize answers JSON and starts a
// session, tools/list answers as an event stream, tools/call as JSON.
func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	s.mu.Lock()
	ok := strings.HasPrefix(auth, "Bearer ") && s.access[strings.TrimPrefix(auth, "Bearer ")]
	s.mu.Unlock()
	if !ok {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="OAuth", resource_metadata="%s/.well-known/oauth-protected-resource/mcp", error="invalid_token", error_description="Missing or invalid access token"`, s.URL))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	session := r.Header.Get("Mcp-Session-Id")
	switch r.Method {
	case http.MethodGet:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.sessions, session)
		s.Deleted++
		s.mu.Unlock()
		return
	case http.MethodPost:
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "not JSON-RPC", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.Seen = append(s.Seen, auth)
	s.Methods = append(s.Methods, msg.Method)
	known := s.sessions[session]
	s.mu.Unlock()
	result := func(v any) map[string]any { return map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": v} }
	if msg.Method == "initialize" {
		id := random()
		s.mu.Lock()
		s.sessions[id] = true
		s.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", id)
		writeJSON(w, http.StatusOK, result(map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "Notion MCP (fake)", "version": "1.0.0"},
		}))
		return
	}
	switch {
	case session == "":
		http.Error(w, "Mcp-Session-Id is required", http.StatusBadRequest)
		return
	case !known:
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	case r.Header.Get("Mcp-Protocol-Version") != "2025-06-18":
		http.Error(w, "MCP-Protocol-Version is required", http.StatusBadRequest)
		return
	case len(msg.ID) == 0:
		w.WriteHeader(http.StatusAccepted) // a notification
		return
	}
	switch msg.Method {
	case "tools/list":
		tools := result(map[string]any{"tools": []map[string]any{{
			"name": "notion-search", "description": "Search the workspace",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
		}}})
		raw, _ := json.Marshal(tools)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, ": keep-alive\n\nevent: message\nid: 1\ndata: %s\n\n", raw)
	case "tools/call":
		query, _ := msg.Params.Arguments["query"].(string)
		writeJSON(w, http.StatusOK, result(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "found: " + query}},
		}))
	default:
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "no such method"}})
	}
}
