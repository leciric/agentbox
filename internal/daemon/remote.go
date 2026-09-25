package daemon

import (
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
	data, err := os.ReadFile(s.remoteConfigPath())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logf("remote: %v", err)
		}
		return
	}
	var cfg remoteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		s.logf("remote: %s: %v", s.remoteConfigPath(), err)
		return
	}
	hostname, _ := os.Hostname()
	c := &remote.Connector{Hub: cfg.Hub, Token: cfg.Token, Handler: s.remoteHandler(), Version: Version, Hostname: hostname, Log: s.cfg.Log}
	rctx, cancel := context.WithCancel(ctx)
	s.remote, s.remoteStop = c, cancel
	go c.Run(rctx)
}

// remoteHandler serves the hub: the same API as the socket, except what only
// makes sense on this machine. A remote client couldn't start a stopped daemon
// again, nor should it move this machine to another hub.
func (s *Server) remoteHandler() http.Handler {
	routes := s.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/shutdown" || r.URL.Path == "/v1/remote" {
			writeJSON(w, http.StatusForbidden, api.Error{Error: fmt.Sprintf("%s %s works only on the machine itself", r.Method, r.URL.Path)})
			return
		}
		routes.ServeHTTP(w, r)
	})
}

func (s *Server) remoteStatus() api.RemoteStatus {
	s.mu.Lock()
	c := s.remote
	s.mu.Unlock()
	if c == nil {
		return api.RemoteStatus{}
	}
	st := c.Status()
	return api.RemoteStatus{Configured: true, Hub: c.Hub, Connected: st.Connected, Since: st.Since, Error: st.LastError}
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
	data, _ := json.MarshalIndent(remoteConfig{Hub: req.Hub, Token: req.Token}, "", "  ")
	if err := os.MkdirAll(s.cfg.Paths.Config, 0o700); err != nil {
		return err
	}
	tmp := s.remoteConfigPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.remoteConfigPath()); err != nil {
		return err
	}
	s.startRemote(s.runCtx)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if st := s.remoteStatus(); st.Connected || st.Error != "" {
			break
		}
	}
	return writeJSON(w, http.StatusOK, s.remoteStatus())
}

func (s *Server) disconnectRemote(w http.ResponseWriter, r *http.Request) error {
	if err := os.Remove(s.remoteConfigPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.startRemote(s.runCtx)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
