package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"agentbox/hubapi"
)

const (
	cookieName = hubapi.SessionCookie
	// A device's token lasts until it's revoked, or goes this long unused.
	sessionLifetime = 90 * 24 * time.Hour
	// How often a device's use is written down, at most.
	touchEvery = time.Hour
)

// Server is the hub's HTTP server.
type Server struct {
	Store Store
	Log   io.Writer
	// Web is the web app, served at /; nil serves none.
	Web fs.FS
	// Version is the hub's, which it answers on /v1/hub.
	Version string
	// URL is the hub's address as clients reach it, like
	// https://hub.example.com, for the links it makes; empty takes it from
	// each request.
	URL string
	// Cloudflare makes environments' stable addresses for phones
	// (tunnels.go); nil gives out none. TunnelIdle is how long an
	// environment's tunnel outlives its last visit: DefaultTunnelIdle when 0.
	Cloudflare *Cloudflare
	TunnelIdle time.Duration
	// Tailscale puts environments in the hub's tailnet instead (tailscale.go,
	// tunnels.go); when it's set, Cloudflare is only used to remove the
	// tunnels made before.
	Tailscale *Tailscale

	tunnelMu sync.Mutex // making and removing tunnels
	claimMu  sync.Mutex // taking setup links
	mu       sync.Mutex
	tunnels  map[string]*tunnel // connected environments, by ID
	failures map[string][]time.Time
	pairings map[string]*pairing // waiting, approved or denied, by code
}

type tunnel struct {
	session   *yamux.Session
	proxy     *httputil.ReverseProxy
	connected time.Time
}

type httpError struct {
	status int
	msg    string
}

func (e httpError) Error() string { return e.msg }

