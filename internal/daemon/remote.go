package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentbox/hubapi"
	"agentbox/internal/api"
	"agentbox/internal/remote"
)

// remoteConfig is the hub this machine connects to as an environment, saved by
// `agentbox remote connect`.
type remoteConfig struct {
	Hub   string `json:"hub"`
	Token string `json:"token"`
}

func (s *Server) remoteConfigPath() string { return filepath.Join(s.cfg.Paths.Config, "remote.json") }

// startRemote connects to the saved hub, replacing an earlier connection. Without
// a saved hub it only stops the earlier one.
func (s *Server) startRemote(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteStop != nil {
		s.remoteStop()
		s.remote, s.remoteStop = nil, nil
	}
	cfg := s.readRemoteConfig()
	if cfg == nil {
		return
	}
	hostname, _ := os.Hostname()
	c := &remote.Connector{Hub: cfg.Hub, Token: cfg.Token, Handler: s.remoteHandler(), Version: Version, Hostname: hostname, Log: s.cfg.Log}
	rctx, cancel := context.WithCancel(ctx)
	s.remote, s.remoteStop = c, cancel
	go c.Run(rctx)
}

// readRemoteConfig is the saved hub, or nil.
func (s *Server) readRemoteConfig() *remoteConfig {
	data, err := os.ReadFile(s.remoteConfigPath())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logf("remote: %v", err)
		}
		return nil
	}
	var cfg remoteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		s.logf("remote: %s: %v", s.remoteConfigPath(), err)
		return nil
	}
	if cfg.Hub == "" || cfg.Token == "" {
		return nil
	}
	return &cfg
}

// remoteHandler serves the hub: the same API as the socket, except what only
// makes sense on this machine. A remote client couldn't start a stopped daemon
// again, nor should it move this machine to another hub.
func (s *Server) remoteHandler() http.Handler {
	routes := s.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/shutdown" || r.URL.Path == "/v1/remote" || strings.HasPrefix(r.URL.Path, "/v1/remote/") || r.URL.Path == "/v1/lan" || strings.HasPrefix(r.URL.Path, "/v1/lan/") {
			_ = writeJSON(w, http.StatusForbidden, api.Error{Error: fmt.Sprintf("%s %s works only on the machine itself", r.Method, r.URL.Path)})
			return
		}
		routes.ServeHTTP(w, r)
	})
}

func (s *Server) remoteStatus() api.RemoteStatus {
	s.mu.Lock()
	c := s.remote
	var pairing *api.RemotePairing
	if s.remotePairing != nil {
		p := *s.remotePairing
		pairing = &p
	}
	s.mu.Unlock()
	if c == nil {
		return api.RemoteStatus{Pairing: pairing}
	}
	st := c.Status()
	return api.RemoteStatus{Configured: true, Hub: c.Hub, Connected: st.Connected, Since: st.Since, Error: st.LastError, Pairing: pairing}
}

// stopPairing stops waiting for a pairing's approval, and forgets it.
func (s *Server) stopPairing() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remotePairStop != nil {
		s.remotePairStop()
	}
	s.remotePairing, s.remotePairStop = nil, nil
}

// pairRemote asks a hub to approve this machine as an environment, and waits
// for it in the background: once a device signed in to the hub approves the
// code, the machine saves the token it gets and connects, as connectRemote
// would with it. The app approves it itself when it's signed in to the hub,
// which makes connecting one click.
func (s *Server) pairRemote(w http.ResponseWriter, r *http.Request) error {
	var req api.RemotePairRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	hub, _, err := api.ParseHubLink(req.Hub)
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	s.stopPairing()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	hc := api.HubClient{URL: hub}
	p, err := hc.Pair(ctx, api.HubPairRequest{
		Kind: hubapi.PairEnvironment, Label: "AgentBox on " + cmp.Or(hostname, "this machine"),
		Name: strings.TrimSpace(req.Name), Hostname: hostname, Version: Version,
	})
	if err != nil {
		return fmt.Errorf("asking %s to connect this machine: %w", hub, err)
	}
	pairing := &api.RemotePairing{Hub: hub, Code: p.Code, ApproveURL: p.ApproveURL, ExpiresAt: p.ExpiresAt, QR: p.QR}
	wctx, stop := context.WithCancel(s.runCtx)
	s.mu.Lock()
	s.remotePairing, s.remotePairStop = pairing, stop
	s.mu.Unlock()
	s.logf("remote: waiting for %s to approve this machine, code %s", hub, p.Code)
	go func() {
		defer stop()
		result, err := hc.WaitForPairing(wctx, p)
		if wctx.Err() != nil {
			return // stopped, or replaced by another
		}
		if err == nil && result.Environment == nil {
			err = errors.New("the hub approved it, but sent no environment token")
		}
		if err == nil {
			err = s.saveRemoteConfig(remoteConfig{Hub: hub, Token: result.Environment.Token})
		}
		s.mu.Lock()
		if s.remotePairing != pairing {
			s.mu.Unlock()
			return
		}
		if err != nil {
			failed := *pairing
			failed.Error = err.Error()
			s.remotePairing = &failed
		} else {
			s.remotePairing = nil
		}
		s.mu.Unlock()
		if err != nil {
			s.logf("remote: pairing with %s: %v", hub, err)
			return
		}
		s.logf("remote: %s approved this machine as %s", hub, result.Environment.Environment.Name)
		s.startRemote(s.runCtx)
		s.hubChanged(s.runCtx)
	}()
	return writeJSON(w, http.StatusOK, s.remoteStatus())
}

func (s *Server) saveRemoteConfig(cfg remoteConfig) error {
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.MkdirAll(s.cfg.Paths.Config, 0o700); err != nil {
		return err
	}
	tmp := s.remoteConfigPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.remoteConfigPath())
}

func (s *Server) getRemote(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, s.remoteStatus())
}

// connectRemote saves the hub and connects, answering once the first attempt
// succeeds or fails, or after 10 seconds.
func (s *Server) connectRemote(w http.ResponseWriter, r *http.Request) error {
	var req api.RemoteConnectRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	req.Hub = strings.TrimRight(strings.TrimSpace(req.Hub), "/")
	if u, err := url.Parse(req.Hub); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%q isn't a hub URL: use https://hub.example.com", req.Hub)
	}
	req.Token = strings.TrimSpace(req.Token)
	if !strings.HasPrefix(req.Token, hubapi.EnvironmentTokenPrefix) {
		return errors.New("that isn't an environment token: add the environment on the hub, which shows its token (abx_e_…)")
	}
	if err := s.saveRemoteConfig(remoteConfig{Hub: req.Hub, Token: req.Token}); err != nil {
		return err
	}
	s.stopPairing()
	s.startRemote(s.runCtx)
	s.hubChanged(s.runCtx)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if st := s.remoteStatus(); st.Connected || st.Error != "" {
			break
		}
	}
	return writeJSON(w, http.StatusOK, s.remoteStatus())
}

func (s *Server) disconnectRemote(w http.ResponseWriter, r *http.Request) error {
	s.stopPairing()
	if err := os.Remove(s.remoteConfigPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.startRemote(s.runCtx)
	s.hubChanged(s.runCtx)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
