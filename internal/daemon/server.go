// Package daemon is the long-running AgentBox control plane. It owns every
// agent operation, runs the slow ones as jobs, and serves the HTTP API from
// package api on a unix socket, plus one scoped socket per agent for the
// in-agent API.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/memory"
	"agentbox/internal/omarchy"
	"agentbox/internal/paths"
	"agentbox/internal/remote"
	"agentbox/internal/secrets"
	"agentbox/internal/state"
)

var Version = "dev"

type Config struct {
	Paths  paths.Paths
	Incus  incus.Client
	User   image.User
	Binary string    // copied into agents for the in-agent API; empty skips the copy
	Log    io.Writer // the daemon's own log; nil discards it
	// UpdateURL is where the daily update check asks; empty is
	// update.DefaultURL.
	UpdateURL string
}

type Server struct {
	cfg     Config
	store   *state.Store
	events  *broker
	jobs    *jobs
	chat    *chat.Manager    // the agents' conversations in the app's Chat tab
	pulls   *pullsCache      // what GitHub said about each repository, served stale
	files   *filesCache      // each agent's worktree file listing, served briefly stale
	themes  *omarchy.Watcher // the desktop theme this machine is running, if any
	updates updates          // what the daily update check last found
	stop    context.CancelFunc

	runCtx context.Context // Run's, for connections that outlive a request

	// askLead runs a hidden prompt on a project chat's session and answers
	// with what it said: the one thing a distillation (D76) needs that isn't
	// a SQL statement. It is a field rather than a call so a test can put a
	// predictable model behind it; New points it at the real session.
	askLead func(ctx context.Context, a state.Agent, ask string) (string, error)
	// askAside is the same prompt in a session of its own, on the model the
	// project chose to consolidate with (D78), answering also with the model
	// that really ran. askLead is what it falls back to. A field for the same
	// reason, and for one more: the real one starts an AI tool, which a test
	// must never do by forgetting to say otherwise.
	askAside func(ctx context.Context, a state.Agent, model, ask string) (answer, ranOn string, err error)

	waiting *waiters // agents waiting for an answer to a question

	mu           sync.Mutex
	agentAPIs    map[string]*http.Server // in-agent API servers, by instance
	leadAPIs     map[string]*http.Server // per-project lead API servers
	lastStates   map[string]api.AgentChange
	previewAddr  string                   // where the preview proxy listens; empty when it's off
	previewIPs   map[string]previewTarget // agents' addresses, cached for the preview proxy
	claudeLogins map[string]*claudeLogin  // in-app Claude Code logins, by job
	distilling   map[string]bool          // projects with a distillation running, by name
	leadWaits    map[string]bool          // agents their project's chat asked for something and hasn't heard back from, by ref (D87)
	remote       *remote.Connector        // the connection to a hub, when this machine is an environment
	remoteStop   context.CancelFunc
	// openCodeModels is the state of the background ask that fills OpenCode's
	// model menu: whether one is running, and when the last one started.
	openCodeModels struct {
		running bool
		last    time.Time
	}
}

func New(cfg Config) (*Server, error) {
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	store, err := state.Open(cfg.Paths.StateDB())
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:          cfg,
		store:        store,
		events:       newBroker(),
		agentAPIs:    map[string]*http.Server{},
		leadAPIs:     map[string]*http.Server{},
		waiting:      newWaiters(),
		lastStates:   map[string]api.AgentChange{},
		previewIPs:   map[string]previewTarget{},
		claudeLogins: map[string]*claudeLogin{},
		distilling:   map[string]bool{},
		leadWaits:    map[string]bool{},
		pulls:        newPullsCache(),
		files:        newFilesCache(),
		updates:      updates{now: make(chan struct{}, 1)},
	}
	s.chat = &chat.Manager{
		Store:   store,
		Launch:  s.launchChat,
		Prepare: s.prepareChatModel,
		Publish: func(ev api.ChatEvent) {
			s.events.publish(api.EventChat, ev)
			s.captureLeadTurn(ev)
		},
		Finished:   s.agentFinished,
		AuthFailed: s.claudeAuthFailed,
		Limits:     s.claudeLimited,
		Logf:       s.logf,
		Version:    Version,
		ImageDir:   cfg.Paths.ChatImages,
	}
	s.askLead, s.askAside = s.askLeadSession, s.askAsideSession
	return s, nil
}

