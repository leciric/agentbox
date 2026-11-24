package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/state"
)

// Remote MCP without a hub (remotemcp.go has the hub's route). An app reaches
// this machine by one of two more routes, each pairing made for one of them:
//
//   - local: an app on the computer AgentBox runs on (Claude Code, Claude
//     Desktop, Cursor) calls http://127.0.0.1:<port>/mcp. In AgentBox's VM the
//     computer's forwarder opens that port, on its loopback address only, and
//     passes each request on to the daemon's socket at api.MCPLocalPath
//     (internal/lan.Forward, the process that opens phones' port); a daemon
//     that isn't in a VM opens it itself. It's open while an app is paired
//     for it.
//   - tunnel: an app anywhere calls /mcp on the tunnel phones use
//     (lantunnel.go), which runs while phones or apps want it. Turned on by
//     itself (state.SettingRemoteMCPTunnel), apart from chatting from a phone.
//
// Either way the app sends its pairing's bearer token, of which state.db keeps
// only the hash, and the daemon checks it on every call, with the route it was
// made for: a local token is no use through the tunnel. What the pairing may
// do is then exactly what it may through the hub. Nothing else of the daemon's
// API is reachable on either: the forwarder passes on /mcp to one path, and
// the tunnel's listener serves phones' handler and /mcp only. There is no
// OAuth without a hub: an app that only signs in that way (claude.ai,
// ChatGPT) still needs one.

// mcpLocalState is the local route's port: the daemon's own listener when it
// opens it, and what the computer's forwarder said of it otherwise.
type mcpLocalState struct {
	mu        sync.Mutex
	srv       *http.Server
	port      int
	listenErr string
	host      *api.MCPLocalHostReport
	hostAt    time.Time

	// routes is the daemon's API, which the tools call, made on first use;
	// touch is when each pairing was last recorded as used.
	once   sync.Once
	routes http.Handler
	touch  remoteMCPTouch
}

func (s *Server) mcpRoutes() http.Handler {
	s.mcpLocal.once.Do(func() { s.mcpLocal.routes = s.routes() })
	return s.mcpLocal.routes
}

func (s *Server) mcpPort(ctx context.Context) (int, error) {
	v, err := s.store.Setting(ctx, state.SettingRemoteMCPPort)
	if err != nil || v == "" {
		return state.DefaultRemoteMCPPort, err
	}
	port, err := strconv.Atoi(v)
	if err != nil || port < 1 || port > 65535 {
		return state.DefaultRemoteMCPPort, nil
	}
	return port, nil
}

func mcpLocalURL(port int) string {
	// 127.0.0.1, not localhost: an app that looks localhost up as ::1 first
	// would find nothing there.
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/mcp"
}

// applyMCPLocal opens or closes the local port, when the daemon is what opens
// it: while an app is paired for it.
func (s *Server) applyMCPLocal(ctx context.Context) {
	if !s.lan.own {
		return
	}
	want, err := s.store.HasMCPRoute(ctx, state.MCPRouteLocal)
	if err != nil {
		s.logf("remote mcp: %v", err)
		return
	}
	port, _ := s.mcpPort(ctx)
	l := &s.mcpLocal
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.srv != nil && (!want || l.port != port) {
		_ = l.srv.Close()
		l.srv = nil
		s.logf("remote mcp: port %d closed", l.port)
	}
	l.listenErr = ""
	if !want || l.srv != nil {
		return
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		l.listenErr = fmt.Sprintf("port %d can't be opened: %v", port, err)
		s.logf("remote mcp: %s", l.listenErr)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) { s.serveMCPRoute(w, r, state.MCPRouteLocal, "") })
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	l.srv, l.port = srv, port
	go func() { _ = srv.Serve(ln) }()
	s.logf("remote mcp: apps on this computer reach AgentBox at %s", mcpLocalURL(port))
}

func (s *Server) closeMCPLocal() {
	l := &s.mcpLocal
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.srv != nil {
		_ = l.srv.Close()
		l.srv = nil
	}
}

// mcpLocalStatus is the local route as GET /v1/remote/mcp shows it.
func (s *Server) mcpLocalStatus(ctx context.Context) (api.MCPLocal, error) {
	port, err := s.mcpPort(ctx)
	if err != nil {
		return api.MCPLocal{}, err
	}
	want, err := s.store.HasMCPRoute(ctx, state.MCPRouteLocal)
	if err != nil {
		return api.MCPLocal{}, err
	}
	out := api.MCPLocal{Port: port, URL: mcpLocalURL(port)}
	l := &s.mcpLocal
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.host != nil && time.Since(l.hostAt) < hostReportTTL:
		out.Listening = l.host.Listening && l.host.Port == port
		out.Error = l.host.Error
	case s.lan.own:
		out.Listening = l.srv != nil
		out.Error = l.listenErr
	case want && hostos.OS() == hostos.Windows:
		out.Error = "waiting for the AgentBox app to open the port on Windows: apps on this computer reach AgentBox while it's open"
	case want:
		out.Error = "waiting for AgentBox's VM to open the port on this computer"
	}
	return out, nil
}

// getMCPLocalHost tells the computer's forwarder whether to open the local
// port, and which.
func (s *Server) getMCPLocalHost(w http.ResponseWriter, r *http.Request) error {
	port, err := s.mcpPort(r.Context())
	if err != nil {
		return err
	}
	open, err := s.store.HasMCPRoute(r.Context(), state.MCPRouteLocal)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.MCPLocalHost{Port: port, Open: open})
}

