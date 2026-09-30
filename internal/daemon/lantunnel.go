package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/tunnel"
)

// Reaching the phone page from anywhere, not only the local network: the
// daemon runs a Cloudflare Tunnel (internal/tunnel) that brings the page to
// an https address on the internet. Off by default, and only while chatting
// from a phone is on too.
//
// The tunnel reaches the daemon on a listener of its own, on the loopback
// address of the machine the daemon runs on — AgentBox's VM, in VM mode, so
// cloudflared runs in the VM next to the daemon and nothing is opened on the
// host. The listener serves the same lanHandler as the network's port, but
// knows the request came over https (a Secure cookie) and from whom
// (Cf-Connecting-IP). A quick tunnel's listener takes any free port; a named
// tunnel's is DefaultLANTunnelPort, which its public hostname points at in
// Cloudflare's dashboard.
//
// A quick tunnel's address changes every time cloudflared starts, and a
// paired phone's cookie belongs to the address it paired on: the tunnel is
// only started again when its settings change, and a phone scans a new code
// after a daemon restart. A named tunnel keeps its hostname.

// lanTunnel is the tunnel while it's on.
type lanTunnel struct {
	key    string       // the token and hostname it runs with
	srv    *http.Server // the listener cloudflared reaches the daemon on
	origin string
	t      *tunnel.Tunnel // nil when it couldn't be started
	err    string         // why it couldn't
}

// stop ends cloudflared, which can take a few seconds: not under lan.mu.
func (lt *lanTunnel) stop() {
	if lt.srv != nil {
		_ = lt.srv.Close()
	}
	if lt.t != nil {
		lt.t.Stop()
	}
}

type lanTunnelSettings struct {
	want            bool
	token, hostname string
	err             string // the settings can't be read: the token can't be opened, say
}

func (s *Server) lanTunnelConfig(ctx context.Context, lanOn bool) lanTunnelSettings {
	on, err := s.store.Flag(ctx, state.SettingLANTunnel)
	if err != nil || !on || !lanOn {
		return lanTunnelSettings{}
	}
	c := lanTunnelSettings{want: true}
	if c.token, err = s.secrets().Setting(ctx, state.SettingLANTunnelToken); err != nil {
		c.err = "the tunnel's token can't be read: " + err.Error()
		return c
	}
	if c.hostname, err = s.store.Setting(ctx, state.SettingLANTunnelHostname); err != nil {
		c.err = err.Error()
	}
	return c
}

// applyTunnelLocked starts or stops the tunnel to match c, with lan.mu held.
// It returns what's left to stop once lan.mu is let go, or nil.
func (s *Server) applyTunnelLocked(ctx context.Context, c lanTunnelSettings) func() {
	l := s.lan
	key := c.token + "\x00" + c.hostname
	if l.tunnel != nil && c.want && c.err == "" && l.tunnel.key == key && l.tunnel.err == "" {
		return nil
	}
	old := l.tunnel
	l.tunnel = nil
	if old != nil && old.srv != nil {
		// Closed now, so a new tunnel can have its port.
		_ = old.srv.Close()
		old.srv = nil
	}
	if c.want {
		l.tunnel = s.startTunnel(ctx, c, key)
	}
	if old == nil {
		return nil
	}
	return old.stop
}

