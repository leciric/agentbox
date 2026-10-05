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
	"sync/atomic"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/connectors"
	"agentbox/internal/credentials"
	"agentbox/internal/gitrepo"
	"agentbox/internal/hostos"
	"agentbox/internal/image"
	"agentbox/internal/imagecache"
	"agentbox/internal/incus"
	"agentbox/internal/memory"
	"agentbox/internal/omarchy"
	"agentbox/internal/paths"
	"agentbox/internal/pkgcache"
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
	// ReleasesURL is where the nightly channel looks for nightlies; empty is
	// update.DefaultReleasesURL.
	ReleasesURL string
	// PreviewAddr is where the preview proxy listens: empty is
	// defaultPreviewAddr, and "off" turns the proxy off.
	PreviewAddr string
	// ConnectorsLoopback lets connectors reach servers on this machine's
	// loopback, where only tests' fake MCP servers listen
	// (connectors.OAuth.Loopback).
	ConnectorsLoopback bool
	// GitHubAPI is the GitHub API root; empty is github.Client's own, which
	// AGENTBOX_GITHUB_API can move.
	GitHubAPI string
}

type Server struct {
	cfg Config
	// timeline holds a *sync.Mutex per agent ref, taken by its checkpoints
	// and rollbacks (timeline.go).
	timeline sync.Map
	// imageCache is the image cache agents' Docker shares (imagecache.go),
	// and imageCacheUp whether its socket is being served.
	imageCache   *imagecache.Cache
	imageCacheUp atomic.Bool
	store        *state.Store
	events       *broker
	jobs         *jobs
	chat         *chat.Manager // the agents' conversations in the app's Chat tab
	pulls        *pullsCache   // what GitHub said about each repository, served stale
	prWatch      *prWatcher    // the agents' pull requests being watched (prwatch.go)
	// prTell sends an agent a message from the pull request watch, waking it
	// first, and says what that took: tellAgent, or a test's recorder.
	prTell func(ctx context.Context, a state.Agent, text string) (told, error)
	// prLead puts the watch's notice in front of a project's chat: tellLead,
	// or a test's recorder.
	prLead  func(ctx context.Context, project, notice string, act bool)
	files   *filesCache      // each agent's worktree file listing, served briefly stale
	disks   *agentDiskCache  // each agent's machine and worktree sizes, for its info card
	themes  *omarchy.Watcher // the desktop theme this machine is running, if any
	updates updates          // what the daily update check last found
	stop    context.CancelFunc
	incus   *incusWatch // whether Incus answers, and what to do when it doesn't
	disk    *diskWatch  // the disk guard: a floor of free space on every disk AgentBox writes to

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
	// idleAfter schedules a lead's cache card: time.AfterFunc when nil, a test's
	// clock otherwise.
	idleAfter func(d time.Duration, f func()) interface{ Stop() bool }
	// dockerPruneTimeout bounds freeing an agent's Docker space as it stops
	// (dockerprune.go): agent.DockerPruneTimeout, or a test's shorter one.
	dockerPruneTimeout time.Duration

	waiting *waiters // agents waiting for an answer to a question

	// packageCache is the package managers' caches agents share
	// (pkgcache.go), and packageCacheKick asks for a trim now.
	packageCache     *pkgcache.Cache
	packageCacheKick chan struct{}

	// connectors are the remote MCP servers agents use, signed in to here
	// (connectors.go, internal/connectors).
	connectors *connectors.Service

	// firstSweeps is done once the sweeps Run starts have each made their
	// first pass, the one at startup: what a test waits for, so that pass
	// can't land in the middle of what it is checking.
	firstSweeps sync.WaitGroup

	// bgChecks tracks the background Claude account checks refreshClaudeTokens
	// starts, so Run doesn't return - and a test doesn't tear its temp dir down
	// - while one is still about to write its answer beside the token.
	bgChecks sync.WaitGroup

	mu           sync.Mutex
	agentAPIs    map[string]*http.Server // in-agent API servers, by instance
	leadAPIs     map[string]*http.Server // per-project lead API servers
	lastStates   map[string]api.AgentChange
	previewAddr  string                   // where the preview proxy listens; empty when it's off
	previewIPs   map[string]previewTarget // agents' addresses, cached for the preview proxy
	claudeLogins map[string]*claudeLogin  // in-app Claude Code logins, by job
	distilling   map[string]bool          // projects with a distillation running, by name
	leadCaches   map[string]*leadCache    // leads' prompt caches and their cards, by project (cachecard.go)
	snaps        snapStore                // SnapShots waiting for the app's composer (snaps.go)
	leadWaits    map[string]bool          // agents their project's chat asked for something and hasn't heard back from, by ref (D87)
	baseSyncErrs map[string]string        // why each project's last base sync failed, by project, so a remote that stays down is logged once (basesync.go)
	image        imageWork                // what the daemon is doing to the base image (imagetools.go)
	remote       *remote.Connector        // the connection to a hub, when this machine is an environment
	lan          *lanState                // phones chatting from the local network or a tunnel (lan.go)
	// connectorsGiven is whether each connector was last given to agents,
	// by scope and name, so only a change to that rewrites their MCP
	// servers (connectors.go).
	connectorsGiven map[string]bool
	remoteStop      context.CancelFunc
	// openCodeModels is the state of the background ask that fills OpenCode's
	// model menu: whether one is running, and when the last one started.
	openCodeModels struct {
		running bool
		last    time.Time
	}

	// loginCallbackUnreachable is whether the browser can't reach this
	// machine's localhost, where a Claude Code login's callback listens: in
	// the VM a Linux host runs AgentBox in, nothing forwards it (setup.go).
	loginCallbackUnreachable bool

	terminalMu       sync.Mutex
	terminalActivity map[string]time.Time // last input typed into a terminal, by ref (autostopidle.go)

	// The agent queue (queue.go). queueMu makes one pass of it at a time;
	// startingQueued, under mu, are the queued agents handed to a create job
	// that hasn't ended. queueStart, slotBudget and projectPeak are
	// startQueuedAgent and the manager's, or a test's.
	queueKick      chan struct{}
	queueEvery     time.Duration // queueInterval; 0 runs no loop
	queueMu        sync.Mutex
	startingQueued map[string]bool
	queueStart     func(ctx context.Context, q state.QueuedAgent) error
	slotBudget     func(ctx context.Context) (int64, error)
	projectPeak    func(ctx context.Context, project string) (int64, bool, error)
	// projectShape is the manager's ProjectShape, or a test's: admission's
	// baselines and the burst pool's bursts (burst.go).
	projectShape func(ctx context.Context, project string) (agent.Shape, error)
	// memAvailable is agent.MemAvailable, or a test's: Admit's sanity bound,
	// the VM's real spare memory regardless of what admission's bookkeeping
	// adds up to.
	memAvailable func() int64
	burst        *burstPool
	// usageNow is what each agent used when last sampled, by ref, under mu.
	usageNow map[string]agent.AgentUsage
	// Admission (admission.go), under mu: the creates admitted straight to
	// their machines whose jobs haven't ended, by a number of their own, and
	// why each queued agent waits, by ref. setMemoryHigh is
	// agent.SetMemoryHigh, or a test's.
	pendingCreates map[int]pendingCreate
	pendingSeq     int
	waitReasons    map[string]string
	setMemoryHigh  func(instance string, high int64) error
	// Stopped agents told something (wake.go): waking, under mu, are those
	// whose machines wait for memory, with messages held for them, by ref;
	// wakeMu makes one start of a stopped machine for a message at a time.
	waking map[string]*wakingAgent
	wakeMu sync.Mutex
	// The lead recheck (leadrecheck.go), under mu: when each project's lead
	// was last rechecked, and what it was told then, so the same state isn't
	// sent twice.
	recheckedAt   map[string]time.Time
	recheckedWhat map[string]string
	// recheckTell wakes a lead with its recheck: tellLead acting, or a
	// test's recorder.
	recheckTell func(ctx context.Context, project, note string)
	// The stall watch (stallwatch.go): each running turn's CPU readings, by
	// ref and under mu; how a machine's CPU time is read; and how the lead
	// is told of a stall — tellLead acting, or a test's recorder.
	stalls    map[string]*stallTrack
	cpuTime   func(instance string) (time.Duration, bool)
	stallTell func(ctx context.Context, project, note string)
}