var (
	errSignIn  = httpError{http.StatusUnauthorized, "sign in to the hub first"}
	errOrigin  = httpError{http.StatusForbidden, "this request came from another site"}
	namePatten = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		_, _ = fmt.Fprintf(s.Log, time.Now().Format(time.DateTime)+" "+format+"\n", args...)
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				status := http.StatusBadRequest
				var he httpError
				switch {
				case errors.As(err, &he):
					status = he.status
				case errors.Is(err, ErrNotFound):
					status = http.StatusNotFound
				case errors.Is(err, ErrExists):
					status = http.StatusConflict
				}
				if status >= 500 || status == http.StatusBadRequest {
					s.logf("%s %s: %v", r.Method, r.URL.Path, err)
				}
				_ = writeJSON(w, status, hubapi.Error{Error: err.Error()})
			}
		})
	}
	h(http.MethodGet+" "+hubapi.PathHealth, func(w http.ResponseWriter, r *http.Request) error {
		return writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	h(http.MethodGet+" "+hubapi.PathInfo, s.info)
	h(http.MethodPost+" "+hubapi.PathSetup, s.setup)
	h(http.MethodPost+" "+hubapi.PathPair, s.startPairing)
	h(http.MethodPost+" "+hubapi.PathPairToken, s.pairToken)
	h(http.MethodGet+" "+hubapi.PathPairings+"/{code}", s.getPairing)
	h(http.MethodPost+" "+hubapi.PathPairings+"/{code}/approve", s.decidePairing(true))
	h(http.MethodPost+" "+hubapi.PathPairings+"/{code}/deny", s.decidePairing(false))
	h(http.MethodGet+" "+hubapi.PathDevices, s.listDevices)
	h(http.MethodDelete+" "+hubapi.PathDevices+"/{id}", s.revokeDevice)
	h(http.MethodPost+" "+hubapi.PathLogin, s.login)
	h(http.MethodPost+" "+hubapi.PathLogout, s.logout)
	h(http.MethodGet+" "+hubapi.PathMe, s.me)
	h(http.MethodGet+" "+hubapi.PathEnvironments, s.listEnvironments)
	h(http.MethodPost+" "+hubapi.PathEnvironments, s.createEnvironment)
	h(http.MethodDelete+" "+hubapi.PathEnvironments+"/{id}", s.deleteEnvironment)
	h(http.MethodGet+" "+hubapi.PathConnect, s.connect)
	h(http.MethodPost+" "+hubapi.PathEnvironmentTunnel, s.environmentTunnel)
	h(http.MethodPost+" "+hubapi.PathEnvironmentAddress, s.environmentAddress)
	h(hubapi.EnvironmentAPI("{id}")+"/{path...}", s.proxy)
	s.pages(mux)
	if s.Web != nil {
		mux.Handle("/", webApp(s.Web))
	} else {
		mux.Handle("GET /{$}", http.RedirectHandler("/hub/", http.StatusFound))
	}
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// webApp serves the web app's files, and its index.html for any other path.
func webApp(files fs.FS) http.Handler {
	server := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(files, path); err == nil {
				server.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "this hub has no web app", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// sameOrigin reports whether a browser request came from the hub's own pages.
// Requests without an Origin (the CLI, the desktop app, same-origin GETs) pass.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if secure(r) {
		scheme = "https"
	}
	return origin == scheme+"://"+r.Host
}

// authenticate returns the signed-in user, from a bearer token (the CLI and the
// desktop app) or the session cookie (the web app).
func (s *Server) authenticate(r *http.Request) (User, Session, error) {
	token, fromCookie := bearer(r), false
	if token == "" {
		if c, err := r.Cookie(cookieName); err == nil {
			token, fromCookie = c.Value, true
		}
	}
	if !strings.HasPrefix(token, sessionPrefix) {
		return User{}, Session{}, errSignIn
	}
	if fromCookie && !sameOrigin(r) {
		return User{}, Session{}, errOrigin
	}
	sess, err := s.Store.Session(r.Context(), hashToken(token), time.Now())
	if errors.Is(err, ErrNotFound) {
		return User{}, Session{}, httpError{http.StatusUnauthorized, "your session on the hub ended: sign in again"}
	}
	if err != nil {
		return User{}, Session{}, err
	}
	u, err := s.Store.UserByID(r.Context(), sess.UserID)
	if err != nil {
		return User{}, Session{}, errSignIn
	}
	if now := time.Now(); now.Sub(sess.LastSeenAt) > touchEvery {
		if err := s.Store.TouchSession(r.Context(), sess.TokenHash, now, now.Add(sessionLifetime)); err != nil {
			s.logf("recording that a device was seen: %v", err)
		}
	}
	return u, sess, nil
}

func bearer(r *http.Request) string {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return strings.TrimSpace(token)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// tooManyFailures reports whether an address failed to sign in 10 times in the
// last 10 minutes; record adds a failure.
func (s *Server) tooManyFailures(ip string, record bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures == nil {
		s.failures = map[string][]time.Time{}
	}
	cutoff := time.Now().Add(-10 * time.Minute)
	recent := s.failures[ip][:0]
	for _, t := range s.failures[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if record {
		recent = append(recent, time.Now())
	}
	s.failures[ip] = recent
	return len(recent) >= 10
}

func toAPIUser(u User) hubapi.HubUser {
	return hubapi.HubUser{ID: u.ID, Email: u.Email, Name: u.Name}
}

// startSession signs a user in, answering with the token and setting the cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u User, label string) error {
	sess, token, err := s.newSession(r.Context(), u, label)
	if err != nil {
		return err
	}
	setCookie(w, r, token, sess.ExpiresAt)
	return writeJSON(w, http.StatusOK, hubapi.HubSession{Token: token, User: toAPIUser(u), ExpiresAt: sess.ExpiresAt})
}

func setCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode,
	})
}

// ResetPassword sets an account's password, and signs it out everywhere.
func ResetPassword(ctx context.Context, store Store, email, password string) (User, error) {
	u, err := store.UserByEmail(ctx, strings.TrimSpace(email))
	if errors.Is(err, ErrNotFound) {
		return User{}, fmt.Errorf("no account has the email %s", email)
	} else if err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	if err := store.SetPassword(ctx, u.ID, hash); err != nil {
		return User{}, err
	}
	return u, store.DeleteSessions(ctx, u.ID)
}