func (s *Server) startTunnel(ctx context.Context, c lanTunnelSettings, key string) *lanTunnel {
	l := s.lan
	lt := &lanTunnel{key: key}
	if c.err != "" {
		lt.err = c.err
		return lt
	}
	port := 0
	if c.token != "" {
		port = l.tunnelPort
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		lt.err = fmt.Sprintf("port %d, where the tunnel reaches AgentBox, can't be opened: %v", port, err)
		s.logf("phones: %s", lt.err)
		return lt
	}
	lt.origin = "http://" + ln.Addr().String()
	lt.srv = &http.Server{
		Handler:           s.lanHandler(lanViaTunnel),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	srv := lt.srv
	go func() { _ = srv.Serve(ln) }()

	tools := s.cfg.Paths.Tools()
	binary := l.cloudflared
	if binary == nil {
		binary = tunnel.Installer{Dir: tools}.Ensure
	}
	home := filepath.Join(tools, "cloudflared", "home")
	_ = os.MkdirAll(home, 0o700)
	var last string
	lt.t = tunnel.Start(ctx, tunnel.Config{Binary: binary, Origin: lt.origin, Token: c.token, Hostname: c.hostname, Home: home},
		func(st tunnel.Status) {
			if st.State == tunnel.StateRunning && st.URL != last {
				last = st.URL
				s.logf("phones can pair from anywhere at %s", st.URL)
			}
			s.publishLAN(ctx)
		}, s.logf)
	kind := "a quick tunnel"
	if c.token != "" {
		kind = "the tunnel for " + c.hostname
	}
	s.logf("phones: starting %s to %s", kind, lt.origin)
	return lt
}

func (s *Server) lanTunnelStatus(ctx context.Context) (api.LANTunnel, error) {
	on, err := s.store.Flag(ctx, state.SettingLANTunnel)
	if err != nil {
		return api.LANTunnel{}, err
	}
	token, err := s.store.Setting(ctx, state.SettingLANTunnelToken)
	if err != nil {
		return api.LANTunnel{}, err
	}
	hostname, err := s.store.Setting(ctx, state.SettingLANTunnelHostname)
	if err != nil {
		return api.LANTunnel{}, err
	}
	l := s.lan
	l.mu.Lock()
	defer l.mu.Unlock()
	out := api.LANTunnel{Enabled: on, Named: token != "", Hostname: hostname, State: "off",
		Origin: fmt.Sprintf("http://localhost:%d", l.tunnelPort)}
	switch lt := l.tunnel; {
	case lt == nil:
	case lt.err != "":
		out.State, out.Error = tunnel.StateFailed, lt.err
	default:
		st := lt.t.Status()
		out.State, out.URL, out.Error = st.State, st.URL, st.Error
		if st.State != tunnel.StateRunning {
			out.URL = ""
		}
	}
	return out, nil
}

// updateLANTunnel stores what PATCH /v1/lan says of the tunnel; applyLAN then
// starts or stops it.
func (s *Server) updateLANTunnel(ctx context.Context, req api.UpdateLANRequest) error {
	if req.TunnelToken != nil || req.TunnelHostname != nil {
		if req.TunnelToken != nil && strings.TrimSpace(*req.TunnelToken) == "" {
			// Back to a quick tunnel.
			if err := s.secrets().SetSetting(ctx, state.SettingLANTunnelToken, ""); err != nil {
				return err
			}
			if err := s.store.SetSetting(ctx, state.SettingLANTunnelHostname, ""); err != nil {
				return err
			}
			s.logf("phones: the tunnel is a quick one again")
		} else {
			var token string
			var err error
			if req.TunnelToken != nil {
				if token, err = tunnelToken(*req.TunnelToken); err != nil {
					return err
				}
			} else if token, err = s.secrets().Setting(ctx, state.SettingLANTunnelToken); err != nil {
				return err
			} else if token == "" {
				return errors.New("a named tunnel needs its token as well as its hostname")
			}
			hostname, err := s.store.Setting(ctx, state.SettingLANTunnelHostname)
			if err != nil {
				return err
			}
			if req.TunnelHostname != nil {
				hostname = *req.TunnelHostname
			}
			if hostname, err = tunnelHostname(hostname); err != nil {
				return err
			}
			if err := s.secrets().SetSetting(ctx, state.SettingLANTunnelToken, token); err != nil {
				return err
			}
			if err := s.store.SetSetting(ctx, state.SettingLANTunnelHostname, hostname); err != nil {
				return err
			}
			// The token is a password to the tunnel: it's never logged.
			s.logf("phones: the tunnel is now the named tunnel for %s", hostname)
		}
	}
	if req.Tunnel != nil {
		if err := s.store.SetFlag(ctx, state.SettingLANTunnel, *req.Tunnel); err != nil {
			return err
		}
	}
	return nil
}

// tunnelToken finds a named tunnel's token in what was pasted: the token
// itself, or the whole `cloudflared service install <token>` command
// Cloudflare's dashboard shows. A token is base64 of JSON naming the account,
// the tunnel and its secret.
func tunnelToken(pasted string) (string, error) {
	for _, field := range strings.Fields(pasted) {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			raw, err := enc.DecodeString(field)
			if err != nil {
				continue
			}
			var tok struct {
				Account string `json:"a"`
				Tunnel  string `json:"t"`
				Secret  string `json:"s"`
			}
			if json.Unmarshal(raw, &tok) == nil && tok.Tunnel != "" && tok.Secret != "" {
				return field, nil
			}
		}
	}
	return "", errors.New("that isn't a tunnel token: copy it from your tunnel's page in Cloudflare's dashboard (Networks → Tunnels), where it's the long string after `cloudflared service install`")
}

var hostnameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// tunnelHostname is the named tunnel's public hostname, from what was typed:
// with or without https:// and a trailing slash.
func tunnelHostname(typed string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(typed))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h = strings.TrimSuffix(h, "/")
	if h == "" {
		return "", errors.New("a named tunnel needs the public hostname it's on in Cloudflare's dashboard, like chat.example.com")
	}
	if !hostnameRe.MatchString(h) {
		return "", fmt.Errorf("%q isn't a hostname, like chat.example.com", typed)
	}
	return h, nil
}