func New(cfg Config) (*Server, error) {
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	if cfg.Incus.Health == nil {
		cfg.Incus.Health = new(incus.Health) // incuswatch.go
	}
	store, err := state.Open(cfg.Paths.StateDB())
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:              cfg,
		store:            store,
		events:           newBroker(),
		agentAPIs:        map[string]*http.Server{},
		leadAPIs:         map[string]*http.Server{},
		waiting:          newWaiters(),
		lastStates:       map[string]api.AgentChange{},
		previewIPs:       map[string]previewTarget{},
		claudeLogins:     map[string]*claudeLogin{},
		distilling:       map[string]bool{},
		leadWaits:        map[string]bool{},
		baseSyncErrs:     map[string]string{},
		pulls:            newPullsCache(),
		prWatch:          newPRWatcher(),
		files:            newFilesCache(),
		updates:          updates{now: make(chan struct{}, 1)},
		terminalActivity: map[string]time.Time{},
		lan:              newLANState(),
		queueKick:        make(chan struct{}, 1),
		queueEvery:       queueInterval,
		startingQueued:   map[string]bool{},
		pendingCreates:   map[int]pendingCreate{},
		waitReasons:      map[string]string{},
		waking:           map[string]*wakingAgent{},
		setMemoryHigh:    agent.SetMemoryHigh,
		recheckedAt:      map[string]time.Time{},
		recheckedWhat:    map[string]string{},
		stalls:           map[string]*stallTrack{},
		cpuTime:          agent.CPUTime,
		connectorsGiven:  map[string]bool{},
	}
	// Lima on a Mac and WSL2 forward the VM's localhost ports on their own.
	s.loginCallbackUnreachable = hostos.OS() == hostos.Linux
	s.queueStart = s.startQueuedAgent
	s.recheckTell = func(ctx context.Context, project, note string) { s.tellLead(ctx, project, note, true) }
	s.stallTell = s.recheckTell
	s.slotBudget = func(context.Context) (int64, error) { return agent.SlotBudget(), nil }
	s.projectPeak = func(ctx context.Context, project string) (int64, bool, error) {
		return s.manager(nil).ProjectPeak(ctx, project)
	}
	s.projectShape = func(ctx context.Context, project string) (agent.Shape, error) {
		return s.manager(nil).ProjectShape(ctx, project)
	}
	s.memAvailable = agent.MemAvailable
	s.burst = newBurstPool()
	s.connectors = s.newConnectors()
	s.disks = newAgentDiskCache(func(ctx context.Context, a state.Agent) agent.AgentDisk { return s.manager(nil).AgentDisk(ctx, a) })
	s.prTell, s.prLead = s.prTellAgent, s.tellLead
	s.chat = &chat.Manager{
		Store:   store,
		Launch:  s.launchChat,
		Prepare: s.prepareChatModel,
		Publish: func(ev api.ChatEvent) {
			s.events.publish(api.EventChat, ev)
			s.captureLeadTurn(ev)
		},
		Finished:   s.agentFinished,
		LeadIdle:   s.leadCacheIdle,
		Idle:       s.leadIdle,
		TurnEnded:  s.checkpointTurn,
		AuthFailed: s.claudeAuthFailed,
		Lost:       s.agentLost,
		Limits:     s.claudeLimited,
		Logf:       s.logf,
		Version:    Version,
		ImageDir:   cfg.Paths.ChatImages,
	}
	s.askLead, s.askAside = s.askLeadSession, s.askAsideSession
	s.incus = s.newIncusWatch()
	s.disk = s.newDiskWatch()
	s.imageCache = s.newImageCache()
	s.packageCache = s.newPackageCache()
	s.packageCacheKick = make(chan struct{}, 1)
	s.dockerPruneTimeout = agent.DockerPruneTimeout
	return s, nil
}

