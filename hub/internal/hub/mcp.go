package hub

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"agentbox/hubapi"
)

// Remote MCP: another app — Claude, ChatGPT, Poke — reaches an environment's
// AgentBox through the hub, at hubapi.PathMCP, with a token of one of the
// environment's MCP pairings. The hub is the OAuth authorization server those
// apps expect (RFC 8414 metadata, RFC 7591 registration, the authorization
// code grant with PKCE, rotating refresh tokens), with one difference from the
// usual: nobody signs in. What the authorization page asks for is a pairing's
// code, which the environment's owner made in the AgentBox app a moment ago
// and reads from it. Whoever types the code decides, as with a device code.
//
// The hub knows which pairing a token belongs to and nothing about what it
// may do: it passes the call down the tunnel with the pairing's ID
// (hubapi.HeaderMCPPairing), and the environment, which made the pairing,
// enforces its access level and its projects. What the hub keeps of those is
// only for showing on the authorization page.

const (
	// A pairing's code works once, within this long.
	mcpCodeLifetime = 10 * time.Minute
	// An authorization code is exchanged by the app's server at once.
	oauthCodeLifetime = 2 * time.Minute
	mcpAccessLifetime = time.Hour
	// A refresh token is replaced each time it's used, so an app that keeps
	// calling stays signed in, and one that stopped is signed out after this.
	mcpRefreshLifetime = 90 * 24 * time.Hour
	mcpTouchEvery      = time.Minute

	maxMCPPairings   = 50 // per environment
	maxMCPProjects   = 200
	maxOAuthClients  = 2000
	maxRedirectURIs  = 10
	oauthClientGrace = 24 * time.Hour // an unused registration is kept this long
)

type mcpKey struct{} // the pairing's ID, on a request passed to an environment

// mcpCode is a pairing's code, waiting to be typed.
type mcpCode struct {
	pairingID string
	expires   time.Time
}

// oauthCode is an authorization code, waiting to be exchanged.
type oauthCode struct {
	pairingID   string
	clientID    string
	redirectURI string
	challenge   string
	expires     time.Time
}

var mcpAccessLevels = []string{hubapi.MCPAccessRead, hubapi.MCPAccessChat, hubapi.MCPAccessFull}

func (s *Server) mcpRoutes(mux *http.ServeMux, h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h(http.MethodGet+" "+hubapi.PathMCPPairings, s.listMCPPairings)
	h(http.MethodPost+" "+hubapi.PathMCPPairings, s.createMCPPairing)
	h(http.MethodDelete+" "+hubapi.PathMCPPairings+"/{id}", s.deleteMCPPairing)
	h(http.MethodPost+" "+hubapi.PathMCPPairings+"/{id}/code", s.renewMCPCode)

	// What follows is called by other apps' servers, and by browsers on
	// other sites (MCP's inspector, web clients). None of it reads a cookie:
	// a bearer token or a code is all that's ever trusted, so any origin
	// may call it.
	open := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		method, path, _ := strings.Cut(pattern, " ")
		h(method+" "+path, func(w http.ResponseWriter, r *http.Request) error {
			cors(w)
			return fn(w, r)
		})
		mux.HandleFunc(http.MethodOptions+" "+path, func(w http.ResponseWriter, r *http.Request) {
			cors(w)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		h(method+" "+hubapi.PathMCP, func(w http.ResponseWriter, r *http.Request) error {
			cors(w)
			return s.mcp(w, r)
		})
	}
	mux.HandleFunc(http.MethodOptions+" "+hubapi.PathMCP, func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		w.WriteHeader(http.StatusNoContent)
	})
	// Clients look for the metadata at the root and under the resource's
	// path, and some for OpenID's name of it.
	for _, suffix := range []string{"", hubapi.PathMCP} {
		open("GET "+hubapi.PathOAuthResource+suffix, s.oauthResource)
		open("GET "+hubapi.PathOAuthServer+suffix, s.oauthServer)
	}
	open("GET /.well-known/openid-configuration", s.oauthServer)
	open("POST "+hubapi.PathOAuthRegister, s.oauthRegister)
	open("POST "+hubapi.PathOAuthToken, s.oauthToken)

	mux.HandleFunc("GET "+hubapi.PathMCPAuthorizePage, s.authorizePage)
	mux.HandleFunc("POST "+hubapi.PathMCPAuthorizePage, s.authorizePage)
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Mcp-Protocol-Version, Mcp-Session-Id, Last-Event-ID")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id, WWW-Authenticate")
	w.Header().Set("Access-Control-Max-Age", "600")
}