// mcpLocalHostReport is the forwarder saying how the local port is.
func (s *Server) mcpLocalHostReport(w http.ResponseWriter, r *http.Request) error {
	var req api.MCPLocalHostReport
	if err := readJSON(r, &req); err != nil {
		return err
	}
	l := &s.mcpLocal
	l.mu.Lock()
	changed := l.host == nil || time.Since(l.hostAt) >= hostReportTTL || *l.host != req
	l.host, l.hostAt = &req, time.Now()
	l.mu.Unlock()
	if changed {
		if req.Error != "" {
			s.logf("remote mcp: %s", req.Error)
		}
		s.publishLAN(r.Context())
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// serveMCPLocal is what the forwarder passes on from the local port.
func (s *Server) serveMCPLocal(w http.ResponseWriter, r *http.Request) error {
	s.serveMCPRoute(w, r, state.MCPRouteLocal, "")
	return nil
}

// serveMCPRoute answers an app that came by route with its bearer token. addr
// is who it came from through the tunnel, whose wrong tokens count against
// it as a phone's do.
func (s *Server) serveMCPRoute(w http.ResponseWriter, r *http.Request, route, addr string) {
	unauthorized := func(msg string) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="agentbox"`)
		_ = writeJSON(w, http.StatusUnauthorized, api.Error{Error: msg})
	}
	token, ok := lanBearer(r)
	if !ok {
		unauthorized("pair this app in AgentBox's Settings, under Other apps, and send its token: Authorization: Bearer <token>")
		return
	}
	limited := route == state.MCPRouteTunnel && addr != ""
	if limited && s.lan.limiter.Blocked(addr, false) {
		s.tooMany(w)
		return
	}
	p, err := s.store.MCPPairingByToken(r.Context(), token)
	switch {
	case errors.Is(err, state.ErrNotFound), err == nil && p.Route != route:
		if limited {
			s.lan.limiter.Fail(addr)
		}
		if err == nil {
			unauthorized("this app was paired to reach AgentBox another way: use the address AgentBox gave with its token")
			return
		}
		unauthorized("this app's pairing with AgentBox was revoked, or its token was replaced with a new one")
		return
	case err != nil:
		writeError(w, err)
		return
	}
	s.serveMCPPairing(w, r, p, s.mcpRoutes(), &s.mcpLocal.touch)
}

// tunnelHandler is what the tunnel reaches: apps' MCP endpoint, at /mcp, when
// apps may come that way, and the phone page when phones may.
func (s *Server) tunnelHandler() http.Handler {
	phones := s.lanHandler(lanViaTunnel)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.URL.Path == "/mcp" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
			if on, err := s.store.Flag(ctx, state.SettingRemoteMCPTunnel); err != nil || !on {
				_ = writeJSON(w, http.StatusServiceUnavailable, api.Error{Error: "other apps can't reach AgentBox through its tunnel: turn it on in AgentBox's Settings, under Other apps"})
				return
			}
			s.serveMCPRoute(w, r, state.MCPRouteTunnel, tunnelAddr(r))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/.well-known/oauth") {
			// An app looking for how to sign in: there's no OAuth without a
			// hub, only the token it was given.
			http.NotFound(w, r)
			return
		}
		if on, err := s.store.Flag(ctx, state.SettingLANTunnel); err != nil || !on {
			http.Error(w, "Chatting from a phone through the tunnel is off in AgentBox.", http.StatusServiceUnavailable)
			return
		}
		phones.ServeHTTP(w, r)
	})
}

// tunnelAddr is who a request through the tunnel came from, as lanHandler
// works it out.
func tunnelAddr(r *http.Request) string {
	if ip := r.Header.Get("Cf-Connecting-IP"); ip != "" {
		return ip
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		parts := strings.Split(fwd, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// mcpTunnelStatus is the tunnel route as GET /v1/remote/mcp shows it.
func (s *Server) mcpTunnelStatus(ctx context.Context) (api.MCPTunnel, error) {
	on, err := s.store.Flag(ctx, state.SettingRemoteMCPTunnel)
	if err != nil {
		return api.MCPTunnel{}, err
	}
	t, err := s.lanTunnelStatus(ctx)
	if err != nil {
		return api.MCPTunnel{}, err
	}
	out := api.MCPTunnel{Enabled: on, Mode: t.Mode, State: t.State, Error: t.Error, Transport: t.Transport}
	if t.URL != "" {
		out.URL = strings.TrimSuffix(t.URL, "/") + "/mcp"
	}
	return out, nil
}

// updateRemoteMCP is PATCH /v1/remote/mcp.
func (s *Server) updateRemoteMCP(w http.ResponseWriter, r *http.Request) error {
	var req api.UpdateMCPRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if req.Port != nil {
		port := *req.Port
		if port != 0 && (port < 1024 || port > 65535) {
			return fmt.Errorf("the port must be between 1024 and 65535, not %d", port)
		}
		v := ""
		if port != 0 && port != state.DefaultRemoteMCPPort {
			v = strconv.Itoa(port)
		}
		if err := s.store.SetSetting(ctx, state.SettingRemoteMCPPort, v); err != nil {
			return err
		}
	}
	if req.Tunnel != nil {
		if err := s.store.SetFlag(ctx, state.SettingRemoteMCPTunnel, *req.Tunnel); err != nil {
			return err
		}
		if *req.Tunnel {
			s.logf("remote mcp: other apps may reach AgentBox through its tunnel")
		} else {
			s.logf("remote mcp: other apps no longer reach AgentBox through its tunnel")
		}
	}
	// Run's context, not the request's: the listeners outlive it.
	s.applyLAN(s.runCtx)
	s.applyMCPLocal(s.runCtx)
	s.publishLAN(ctx)
	return s.getRemoteMCP(w, r)
}
