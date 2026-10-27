package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The daemon's second listener, for the desktop app on another machine: a
// loopback port that `tailscale serve` publishes as an https address on this
// machine's MagicDNS name. Only the tailnet reaches it, and only through the
// proxy, which adds who asked; the listener itself never leaves loopback.
const (
	// tailnetAddr is where the daemon listens for the proxy.
	tailnetAddr = "127.0.0.1:7781"
	// tailnetLoginHeader is the Tailscale Serve identity header: the login of
	// whoever made the request. Serve removes it from incoming requests and
	// sets its own, so a tailnet client can't choose it. A local process can,
	// which is why the listener is on loopback: local processes are already
	// trusted with this machine's daemon socket.
	tailnetLoginHeader = "Tailscale-User-Login"
)

type tailnetState struct {
	mu  sync.Mutex
	srv *http.Server // the listener, when it is open
	err string       // why it couldn't be
	// addr is where it listens. Empty is tailnetAddr; a test sets its own, so
	// parallel tests don't fight over one port.
	addr string
}

// applyTailnet opens or closes the listener to match the setting. It is safe
// to call whenever the setting changes, and does nothing when the listener is
// already as it should be.
func (s *Server) applyTailnet(ctx context.Context) {
	on, err := s.store.Flag(ctx, state.SettingTailnet)
	if err != nil {
		s.logf("tailnet: %v", err)
		return
	}
	t := &s.tailnet
	t.mu.Lock()
	defer t.mu.Unlock()
	if !on {
		if t.srv != nil {
			_ = t.srv.Close()
			t.srv = nil
			s.logf("the tailnet listener is closed")
		}
		t.err = ""
		return
	}
	if t.srv != nil {
		return
	}
	addr := t.addr
	if addr == "" {
		addr = tailnetAddr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.err = fmt.Sprintf("%s can't be opened: %v", addr, err)
		s.logf("tailnet: %s", t.err)
		return
	}
	// The address it actually got, which is addr itself unless a test asked
	// for port zero.
	t.addr = ln.Addr().String()
	srv := &http.Server{
		Handler:           s.tailnetHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	t.srv, t.err = srv, ""
	go func() { _ = srv.Serve(ln) }()
	s.logf("serving the daemon to the tailnet on %s", t.addr)
}

// closeTailnet ends the listener as the daemon stops.
func (s *Server) closeTailnet() {
	t := &s.tailnet
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.srv != nil {
		_ = t.srv.Close()
		t.srv = nil
	}
}

// tailnetHandler serves the API to whoever Tailscale says is asking, and only
// what a client on another machine may do: the machine's own settings stay on
// its socket.
func (s *Server) tailnetHandler() http.Handler {
	routes := s.remoteHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		login := strings.TrimSpace(r.Header.Get(tailnetLoginHeader))
		if login == "" {
			_ = writeJSON(w, http.StatusUnauthorized, api.Error{Error: "this request didn't come through Tailscale, which is what says who is asking"})
			return
		}
		allowed, err := s.tailnetAllows(r.Context(), login)
		if err != nil {
			_ = writeJSON(w, http.StatusInternalServerError, api.Error{Error: err.Error()})
			return
		}
		if !allowed {
			_ = writeJSON(w, http.StatusForbidden, api.Error{Error: fmt.Sprintf("%s isn't allowed into this daemon: allow it with agentbox tailnet on --allow %s", login, login)})
			return
		}
		routes.ServeHTTP(w, r)
	})
}

// tailnetAllows says whether login may use the listener.
func (s *Server) tailnetAllows(ctx context.Context, login string) (bool, error) {
	allow, err := s.tailnetAllow(ctx)
	if err != nil {
		return false, err
	}
	for _, a := range allow {
		if strings.EqualFold(a, login) {
			return true, nil
		}
	}
	return false, nil
}

// tailnetAllow is who the settings let in.
func (s *Server) tailnetAllow(ctx context.Context) ([]string, error) {
	value, err := s.store.Setting(ctx, state.SettingTailnetAllow)
	if err != nil {
		return nil, err
	}
	return splitLogins(value), nil
}

// splitLogins reads the comma separated logins of a setting, in the order they
// were written, without empties.
func splitLogins(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		if login := strings.TrimSpace(part); login != "" {
			out = append(out, login)
		}
	}
	return out
}

func (s *Server) tailnetStatus(ctx context.Context) (api.TailnetStatus, error) {
	on, err := s.store.Flag(ctx, state.SettingTailnet)
	if err != nil {
		return api.TailnetStatus{}, err
	}
	allow, err := s.tailnetAllow(ctx)
	if err != nil {
		return api.TailnetStatus{}, err
	}
	st := api.TailnetStatus{Enabled: on, Addr: tailnetAddr, Allow: allow}
	s.tailnet.mu.Lock()
	if s.tailnet.addr != "" {
		st.Addr = s.tailnet.addr
	}
	st.Listening = s.tailnet.srv != nil
	st.Error = s.tailnet.err
	s.tailnet.mu.Unlock()
	return st, nil
}

func (s *Server) getTailnet(w http.ResponseWriter, r *http.Request) error {
	st, err := s.tailnetStatus(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, st)
}

// updateTailnet writes the setting, then opens or closes the listener, and
// refuses enabling without anyone allowed in.
func (s *Server) updateTailnet(w http.ResponseWriter, r *http.Request) error {
	var req api.UpdateTailnetRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	allow, err := s.tailnetAllow(ctx)
	if err != nil {
		return err
	}
	if req.Allow != nil {
		allow = splitLogins(strings.Join(*req.Allow, ","))
	}
	if req.Enabled != nil && *req.Enabled && len(allow) == 0 {
		return fmt.Errorf("say who may use it first, like agentbox tailnet on --allow %s", "you@example.com")
	}
	if req.Allow != nil {
		if err := s.store.SetSetting(ctx, state.SettingTailnetAllow, strings.Join(allow, ",")); err != nil {
			return err
		}
	}
	if req.Enabled != nil {
		if err := s.store.SetFlag(ctx, state.SettingTailnet, *req.Enabled); err != nil {
			return err
		}
	}
	s.applyTailnet(s.runCtx)
	s.events.publish(api.EventTailnet, nil)
	return s.getTailnet(w, r)
}