func (s *Server) toAPIMCPPairing(r *http.Request, p MCPPairing) hubapi.HubMCPPairing {
	return hubapi.HubMCPPairing{
		ID: p.ID, Label: p.Label, Access: p.Access, Projects: p.Projects,
		URL: s.baseURL(r) + hubapi.PathMCP, CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt,
	}
}

func (s *Server) listMCPPairings(w http.ResponseWriter, r *http.Request) error {
	e, err := s.environmentByToken(r)
	if err != nil {
		return err
	}
	pairings, err := s.Store.MCPPairings(r.Context(), e.ID)
	if err != nil {
		return err
	}
	out := make([]hubapi.HubMCPPairing, 0, len(pairings))
	for _, p := range pairings {
		item := s.toAPIMCPPairing(r, p)
		if item.Clients, err = s.Store.MCPPairingClients(r.Context(), p.ID); err != nil {
			return err
		}
		out = append(out, item)
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) createMCPPairing(w http.ResponseWriter, r *http.Request) error {
	e, err := s.environmentByToken(r)
	if err != nil {
		return err
	}
	var req hubapi.HubMCPPairingRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	label := cleanLabel(req.Label, 60)
	if label == "" {
		return errors.New("name the app this pairing is for")
	}
	if !slices.Contains(mcpAccessLevels, req.Access) {
		return fmt.Errorf("%q isn't an access level: use %s", req.Access, strings.Join(mcpAccessLevels, ", "))
	}
	if len(req.Projects) > maxMCPProjects {
		return fmt.Errorf("a pairing covers at most %d projects by name: leave them out to cover all", maxMCPProjects)
	}
	projects := make([]string, 0, len(req.Projects))
	for _, p := range req.Projects {
		if p = cleanLabel(p, 100); p != "" {
			projects = append(projects, p)
		}
	}
	existing, err := s.Store.MCPPairings(r.Context(), e.ID)
	if err != nil {
		return err
	}
	if len(existing) >= maxMCPPairings {
		return httpError{http.StatusConflict, fmt.Sprintf("%s already has %d pairings with other apps: revoke one first", e.Name, maxMCPPairings)}
	}
	p := MCPPairing{ID: newID("mcp_"), EnvironmentID: e.ID, Label: label, Access: req.Access, Projects: projects, CreatedAt: time.Now()}
	if err := s.Store.CreateMCPPairing(r.Context(), p); err != nil {
		return err
	}
	out := s.toAPIMCPPairing(r, p)
	if req.Token {
		out.Token = newToken(hubapi.MCPTokenPrefix)
		if err := s.Store.CreateMCPToken(r.Context(), MCPToken{TokenHash: hashToken(out.Token), PairingID: p.ID, Kind: mcpTokenStatic, CreatedAt: p.CreatedAt}); err != nil {
			return err
		}
	}
	out.Code, out.CodeExpiresAt = s.newMCPCode(p.ID)
	s.logf("environment %s paired the app %q over MCP (%s)", e.Name, p.Label, p.Access)
	return writeJSON(w, http.StatusCreated, out)
}

// newMCPCode makes a pairing's code, replacing the one it had.
func (s *Server) newMCPCode(pairingID string) (string, time.Time) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mcpCodes == nil {
		s.mcpCodes = map[string]mcpCode{}
	}
	for code, c := range s.mcpCodes {
		if c.pairingID == pairingID || !now.Before(c.expires) {
			delete(s.mcpCodes, code)
		}
	}
	code := newCode()
	for _, taken := s.mcpCodes[code]; taken; _, taken = s.mcpCodes[code] {
		code = newCode()
	}
	expires := now.Add(mcpCodeLifetime)
	s.mcpCodes[code] = mcpCode{pairingID: pairingID, expires: expires}
	return code, expires
}

// ownMCPPairing is the pairing in the path, of the environment asking.
func (s *Server) ownMCPPairing(r *http.Request) (Environment, MCPPairing, error) {
	e, err := s.environmentByToken(r)
	if err != nil {
		return Environment{}, MCPPairing{}, err
	}
	p, err := s.Store.MCPPairing(r.Context(), r.PathValue("id"))
	if err != nil || p.EnvironmentID != e.ID {
		return Environment{}, MCPPairing{}, httpError{http.StatusNotFound, "no such pairing"}
	}
	return e, p, nil
}