// Run serves the API until ctx ends or a client asks the daemon to stop.
func (s *Server) Run(ctx context.Context) error {
	socket := s.cfg.Paths.Socket()
	if err := checkSocketPaths(socket, s.agentSocketPath("any")); err != nil {
		return err
	}
	if err := CheckDataDir(s.cfg.Paths.Data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	// Taken first, so it is let go last: after the socket is removed and the
	// store closed, when the next daemon can have both.
	unlock, err := lockDaemon(socket, StopWait)
	if err != nil {
		return err
	}
	defer unlock()

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	s.stop = stop
	s.jobs = newJobs(ctx, s.store, s.events)
	defer func() { _ = s.store.Close() }()
	defer s.chat.Close() // before the store closes: it stores what the sessions haven't

	if err := claimSocket(socket); err != nil {
		return err
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	// Closing it would unlink the path whoever's socket it is by then.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	defer removeOwnSocket(socket)()
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}

	s.reconcile(ctx)
	s.servePreview(ctx)
	s.serveImageCache(ctx)
	s.applyLAN(ctx)
	defer s.closeLAN()
	s.watchTheme(ctx)
	// Run's own loops end with it, and it waits for them: deferred after the
	// store's Close, this runs before it, so none of them outlives the
	// database or the daemon it reports on. stop first, for a Serve that
	// failed rather than being stopped.
	var loops sync.WaitGroup
	defer func() {
		stop()
		loops.Wait()
		s.bgChecks.Wait()
	}()
	s.firstSweeps.Add(3)
	loops.Go(func() { s.watch(ctx) })
	loops.Go(func() { s.sweepMedia(ctx) })
	loops.Go(func() { s.sweepFinishedAgents(ctx) })
	loops.Go(func() { s.sweepMemories(ctx) })
	loops.Go(func() { s.sweepIdleAgents(ctx) })
	loops.Go(func() { s.runQueue(ctx) })
	loops.Go(func() { s.watchUpdates(ctx) })
	loops.Go(func() { s.watchStalls(ctx) })
	loops.Go(func() { s.watchPullRequests(ctx) })
	loops.Go(func() { s.syncBases(ctx) })
	// Incus is asked only from here on: nothing before Serve may wait on it.
	loops.Go(func() { s.watchIncus(ctx) })
	loops.Go(func() { s.dropOldLimits(ctx) })
	loops.Go(func() { s.watchDisk(ctx) })
	loops.Go(func() { s.watchPackageCache(ctx) })
	loops.Go(func() { s.refreshConnectors(ctx) })
	s.runCtx = ctx
	s.startRemote(ctx)
	// A new AgentBox may pin newer agent tools than the base image has: they
	// are moved on in the background, while agents go on being made from it.
	go s.updateBaseTools(ctx)
	// Media made on the VM's own disk goes to the host's share, where the app
	// can open it; it is served from where it was until it has moved.
	loops.Go(func() {
		if n, err := s.manager(io.Discard).MoveMedia(); err != nil {
			s.logf("moving media to %s: %v (%d item(s) moved)", s.cfg.Paths.Media(), err, n)
		} else if n > 0 {
			s.logf("moved %d media item(s) to %s", n, s.cfg.Paths.Media())
		}
	})

	srv := &http.Server{Handler: s.routes(), BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		s.closeAgentAPIs()
		s.closeLeadAPIs()
		s.connectors.Close()
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

// removeOwnSocket returns a func that removes socket as the daemon stops, if
// it is still the one it listened on: a daemon without the lock (an older
// AgentBox) may have replaced it with its own by then. The same inode isn't
// enough to say so, since a file system hands a freed one straight out again;
// the time it was made is what tells two sockets apart.
func removeOwnSocket(socket string) func() {
	mine, err := os.Stat(socket)
	return func() {
		now, nowErr := os.Stat(socket)
		if err == nil && nowErr == nil && os.SameFile(mine, now) && mine.ModTime().Equal(now.ModTime()) {
			_ = os.Remove(socket)
		}
	}
}

// claimSocket refuses to start a second daemon and removes a stale socket file.
func claimSocket(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = conn.Close()
		return fmt.Errorf("another AgentBox daemon is already listening on %s", path)
	}
	return os.Remove(path)
}

// reconcile brings state left by a previous daemon, or by an older AgentBox,
// up to date.
func (s *Server) reconcile(ctx context.Context) {
	if n, err := s.store.FailRunningJobs(ctx, "the daemon stopped while this job was running", time.Now()); err != nil {
		s.logf("reconcile jobs: %v", err)
	} else if n > 0 {
		s.logf("marked %d interrupted job(s) as failed", n)
	}
	// A credential request waits on its agent's call, and no call outlives
	// the daemon it was made to (D95).
	s.cancelCredentialRequests(ctx, "", "", "AgentBox restarted while it waited, which ended the agent's call. The agent asks again if it still needs it.")
	if err := s.serveLeadAPI(state.HomeProject); err != nil {
		s.logf("Home chat API socket: %v", err)
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
	// Nothing here asks Incus, which may not answer (incuswatch.go): the
	// agents' side of their in-agent API is plugAgentSockets', once it does.
	m := s.manager(s.cfg.Log)
	for _, a := range agents {
		if err := m.LockAgentWorktree(ctx, a); err != nil {
			s.logf("lock worktree for %s: %v", a.Ref(), err)
		}
		if a.IsLead() {
			continue // no machine, so no in-agent API and nothing to bring up
		}
		if err := s.serveAgentAPI(a.Instance); err != nil {
			s.logf("in-agent API socket for %s: %v", a.Ref(), err)
		}
	}
}

func (s *Server) manager(log io.Writer) *agent.Manager {
	return &agent.Manager{
		Store:            s.store,
		Incus:            s.cfg.Incus,
		Paths:            s.cfg.Paths,
		Creds:            credentials.Store{Dir: s.cfg.Paths.Credentials()},
		Secrets:          s.secrets(),
		User:             s.cfg.User,
		Log:              log,
		AgentSocket:      s.agentSocketPath,
		Binary:           s.cfg.Binary,
		BrowserSocket:    s.browserSocketPath,
		AndroidSDK:       findAndroidSDK,
		LeadSocketPath:   s.leadSocketPath,
		DesktopTheme:     s.desktopTheme,
		ImageCacheSocket: s.imageCacheSocket,
		PackageCacheDir:  s.packageCacheDir,
	}
}

// secrets is the store of keys and tokens the user hands to agents, sealed
// under the machine's secrets key.
func (s *Server) secrets() secrets.Store {
	return secrets.Store{State: s.store, KeyPath: s.cfg.Paths.SecretsKey()}
}

func (s *Server) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.cfg.Log, time.Now().Format(time.DateTime)+" "+format+"\n", args...)
}

// projectBySlug lets every request name a project by what it is called as
// well as by its slug, the one the handlers all work with: a {project} in its
// path, or a ?project= in its query, that is a project's display name is
// turned into that project's slug before the handler reads it. Anything that
// names no project is left as it is, for the handler to refuse or, for a
// project that has gone, to go on reading by (the token ledger's rows outlive
// their project).
func (s *Server) projectBySlug(r *http.Request) {
	slug := func(ref string) string {
		if ref == "" || ref == state.HomeProject {
			return ref
		}
		if p, err := s.store.Project(r.Context(), ref); err == nil {
			return p.Name
		}
		return ref
	}
	if ref := r.PathValue("project"); ref != "" {
		r.SetPathValue("project", slug(ref))
	}
	if q := r.URL.Query(); q.Has("project") {
		if ref := q.Get("project"); slug(ref) != ref {
			q.Set("project", slug(ref))
			r.URL.RawQuery = q.Encode()
		}
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			s.projectBySlug(r)
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
	h("GET /v1/update/release", s.getLatestRelease)
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
	h("GET /v1/projects/{project}/chat/cache", s.chatCache)
	h("POST /v1/projects/{project}/chat/cache", s.chatCacheChoice)
	h("POST /v1/projects/{project}/chat/permissions/{item}", s.answerChat(s.leadFromPath))
	h("PUT /v1/projects/{project}/chat/options/{option}", s.setChatOption(s.leadFromPath))
	h("GET /v1/projects/{project}/files", s.listFiles(s.leadFromPath))
	h("GET /v1/projects/{project}/lead", s.projectChat)
	h("DELETE /v1/projects/{project}/lead", s.resetProjectChat)
	h("GET /v1/projects/{project}/fleet", s.fleet)
	h("GET /v1/projects/{project}/agent-events", s.projectAgentEvents)
	h("GET /v1/projects/{project}/questions", s.projectQuestions)
	h("POST /v1/projects/{project}/questions/{id}/answer", s.answerAsUser)
	h("POST /v1/projects/{project}/questions/{id}/credential", s.answerCredential)
	h("POST /v1/projects/{project}/retire", s.retire)
	h("GET /v1/projects/{project}/media", s.projectMedia)
	h("POST /v1/projects/{project}/media/delete", s.deleteProjectMedia)
	h("GET /v1/projects/{project}/pulls", s.projectPullRequests)
	h("POST /v1/projects/{project}/pulls/{number}/merge", s.mergePullRequest)
	h("GET /v1/projects/{project}/secrets", s.listProjectSecrets)
	h("PUT /v1/projects/{project}/secrets/{name}", s.setProjectSecret)
	h("DELETE /v1/projects/{project}/secrets/{name}", s.removeProjectSecret)
	h("GET /v1/projects/{project}/browser-cookies", s.getBrowserCookies)
	h("POST /v1/projects/{project}/browser-cookies/preview", s.previewBrowserCookies)
	h("PUT /v1/projects/{project}/browser-cookies", s.importBrowserCookies)
	h("DELETE /v1/projects/{project}/browser-cookies", s.removeBrowserCookies)
	for _, route := range memoryRoutes {
		h(route.method+" /v1/projects/{project}/memory"+route.path, s.memoryHandler(route.action, s.projectMemoryScope))
	}
	h("GET /v1/projects/{project}/base", s.getBase)
	h("POST /v1/projects/{project}/base", s.saveBase)
	h("DELETE /v1/projects/{project}/base", s.removeBase)
	h("POST /v1/projects/{project}/base/revert", s.revertBase)

	h("GET /v1/queue", s.getQueue)
	h("POST /v1/queue/{project}/{agent}/move", s.moveQueued)
	h("POST /v1/queue/{project}/{agent}/start", s.startQueuedNow)
	h("DELETE /v1/queue/{project}/{agent}", s.removeQueued)
	h("GET /v1/agents", s.listAgents)
	h("POST /v1/agents", s.createAgent)
	h("POST /v1/agents/stop", s.stopAgents)
	h("GET /v1/agents/{project}/{agent}", s.getAgent)
	h("PATCH /v1/agents/{project}/{agent}", s.updateAgent)
	h("DELETE /v1/agents/{project}/{agent}", s.destroyAgent)
	for _, action := range []string{"start", "stop", "pause", "resume", "session"} {
		h("POST /v1/agents/{project}/{agent}/"+action, s.agentAction(action))
	}
	h("GET /v1/agents/{project}/{agent}/diff", s.diff)
	h("GET /v1/agents/{project}/{agent}/disk", s.agentDisk)
	h("GET /v1/agents/{project}/{agent}/files", s.listFiles(s.agentFromPath))
	h("GET /v1/agents/{project}/{agent}/snapshots", s.listSnapshots)
	h("POST /v1/agents/{project}/{agent}/snapshots", s.takeSnapshot)
	h("DELETE /v1/agents/{project}/{agent}/snapshots/{name}", s.deleteSnapshot)
	h("POST /v1/agents/{project}/{agent}/restore", s.restore)
	h("POST /v1/agents/{project}/{agent}/fork", s.fork)
	h("GET /v1/agents/{project}/{agent}/checkpoints", s.listCheckpoints)
	h("POST /v1/agents/{project}/{agent}/rollback", s.rollback)
	h("POST /v1/agents/{project}/{agent}/recreate", s.recreate)
	h("POST /v1/migration/check", s.checkMigration)
	s.connectorRoutes(h)
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
	h("GET /v1/usage/disk", s.diskUsage)
	h("GET /v1/disk", s.getDiskGuard)
	h("GET /v1/usage/memory", s.memoryUsage)
	h("GET /v1/usage/cpu", s.cpuUsage)
	h("POST /v1/usage-stats/{feature}", s.countAppFeature)
	h("POST /v1/reports/draft", s.reportDraft)
	h("POST /v1/reports", s.sendReport)
	h("GET /v1/image", s.imageStatus)
	h("POST /v1/image/build", s.buildImage)
	h("GET /v1/auth", s.authStatus)
	h("POST /v1/auth/claude", s.saveClaudeToken)
	h("POST /v1/auth/claude/login", s.startClaudeLogin)
	h("GET /v1/auth/claude/login/{job}", s.claudeLoginStatus)
	h("POST /v1/auth/claude/login/{job}/code", s.claudeLoginCode)
	h("DELETE /v1/auth/claude/{account}", s.removeClaudeAccount)
	h("POST /v1/auth/claude/{account}/default", s.setDefaultClaudeAccount)
	h("POST /v1/auth/claude/{account}/rename", s.renameClaudeAccount)
	h("POST /v1/auth/github", s.saveGitHubToken)
	h("DELETE /v1/auth/github/{account}", s.removeGitHubAccount)
	h("POST /v1/auth/github/{account}/default", s.setDefaultGitHubAccount)
	h("POST /v1/auth/github/{account}/rename", s.renameGitHubAccount)
	h("GET /v1/setup", s.setup)
	h("GET /v1/remote", s.getRemote)
	h("PUT /v1/remote", s.connectRemote)
	h("DELETE /v1/remote", s.disconnectRemote)
	// Chatting from a phone on the local network (lan.go).
	h("GET /v1/lan", s.getLAN)
	h("PATCH /v1/lan", s.updateLAN)
	h("POST /v1/lan/pairings", s.addLANPairing)
	h("DELETE /v1/lan/phones/{id}", s.removeLANPhone)
	h("PUT /v1/lan/host", s.lanHostReport)
	h("PUT /v1/lan/web/{version}/files/{path...}", s.putLANWebFile)
	h("POST /v1/lan/web/{version}", s.installLANWeb)
	mux.Handle(lanNetPrefix+"/", http.StripPrefix(lanNetPrefix, s.lanHandler(lanViaSocket)))

	h("POST /v1/snaps", s.takeSnap)
	h("GET /v1/snaps", s.listSnaps)
	h("GET /v1/snaps/{id}/image", s.snapImage)
	h("POST /v1/snaps/{id}/send", s.sendSnap)
	h("DELETE /v1/snaps/{id}", s.dropSnap)
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
	case errors.Is(err, incus.ErrNotAnswering):
		status = http.StatusServiceUnavailable
	case isDiskFull(err):
		status = http.StatusInsufficientStorage
	}
	body := api.Error{Error: err.Error()}
	if errors.Is(err, gitrepo.ErrNotEmpty) {
		body.Code = api.ErrorFolderNotEmpty
	}
	_ = writeJSON(w, status, body)
}

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}