// dummyHash is checked when no account has the email, so a wrong email takes as
// long as a wrong password.
var dummyHash, _ = HashPassword("not anyone's password")

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	if !sameOrigin(r) {
		return errOrigin
	}
	ip := clientIP(r)
	if s.tooManyFailures(ip, false) {
		return httpError{http.StatusTooManyRequests, "too many failed sign-ins from this address: try again in a few minutes"}
	}
	var req hubapi.HubLoginRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	u, err := s.Store.UserByEmail(r.Context(), strings.TrimSpace(req.Email))
	hash := u.PasswordHash
	if err != nil {
		hash = dummyHash
	}
	if !CheckPassword(hash, req.Password) || err != nil {
		s.tooManyFailures(ip, true)
		s.logf("failed sign-in for %q from %s", req.Email, ip)
		return httpError{http.StatusUnauthorized, "wrong email or password"}
	}
	s.logf("%s signed in from %s (%s)", u.Email, ip, cmpOr(req.Label, "unnamed client"))
	return s.startSession(w, r, u, req.Label)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	_, sess, err := s.authenticate(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteSession(r.Context(), sess.TokenHash); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	u, _, err := s.authenticate(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, toAPIUser(u))
}

func (s *Server) toAPIEnvironment(e Environment) hubapi.HubEnvironment {
	out := hubapi.HubEnvironment{ID: e.ID, Name: e.Name, LastSeenAt: e.LastSeenAt, Version: e.Version, Hostname: e.Hostname, CreatedAt: e.CreatedAt}
	s.mu.Lock()
	if t := s.tunnels[e.ID]; t != nil {
		out.Online, out.ConnectedAt = true, t.connected
	}
	s.mu.Unlock()
	return out
}

func (s *Server) listEnvironments(w http.ResponseWriter, r *http.Request) error {
	u, _, err := s.authenticate(r)
	if err != nil {
		return err
	}
	envs, err := s.Store.Environments(r.Context(), u.ID)
	if err != nil {
		return err
	}
	tunnels, err := s.Store.EnvironmentTunnels(r.Context())
	if err != nil {
		return err
	}
	byID := make(map[string]EnvironmentTunnel, len(tunnels))
	for _, t := range tunnels {
		byID[t.EnvironmentID] = t
	}
	out := make([]hubapi.HubEnvironment, 0, len(envs))
	for _, e := range envs {
		env := s.toAPIEnvironment(e)
		if t, ok := byID[e.ID]; ok && t.address() != "" {
			env.Transport, env.Address = t.transport(), t.address()
		}
		out = append(out, env)
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) error {
	u, _, err := s.authenticate(r)
	if err != nil {
		return err
	}
	var req hubapi.HubCreateEnvironmentRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	e, token, err := s.addEnvironment(r.Context(), u, req.Name, false)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, hubapi.HubEnvironmentToken{Environment: s.toAPIEnvironment(e), Token: token})
}

// addEnvironment adds an environment, and its token. With another name taken,
// rename gives it the first free one of name-2, name-3…; without, it fails.
func (s *Server) addEnvironment(ctx context.Context, u User, name string, rename bool) (Environment, string, error) {
	if !namePatten.MatchString(name) {
		return Environment{}, "", fmt.Errorf("%q can't name an environment: use lowercase letters, digits and dashes, like vps or home-pc", name)
	}
	token := newToken(environmentPrefix)
	e := Environment{ID: newID("env_"), OwnerID: u.ID, Name: name, TokenHash: hashToken(token), CreatedAt: time.Now()}
	for n := 2; ; n++ {
		err := s.Store.CreateEnvironment(ctx, e)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrExists) {
			return Environment{}, "", err
		}
		if !rename || n > 99 {
			return Environment{}, "", httpError{http.StatusConflict, fmt.Sprintf("you already have an environment named %s", name)}
		}
		e.Name = fmt.Sprintf("%s-%d", name, n)
	}
	s.logf("%s added the environment %s", cmpOr(u.Email, u.Name), e.Name)
	return e, token, nil
}

// ownEnvironment returns an environment of the signed-in user.
func (s *Server) ownEnvironment(r *http.Request) (User, Environment, error) {
	u, _, err := s.authenticate(r)
	if err != nil {
		return User{}, Environment{}, err
	}
	e, err := s.Store.Environment(r.Context(), r.PathValue("id"))
	if err != nil || e.OwnerID != u.ID {
		return User{}, Environment{}, httpError{http.StatusNotFound, "no such environment"}
	}
	return u, e, nil
}