func (s *Server) renewMCPCode(w http.ResponseWriter, r *http.Request) error {
	_, p, err := s.ownMCPPairing(r)
	if err != nil {
		return err
	}
	out := s.toAPIMCPPairing(r, p)
	out.Code, out.CodeExpiresAt = s.newMCPCode(p.ID)
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteMCPPairing(w http.ResponseWriter, r *http.Request) error {
	e, p, err := s.ownMCPPairing(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteMCPPairing(r.Context(), e.ID, p.ID); err != nil {
		return err
	}
	s.mu.Lock()
	for code, c := range s.mcpCodes {
		if c.pairingID == p.ID {
			delete(s.mcpCodes, code)
		}
	}
	for hash, c := range s.oauthCodes {
		if c.pairingID == p.ID {
			delete(s.oauthCodes, hash)
		}
	}
	s.mu.Unlock()
	s.logf("environment %s revoked the app %q", e.Name, p.Label)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// mcp is the endpoint other apps call: it finds the pairing the token belongs
// to and passes the request to its environment.
func (s *Server) mcp(w http.ResponseWriter, r *http.Request) error {
	challenge := `Bearer resource_metadata="` + s.baseURL(r) + hubapi.PathOAuthResource + hubapi.PathMCP + `"`
	token := bearer(r)
	if token == "" {
		w.Header().Set("WWW-Authenticate", challenge)
		return httpError{http.StatusUnauthorized, "pair this app with AgentBox first: in the AgentBox app, Settings, Use AgentBox in another app"}
	}
	invalid := func() error {
		w.Header().Set("WWW-Authenticate", challenge+`, error="invalid_token"`)
		return httpError{http.StatusUnauthorized, "this app's access to AgentBox ended: it expired or was revoked"}
	}
	if !strings.HasPrefix(token, hubapi.MCPTokenPrefix) {
		return invalid()
	}
	t, err := s.Store.MCPToken(r.Context(), hashToken(token), time.Now())
	if errors.Is(err, ErrNotFound) || (err == nil && t.Kind == mcpTokenRefresh) {
		return invalid()
	} else if err != nil {
		return err
	}
	p, err := s.Store.MCPPairing(r.Context(), t.PairingID)
	if errors.Is(err, ErrNotFound) {
		return invalid()
	} else if err != nil {
		return err
	}
	if now := time.Now(); now.Sub(p.LastUsedAt) > mcpTouchEvery {
		if err := s.Store.TouchMCPPairing(r.Context(), p.ID, now); err != nil {
			s.logf("recording that an app called: %v", err)
		}
	}
	s.mu.Lock()
	tun := s.tunnels[p.EnvironmentID]
	s.mu.Unlock()
	if tun == nil {
		return httpError{http.StatusServiceUnavailable, "the machine this app is paired with is offline: AgentBox isn't running on it, or it lost its connection"}
	}
	tun.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpKey{}, p.ID)))
	return nil
}

func (s *Server) oauthResource(w http.ResponseWriter, r *http.Request) error {
	base := s.baseURL(r)
	return writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 base + hubapi.PathMCP,
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{"mcp"},
		"resource_name":            "AgentBox",
	})
}

func (s *Server) oauthServer(w http.ResponseWriter, r *http.Request) error {
	base := s.baseURL(r)
	return writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + hubapi.PathMCPAuthorizePage,
		"token_endpoint":                        base + hubapi.PathOAuthToken,
		"registration_endpoint":                 base + hubapi.PathOAuthRegister,
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mcp"},
	})
}

// oauthError is an OAuth error answer (RFC 6749, section 5.2).
func oauthError(w http.ResponseWriter, status int, code, description string) error {
	w.Header().Set("Cache-Control", "no-store")
	return writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

// validRedirect reports whether an app may be sent back to uri: an https
// address, http on this machine only (a desktop app's own listener), or an
// app's own scheme. Nothing a browser would run or read from disk.
func validRedirect(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil || len(uri) > 2000 || u.Fragment != "" || u.Scheme == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return u.Host != ""
	case "http":
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	case "javascript", "data", "vbscript", "file", "blob", "about", "ws", "wss", "ftp":
		return false
	}
	return true
}