// Run serves the API until ctx ends or a client asks the daemon to stop.
func (s *Server) Run(ctx context.Context) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	s.stop = stop
	s.jobs = newJobs(ctx, s.store, s.events)
	defer s.store.Close()
	defer s.chat.Close() // before the store closes: it stores what the sessions haven't

	socket := s.cfg.Paths.Socket()
	if err := checkSocketPaths(socket, s.agentSocketPath("any")); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	if err := claimSocket(socket); err != nil {
		return err
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}

	s.reconcile(ctx)
	s.servePreview(ctx)
	s.watchTheme(ctx)
	go s.watch(ctx)
	go s.sweepMedia(ctx)
	go s.sweepMemories(ctx)
	go s.watchUpdates(ctx)
	s.runCtx = ctx
	s.startRemote(ctx)

	srv := &http.Server{Handler: s.routes(), BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
		s.closeAgentAPIs()
		s.closeLeadAPIs()
	}()
	s.logf("AgentBox daemon %s listening on %s", Version, socket)
	err = srv.Serve(ln)
	s.jobs.wait()
	s.logf("stopped")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// maxSocketPath is the longest path a unix socket can have on Linux: 108 bytes,
// including the terminating NUL.
const maxSocketPath = 107

// checkSocketPaths fails early and clearly when a socket path is too long;
// otherwise listening fails with "invalid argument".
func checkSocketPaths(paths ...string) error {
	for _, p := range paths {
		if len(p) > maxSocketPath {
			return fmt.Errorf("the socket path %s is %d bytes, longer than unix sockets allow (%d): use a shorter XDG_DATA_HOME", p, len(p), maxSocketPath)
		}
	}
	return nil
}

// claimSocket refuses to start a second daemon and removes a stale socket file.
func claimSocket(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		conn.Close()
		return fmt.Errorf("another AgentBox daemon is already listening on %s", path)
	}
	return os.Remove(path)
}

// reconcile brings state left by a previous daemon, or by an older AgentBox,
// up to date.
func (s *Server) reconcile(ctx context.Context) {
	if err := s.seedResourceDefaults(ctx); err != nil {
		s.logf("reconcile resource defaults: %v", err)
	}
	if n, err := s.store.FailRunningJobs(ctx, "the daemon stopped while this job was running", time.Now()); err != nil {
		s.logf("reconcile jobs: %v", err)
	} else if n > 0 {
		s.logf("marked %d interrupted job(s) as failed", n)
	}
	if projects, err := s.store.Projects(ctx); err == nil {
		for _, p := range projects {
			if err := s.serveLeadAPI(p.Name); err != nil {
				s.logf("lead API socket for %s: %v", p.Name, err)
			}
		}
	}
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		s.logf("reconcile agents: %v", err)
		return
	}
	m := s.manager(s.cfg.Log)
	for _, a := range agents {
		if a.IsLead() {
			continue // no machine, so no in-agent API and nothing to bring up
		}
		if err := s.serveAgentAPI(a.Instance); err != nil {
			s.logf("in-agent API socket for %s: %v", a.Ref(), err)
		}
		if a.Status != state.AgentReady {
			continue
		}
		if err := m.EnsureAgentAPI(ctx, a); err != nil && !errors.Is(err, incus.ErrNotFound) {
			s.logf("in-agent API for %s: %v", a.Ref(), err)
		}
	}
}

func (s *Server) manager(log io.Writer) *agent.Manager {
	return &agent.Manager{
		Store:          s.store,
		Incus:          s.cfg.Incus,
		Paths:          s.cfg.Paths,
		Creds:          credentials.Store{Dir: s.cfg.Paths.Credentials()},
		Secrets:        s.secrets(),
		User:           s.cfg.User,
		Log:            log,
		AgentSocket:    s.agentSocketPath,
		Binary:         s.cfg.Binary,
		BrowserSocket:  s.browserSocketPath,
		AndroidSDK:     findAndroidSDK,
		LeadSocketPath: s.leadSocketPath,
		DesktopTheme:   s.desktopTheme,
	}
}

// secrets is the store of keys and tokens the user hands to agents, sealed
// under the machine's secrets key.
func (s *Server) secrets() secrets.Store {
	return secrets.Store{State: s.store, KeyPath: s.cfg.Paths.SecretsKey()}
}