func (s *Server) deleteEnvironment(w http.ResponseWriter, r *http.Request) error {
	u, e, err := s.ownEnvironment(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteEnvironment(r.Context(), e.ID); err != nil {
		return err
	}
	s.mu.Lock()
	t := s.tunnels[e.ID]
	s.mu.Unlock()
	if t != nil {
		_ = t.session.Close()
	}
	if s.Cloudflare != nil || s.Tailscale != nil {
		// Its address goes with it, without keeping the user waiting on
		// Cloudflare or Tailscale; CleanTunnels tries again if this fails.
		go func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
			defer cancel()
			tun, err := s.Store.EnvironmentTunnel(ctx, e.ID)
			if err != nil || !s.canRemove(tun) {
				return
			}
			if err := s.removeTunnel(ctx, tun); err != nil {
				s.logf("removing the tunnel on %s: %v", tun.Hostname, err)
				return
			}
			s.logf("removed the tunnel on %s with its environment", tun.Hostname)
		}()
	}
	s.logf("%s deleted the environment %s", cmpOr(u.Email, u.Name), e.Name)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// connect is the tunnel endpoint environments dial into.
func (s *Server) connect(w http.ResponseWriter, r *http.Request) error {
	token := bearer(r)
	if !strings.HasPrefix(token, environmentPrefix) {
		return httpError{http.StatusUnauthorized, "connect with an environment token"}
	}
	e, err := s.Store.EnvironmentByToken(r.Context(), hashToken(token))
	if errors.Is(err, ErrNotFound) {
		return httpError{http.StatusUnauthorized, "unknown environment token"}
	} else if err != nil {
		return err
	}
	// Environments aren't browsers, and authenticate with their token.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil
	}
	conn.SetReadLimit(-1)
	session, err := yamux.Client(websocket.NetConn(r.Context(), conn, websocket.MessageBinary), hubapi.TunnelConfig())
	if err != nil {
		_ = conn.CloseNow()
		return nil
	}
	t := &tunnel{session: session, connected: time.Now()}
	t.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "http", "environment", "environment"
			pr.Out.URL.Path = "/" + pr.In.PathValue("path")
			pr.Out.URL.RawPath = ""
			// The environment trusts the hub; the hub's own credentials stay here.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Origin")
		},
		Transport: &http.Transport{
			DialContext:         func(context.Context, string, string) (net.Conn, error) { return session.Open() },
			MaxIdleConnsPerHost: 32,
			IdleConnTimeout:     90 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			_ = writeJSON(w, http.StatusBadGateway, hubapi.Error{Error: fmt.Sprintf("%s didn't answer: %v", e.Name, err)})
		},
		// Clients close streams (events, terminals) whenever they're done with them.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	s.mu.Lock()
	if s.tunnels == nil {
		s.tunnels = map[string]*tunnel{}
	}
	old := s.tunnels[e.ID]
	s.tunnels[e.ID] = t
	s.mu.Unlock()
	if old != nil {
		_ = old.session.Close()
	}
	version, hostname := r.Header.Get(hubapi.HeaderVersion), r.Header.Get(hubapi.HeaderHostname)
	_ = s.Store.SeenEnvironment(r.Context(), e.ID, t.connected, version, hostname)
	s.logf("environment %s connected (%s, AgentBox %s, from %s)", e.Name, hostname, version, clientIP(r))

	<-session.CloseChan()
	s.mu.Lock()
	if s.tunnels[e.ID] == t {
		delete(s.tunnels, e.ID)
	}
	s.mu.Unlock()
	_ = s.Store.SeenEnvironment(context.WithoutCancel(r.Context()), e.ID, time.Now(), version, hostname)
	s.logf("environment %s disconnected", e.Name)
	return nil
}

// proxy passes a request to an environment's API through its tunnel.
func (s *Server) proxy(w http.ResponseWriter, r *http.Request) error {
	_, e, err := s.ownEnvironment(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	t := s.tunnels[e.ID]
	s.mu.Unlock()
	if t == nil {
		last := "never"
		if !e.LastSeenAt.IsZero() {
			last = e.LastSeenAt.Format(time.RFC3339)
		}
		return httpError{http.StatusServiceUnavailable, fmt.Sprintf("%s is offline (last connected: %s)", e.Name, last)}
	}
	t.proxy.ServeHTTP(w, r)
	return nil
}

// Close disconnects every environment.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tunnels {
		_ = t.session.Close()
	}
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