// oauthRegister is dynamic client registration: an app says what it's called
// and where it may be sent back to. Anyone can call it, so registrations are
// capped, and the ones nobody signed in with are dropped after a day.
func (s *Server) oauthRegister(w http.ResponseWriter, r *http.Request) error {
	if s.tooManyFailures("register:"+clientIP(r), true) {
		return oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable", "too many registrations from this address: try again in a few minutes")
	}
	var req struct {
		Name         string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := readJSON(r, &req); err != nil {
		return oauthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > maxRedirectURIs {
		return oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", fmt.Sprintf("register between 1 and %d redirect_uris", maxRedirectURIs))
	}
	for _, uri := range req.RedirectURIs {
		if !validRedirect(uri) {
			return oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", fmt.Sprintf("%q can't be a redirect URI: use https, http on localhost, or the app's own scheme", uri))
		}
	}
	ctx, now := r.Context(), time.Now()
	if n, err := s.Store.CountOAuthClients(ctx); err != nil {
		return err
	} else if n >= maxOAuthClients {
		if err := s.Store.DeleteExpiredMCPTokens(ctx, now); err != nil {
			return err
		}
		if err := s.Store.PruneOAuthClients(ctx, now.Add(-oauthClientGrace)); err != nil {
			return err
		}
		if n, err = s.Store.CountOAuthClients(ctx); err != nil {
			return err
		} else if n >= maxOAuthClients {
			return oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "this hub has too many registered apps")
		}
	}
	c := OAuthClient{ID: newID("app_"), Name: cmpOr(cleanLabel(req.Name, 60), "An app"), RedirectURIs: req.RedirectURIs, CreatedAt: now}
	if err := s.Store.CreateOAuthClient(ctx, c); err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	return writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  c.ID,
		"client_id_issued_at":        now.Unix(),
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// newOAuthCode makes the authorization code an app exchanges for its tokens.
func (s *Server) newOAuthCode(c oauthCode) string {
	code := newToken("abx_c_")
	now := time.Now()
	c.expires = now.Add(oauthCodeLifetime)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.oauthCodes == nil {
		s.oauthCodes = map[string]oauthCode{}
	}
	for hash, old := range s.oauthCodes {
		if !now.Before(old.expires) {
			delete(s.oauthCodes, hash)
		}
	}
	s.oauthCodes[hashToken(code)] = c
	return code
}

// oauthToken is the token endpoint: an authorization code, with the PKCE
// verifier that proves the caller is who asked for it, or a refresh token,
// for an access token and a new refresh token.
func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := r.ParseForm(); err != nil {
		return oauthError(w, http.StatusBadRequest, "invalid_request", "send the request as a form")
	}
	form := r.PostForm
	var pairingID, clientID string
	switch form.Get("grant_type") {
	case "authorization_code":
		hash := hashToken(form.Get("code"))
		s.mu.Lock()
		c, ok := s.oauthCodes[hash]
		delete(s.oauthCodes, hash) // one try, right or wrong
		s.mu.Unlock()
		if !ok || !time.Now().Before(c.expires) {
			return oauthError(w, http.StatusBadRequest, "invalid_grant", "the authorization code is wrong, used or expired")
		}
		if form.Get("client_id") != c.clientID || form.Get("redirect_uri") != c.redirectURI {
			return oauthError(w, http.StatusBadRequest, "invalid_grant", "the code was issued to another client or redirect URI")
		}
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(c.challenge)) != 1 {
			return oauthError(w, http.StatusBadRequest, "invalid_grant", "the code verifier doesn't match the challenge")
		}
		pairingID, clientID = c.pairingID, c.clientID
	case "refresh_token":
		token := form.Get("refresh_token")
		if !strings.HasPrefix(token, hubapi.MCPRefreshPrefix) {
			return oauthError(w, http.StatusBadRequest, "invalid_grant", "that isn't a refresh token")
		}
		t, err := s.Store.TakeMCPToken(r.Context(), hashToken(token), time.Now())
		if errors.Is(err, ErrNotFound) || (err == nil && t.Kind != mcpTokenRefresh) {
			return oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token is used, expired or revoked")
		} else if err != nil {
			return err
		}
		if id := form.Get("client_id"); id != "" && id != t.ClientID {
			return oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token was issued to another client")
		}
		pairingID, clientID = t.PairingID, t.ClientID
	default:
		return oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
	}
	// The pairing may have been revoked since the code was made.
	if _, err := s.Store.MCPPairing(r.Context(), pairingID); err != nil {
		return oauthError(w, http.StatusBadRequest, "invalid_grant", "the pairing was revoked")
	}
	// One pair per client: the tokens it held before end here.
	if err := s.Store.DeleteMCPClientTokens(r.Context(), pairingID, clientID); err != nil {
		return err
	}
	now := time.Now()
	access, refresh := newToken(hubapi.MCPTokenPrefix), newToken(hubapi.MCPRefreshPrefix)
	for _, t := range []MCPToken{
		{TokenHash: hashToken(access), Kind: mcpTokenAccess, ExpiresAt: now.Add(mcpAccessLifetime)},
		{TokenHash: hashToken(refresh), Kind: mcpTokenRefresh, ExpiresAt: now.Add(mcpRefreshLifetime)},
	} {
		t.PairingID, t.ClientID, t.CreatedAt = pairingID, clientID, now
		if err := s.Store.CreateMCPToken(r.Context(), t); err != nil {
			return err
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	return writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": int(mcpAccessLifetime.Seconds()),
		"refresh_token": refresh, "scope": "mcp",
	})
}