func (s *Server) logf(format string, args ...any) {
	fmt.Fprintf(s.cfg.Log, time.Now().Format(time.DateTime)+" "+format+"\n", args...)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				writeError(w, err)
			}
		})
	}
	h("GET /v1/version", s.version)
	h("POST /v1/shutdown", s.shutdown)

	h("GET /v1/theme", s.themeStatus)
	h("PATCH /v1/theme", s.updateTheme)
	h("GET /v1/update", s.getUpdate)
	h("GET /v1/settings", s.settings)
	h("PATCH /v1/settings", s.updateSettings)
	// What every chat spent, kept after its agent is gone (D83).
	h("GET /v1/tokens", s.tokenReport)
	h("GET /v1/tokens/turns", s.tokenTurns)
	// What Anthropic last said about each Claude account's usage limits (D85).
	h("GET /v1/limits", s.claudeLimits)

	h("GET /v1/projects", s.listProjects)
	h("POST /v1/projects", s.addProject)
	// How the sidebar is organised (D79): the sections, and one layout that
	// carries a whole new order rather than a move to infer the rest from.
	h("GET /v1/sections", s.listSections)
	h("POST /v1/sections", s.addSection)
	h("PATCH /v1/sections/{id}", s.updateSection)
	h("DELETE /v1/sections/{id}", s.removeSection)
	h("PUT /v1/projects/layout", s.setProjectLayout)
	h("GET /v1/projects/{project}", s.getProject)
	h("PATCH /v1/projects/{project}", s.updateProject)
	h("DELETE /v1/projects/{project}", s.removeProject)
	h("GET /v1/projects/{project}/brief", s.brief)
	h("GET /v1/projects/{project}/notes", s.getNotes)
	h("PUT /v1/projects/{project}/notes", s.setNotes)
	h("GET /v1/projects/{project}/chat", s.getChat(s.leadFromPath))
	h("DELETE /v1/projects/{project}/chat", s.clearChat(s.leadFromPath))
	h("POST /v1/projects/{project}/chat/start", s.startChat(s.ensureLeadFromPath))
	h("POST /v1/projects/{project}/chat/messages", s.sendChat(s.ensureLeadFromPath))
	h("GET /v1/projects/{project}/chat/images/{image}", s.chatImage(s.leadFromPath))
	h("POST /v1/projects/{project}/chat/cancel", s.cancelChat(s.leadFromPath))
	h("POST /v1/projects/{project}/chat/rollover", s.rolloverChat)
	h("POST /v1/projects/{project}/chat/permissions/{item}", s.answerChat(s.leadFromPath))
	h("PUT /v1/projects/{project}/chat/options/{option}", s.setChatOption(s.leadFromPath))
	h("GET /v1/projects/{project}/files", s.listFiles(s.leadFromPath))
	h("GET /v1/projects/{project}/lead", s.projectChat)
	h("DELETE /v1/projects/{project}/lead", s.resetProjectChat)
	h("GET /v1/projects/{project}/fleet", s.fleet)
	h("GET /v1/projects/{project}/agent-events", s.projectAgentEvents)
	h("GET /v1/projects/{project}/questions", s.projectQuestions)
	h("POST /v1/projects/{project}/questions/{id}/answer", s.answerAsUser)
	h("POST /v1/projects/{project}/retire", s.retire)
	h("GET /v1/projects/{project}/media", s.projectMedia)
	h("POST /v1/projects/{project}/media/delete", s.deleteProjectMedia)
	h("GET /v1/projects/{project}/pulls", s.projectPullRequests)
	h("POST /v1/projects/{project}/pulls/{number}/merge", s.mergePullRequest)
	h("GET /v1/projects/{project}/secrets", s.listProjectSecrets)
	h("PUT /v1/projects/{project}/secrets/{name}", s.setProjectSecret)
	h("DELETE /v1/projects/{project}/secrets/{name}", s.removeProjectSecret)
	for _, route := range memoryRoutes {
		h(route.method+" /v1/projects/{project}/memory"+route.path, s.memoryHandler(route.action, s.projectMemoryScope))
	}
	h("GET /v1/projects/{project}/base", s.getBase)
	h("POST /v1/projects/{project}/base", s.saveBase)
	h("DELETE /v1/projects/{project}/base", s.removeBase)
	h("POST /v1/projects/{project}/base/revert", s.revertBase)

	h("GET /v1/agents", s.listAgents)
	h("POST /v1/agents", s.createAgent)
	h("GET /v1/agents/{project}/{agent}", s.getAgent)
	h("PATCH /v1/agents/{project}/{agent}", s.updateAgent)
	h("DELETE /v1/agents/{project}/{agent}", s.destroyAgent)
	for _, action := range []string{"start", "stop", "pause", "resume", "session"} {
		h("POST /v1/agents/{project}/{agent}/"+action, s.agentAction(action))
	}
	h("GET /v1/agents/{project}/{agent}/diff", s.diff)
	h("GET /v1/agents/{project}/{agent}/files", s.listFiles(s.agentFromPath))
	h("GET /v1/agents/{project}/{agent}/snapshots", s.listSnapshots)
	h("POST /v1/agents/{project}/{agent}/snapshots", s.takeSnapshot)
	h("DELETE /v1/agents/{project}/{agent}/snapshots/{name}", s.deleteSnapshot)
	h("POST /v1/agents/{project}/{agent}/restore", s.restore)
	h("POST /v1/agents/{project}/{agent}/fork", s.fork)
	h("GET /v1/agents/{project}/{agent}/secrets", s.listAgentSecrets)
	h("PUT /v1/agents/{project}/{agent}/secrets/{name}", s.setAgentSecret)
	h("DELETE /v1/agents/{project}/{agent}/secrets/{name}", s.removeAgentSecret)
	h("GET /v1/agents/{project}/{agent}/terminal", s.terminal)
	h("GET /v1/agents/{project}/{agent}/chat", s.getChat(s.agentFromPath))
	h("DELETE /v1/agents/{project}/{agent}/chat", s.clearChat(s.agentFromPath))
	h("POST /v1/agents/{project}/{agent}/chat/start", s.startChat(s.agentFromPath))
	h("POST /v1/agents/{project}/{agent}/chat/messages", s.sendChat(s.agentFromPath))
	h("GET /v1/agents/{project}/{agent}/chat/images/{image}", s.chatImage(s.agentFromPath))
	h("POST /v1/agents/{project}/{agent}/chat/cancel", s.cancelChat(s.agentFromPath))
	h("POST /v1/agents/{project}/{agent}/chat/permissions/{item}", s.answerChat(s.agentFromPath))
	h("PUT /v1/agents/{project}/{agent}/chat/options/{option}", s.setChatOption(s.agentFromPath))
	h("GET /v1/agents/{project}/{agent}/browser", s.browser("status", s.agentFromPath))
	for _, action := range []string{"start", "stop", "open"} {
		h("POST /v1/agents/{project}/{agent}/browser/"+action, s.browser(action, s.agentFromPath))
	}
	h("GET /v1/agents/{project}/{agent}/browser/view", s.browserView)
	h("GET /v1/agents/{project}/{agent}/android", s.android("status", s.agentFromPath, "user"))
	for _, action := range []string{"start", "stop", "install"} {
		h("POST /v1/agents/{project}/{agent}/android/"+action, s.android(action, s.agentFromPath, "user"))
	}
	h("GET /v1/agents/{project}/{agent}/android/view", s.androidView)
	h("GET /v1/preview", s.previewInfo)
	for _, route := range mediaRoutes {
		h(route.method+" /v1/agents/{project}/{agent}/media"+route.path, s.media(route.action, s.agentFromPath, "user"))
	}
	h("POST /v1/agents/{project}/{agent}/media/delete", s.deleteAgentMedia)
	h("GET /v1/media/{id}", s.mediaItem)
	h("GET /v1/media/{id}/file", s.mediaFile)
	h("DELETE /v1/media/{id}", s.deleteMedia)

	h("GET /v1/usage", s.usage)
	h("GET /v1/image", s.imageStatus)
	h("POST /v1/image/build", s.buildImage)
	h("GET /v1/auth", s.authStatus)
	h("POST /v1/auth/claude", s.saveClaudeToken)
	h("POST /v1/auth/claude/login", s.startClaudeLogin)
	h("GET /v1/auth/claude/login/{job}", s.claudeLoginStatus)
	h("POST /v1/auth/claude/login/{job}/code", s.claudeLoginCode)
	h("DELETE /v1/auth/claude/{account}", s.removeClaudeAccount)
	h("POST /v1/auth/claude/{account}/default", s.setDefaultClaudeAccount)
	h("POST /v1/auth/github", s.saveGitHubToken)
	h("DELETE /v1/auth/github/{account}", s.removeGitHubAccount)
	h("POST /v1/auth/github/{account}/default", s.setDefaultGitHubAccount)
	h("GET /v1/setup", s.setup)
	h("GET /v1/remote", s.getRemote)
	h("PUT /v1/remote", s.connectRemote)
	h("DELETE /v1/remote", s.disconnectRemote)

	h("GET /v1/jobs", s.listJobs)
	h("GET /v1/jobs/{id}", s.getJob)
	h("GET /v1/jobs/{id}/log", s.jobLog)
	h("POST /v1/jobs/{id}/cancel", s.cancelJob)
	h("GET /v1/events", s.eventStream)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, state.ErrNotFound), errors.Is(err, incus.ErrNotFound), errors.Is(err, memory.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, state.ErrExists):
		status = http.StatusConflict
	}
	writeJSON(w, status, api.Error{Error: err.Error()})
}

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}
