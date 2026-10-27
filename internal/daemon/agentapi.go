package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Each agent gets its own socket, exposed inside the agent by an Incus proxy
// device. The daemon knows which agent is calling from the socket a request
// arrives on, so the in-agent API can only ever describe or act on that agent.
//
// An agent whose machine is a pod has no socket to arrive on: it reaches the
// same routes over HTTP, and its bearer token names the agent instead
// (podInAgentRoutes). There is one route set; only how a caller is known
// differs.

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
	return s.inAgentRoutesFor(func(r *http.Request) (state.Agent, error) {
		return s.store.AgentByInstance(r.Context(), instance)
	})
}

// inAgentRoutesFor is the in-agent API for whichever agent agentOf names: the
// one behind the socket a request arrived on, or the one a pod's token named
// (podInAgentRoutes).
func (s *Server) inAgentRoutesFor(agentOf func(*http.Request) (state.Agent, error)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", func(w http.ResponseWriter, _ *http.Request) {
		_ = writeJSON(w, http.StatusOK, api.VersionInfo{Version: Version}) // not the host's groups
	})
	mux.HandleFunc("GET /v1/self", func(w http.ResponseWriter, r *http.Request) {
		a, err := agentOf(r)
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
	// The agent itself, as the socket or the token saying which one it is.
	handle := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				writeError(w, err)
			}
		})
	}
	// Asking its project's chat: the one thing an agent may do that reaches
	// beyond itself, and it only ever reaches its own project's chat.
	handle("POST /v1/self/ask", s.ask(agentOf))
	handle("GET /v1/self/tests", s.testPolicy(agentOf))
	handle("POST /v1/self/heavy", s.startHeavy(agentOf))
	handle("POST /v1/self/heavy/{key}/join", s.joinHeavy(agentOf))
	handle("DELETE /v1/self/heavy/{key}", s.endHeavy(agentOf))
	// Asking the user for a credential it lacks, which the user answers in the
	// app: the agent is told what happened, and never the value (D95).
	handle("POST /v1/self/credential", s.requestCredential(agentOf))
	handle("POST /v1/self/connector", s.requestConnector(agentOf))
	handle("GET /v1/self/browser", s.browser("status", agentOf))
	handle("GET /v1/self/browser/gpu", s.browserGPU(agentOf))
	for _, action := range []string{"start", "stop", "open"} {
		handle("POST /v1/self/browser/"+action, s.browser(action, agentOf))
	}
	// Its own Android emulator.
	handle("GET /v1/self/android", s.android("status", agentOf, "agent"))
	for _, action := range []string{"start", "stop", "install"} {
		handle("POST /v1/self/android/"+action, s.android(action, agentOf, "agent"))
	}
	// The Mac's iOS Simulator, which it drives as the app's view does.
	handle("GET /v1/self/ios", s.ios("status", self))
	for _, action := range iosActions {
		handle("POST /v1/self/ios/"+action, s.ios(action, self))
	}
	// Its own media: it can add and delete items, but not export them.
	for _, route := range mediaRoutes {
		if route.action != "export" {
			handle(route.method+" /v1/self/media"+route.path, s.media(route.action, agentOf, "agent"))
		}
	}
	handle("GET /v1/self/media/{id}/file", s.selfMediaFile(agentOf))
	handle("DELETE /v1/self/media/{id}", s.selfDeleteMedia(agentOf))
	// Its project's memory: it reads all of it, and appends what it found,
	// what it produced and how its task went. Writing a memory down and
	// saying what the project is doing now are not an agent's to do.
	for _, route := range memoryRoutes {
		if route.inAgent {
			handle(route.method+" /v1/self/memory"+route.path, s.memoryHandler(route.action, s.agentMemoryScope(agentOf)))
		}
	}
	// Its connectors: what it is given, and the relay to each, which adds
	// the credentials the agent never holds (connectors.go).
	handle("GET /v1/self/connectors", s.selfConnectors(agentOf))
	for _, method := range []string{"POST", "GET", "DELETE"} {
		mux.HandleFunc(method+" /v1/self/connectors/{name}/mcp", s.selfConnectorMCP(agentOf))
	}
	// Nothing else: an agent must not reach projects, other agents or the daemon itself.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_ = writeJSON(w, http.StatusForbidden, api.Error{Error: "not available inside an agent"})
	})
	return traceAPI("agent", mux, false)
}

// podAgentKey is where podInAgentRoutes leaves the agent a pod's token named,
// for the routes it built to find on the request.
type podAgentKey struct{}

// selfPath reports whether a request's path is the in-agent API: /v1/self and
// everything under it.
func selfPath(path string) bool {
	return path == "/v1/self" || strings.HasPrefix(path, "/v1/self/")
}

// podInAgentRoutes is the in-agent API for an agent whose machine is a pod:
// its bearer token names the agent, since a pod has no socket to arrive on. The
// routes are the socket's own (inAgentRoutesFor), so a pod reaches exactly what
// an agent in an Incus machine does.
func (s *Server) podInAgentRoutes() http.Handler {
	routes := s.inAgentRoutesFor(func(r *http.Request) (state.Agent, error) {
		a, ok := r.Context().Value(podAgentKey{}).(state.Agent)
		if !ok {
			return state.Agent{}, errors.New("this request wasn't authenticated as an agent")
		}
		return a, nil
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := lanBearer(r)
		if !ok {
			_ = writeJSON(w, http.StatusUnauthorized, api.Error{Error: "this request needs the agent's token"})
			return
		}
		project, agent, err := s.store.AgentForToken(r.Context(), token)
		if err != nil {
			_ = writeJSON(w, http.StatusUnauthorized, api.Error{Error: "the agent's token isn't one this daemon issued"})
			return
		}
		// The token names the agent, and the row is read again for what the
		// routes act on: one whose agent has gone is no agent's token at all.
		a, err := s.store.Agent(r.Context(), project, agent)
		if err != nil {
			_ = writeJSON(w, http.StatusUnauthorized, api.Error{Error: "the agent's token isn't one this daemon issued"})
			return
		}
		routes.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), podAgentKey{}, a)))
	})
}

// testPolicy tells the agent's heavy-hook whether a whole test suite is
// refused: while the installation blocks them (SettingBlockFullSuite) and the
// agent's project doesn't allow them (internal/cli/fullsuite.go). It asks
// only when a command is one.
func (s *Server) testPolicy(agentOf func(*http.Request) (state.Agent, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := agentOf(r)
		if err != nil {
			return err
		}
		block, err := s.store.Flag(r.Context(), state.SettingBlockFullSuite)
		if err != nil {
			return err
		}
		if block {
			p, err := s.store.Project(r.Context(), a.Project)
			if err != nil {
				return err
			}
			block = !p.FullTestSuite
		}
		return writeJSON(w, http.StatusOK, api.TestPolicy{BlockFullSuite: block, Base: a.BaseCommit})
	}
}
