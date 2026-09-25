package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Each agent gets its own socket, exposed inside the agent by an Incus proxy
// device. The daemon knows which agent is calling from the socket a request
// arrives on, so the in-agent API can only ever describe or act on that agent.

// agentSocketPath is short (a hash of the instance name) because unix socket
// paths are limited to 108 bytes.
func (s *Server) agentSocketPath(instance string) string {
	sum := sha256.Sum256([]byte(instance))
	return filepath.Join(s.cfg.Paths.AgentSockets(), hex.EncodeToString(sum[:6])+".sock")
}

func (s *Server) serveAgentAPI(instance string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.agentAPIs[instance]; ok {
		return nil
	}
	path := s.agentSocketPath(instance)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	// Only root (Incus' proxy) and this user can connect.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	srv := &http.Server{Handler: s.inAgentRoutes(instance)}
	s.agentAPIs[instance] = srv
	go func() { _ = srv.Serve(ln) }()
	return nil
}

func (s *Server) stopAgentAPI(instance string) {
	s.mu.Lock()
	srv := s.agentAPIs[instance]
	delete(s.agentAPIs, instance)
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
	_ = os.Remove(s.agentSocketPath(instance))
}

func (s *Server) closeAgentAPIs() {
	s.mu.Lock()
	instances := make([]string, 0, len(s.agentAPIs))
	for instance := range s.agentAPIs {
		instances = append(instances, instance)
	}
	s.mu.Unlock()
	for _, instance := range instances {
		s.stopAgentAPI(instance)
	}
}

func (s *Server) inAgentRoutes(instance string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", func(w http.ResponseWriter, _ *http.Request) {
		_ = writeJSON(w, http.StatusOK, api.VersionInfo{Version: Version}) // not the host's groups
	})
	mux.HandleFunc("GET /v1/self", func(w http.ResponseWriter, r *http.Request) {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			writeError(w, err)
			return
		}
		info, err := s.describe(r.Context(), a)
		if err != nil {
			writeError(w, err)
			return
		}
		_ = writeJSON(w, http.StatusOK, api.Self{
			Ref:      info.Ref,
			Project:  info.Project,
			Agent:    info.Name,
			Branch:   info.Branch,
			Worktree: info.Worktree,
			IP:       info.IP,
			State:    info.State,
		})
	})
	// The agent's own browser.
	self := func(r *http.Request) (state.Agent, error) { return s.store.AgentByInstance(r.Context(), instance) }
	handle := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				writeError(w, err)
			}
		})
	}
	// Asking its project's chat: the one thing an agent may do that reaches
	// beyond itself, and it only ever reaches its own project's chat.
	handle("POST /v1/self/ask", s.ask(instance))
	// Asking the user for a credential it lacks, which the user answers in the
	// app: the agent is told what happened, and never the value (D95).
	handle("POST /v1/self/credential", s.requestCredential(instance))
	handle("GET /v1/self/browser", s.browser("status", self))
	for _, action := range []string{"start", "stop", "open"} {
		handle("POST /v1/self/browser/"+action, s.browser(action, self))
	}
	// Its own Android emulator.
	handle("GET /v1/self/android", s.android("status", self, "agent"))
	for _, action := range []string{"start", "stop", "install"} {
		handle("POST /v1/self/android/"+action, s.android(action, self, "agent"))
	}
	// Its own media: it can add items, but not delete or export them.
	for _, route := range mediaRoutes {
		if route.action != "export" {
			handle(route.method+" /v1/self/media"+route.path, s.media(route.action, self, "agent"))
		}
	}
	// Its project's memory: it reads all of it, and appends what it found,
	// what it produced and how its task went. Writing a memory down and
	// saying what the project is doing now are not an agent's to do.
	for _, route := range memoryRoutes {
		if route.inAgent {
			handle(route.method+" /v1/self/memory"+route.path, s.memoryHandler(route.action, s.agentMemoryScope(instance)))
		}
	}
	// Nothing else: an agent must not reach projects, other agents or the daemon itself.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_ = writeJSON(w, http.StatusForbidden, api.Error{Error: "not available inside an agent"})
	})
	return mux
}
