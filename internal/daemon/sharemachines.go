package daemon

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/hubapi"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/github"
	"agentbox/internal/image"
	"agentbox/internal/state"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// A paired AgentBox's computer as a machine of a shared project
// (sharepeers.go, shareremote.go): the agents a project's chat makes for the
// peer may run there, on the peer's own AI accounts and GitHub access, and
// count against none of its slots here.
//
//   - The peer's daemon keeps a WebSocket open to the owner's share gateway,
//     /share/machine, with its token, carrying yamux streams as the hub's
//     tunnel does (hubapi.TunnelConfig, remote.Connector): the owner opens a
//     stream for each request and the peer serves it (serveOwner). The owner
//     can't reach the peer otherwise; the peer reaches the owner already.
//   - The peer's composer chooses, for each message, "On <owner>'s PC" or
//     "On my PC" (ChatMessageRequest.RunOn). The owner keeps the choice for
//     the session (peeringState.runOn) while the peer is its last speaker,
//     tells the lead when it changes, and sends the session's create_agent
//     there when the lead names no machine (leadCreateAgent). The lead names
//     it as it names a hub machine: by the peer's name (leadOnMachine).
//   - On the peer, a create clones the project from its repository (the
//     owner says which: /share/run-on) with the peer's GitHub account, and
//     makes the agent as the hub path does (peerCreateAgent), marked as the
//     owner's (state.PeerAgent, OriginID "share:<remote>"). The owner's lead
//     reaches only the agents it made there (shareGuestRoute). A clone whose
//     creates all fail is taken back (claimPeerProject).
//   - A fan_out follows the choice as a create does (leadFanOutOnPeer): the
//     group is made on the peer's computer (shareGuestFanOut) and compared
//     and kept there by its user. Its agents are the owner's lead's, which
//     hears of each as it does of any, and once when they have all finished.
//   - Their notices go back to the owner (sendShareEvent, /share/agent-events),
//     which keeps them as remote agents (state.RemoteAgent, MachineID
//     "share:<peer>") that both sides see in the project's fleet, and tells
//     the lead, whose chat both sides read.

// shareOriginPrefix marks a machine that is a share peer's computer, in a
// remote agent's MachineID on the owner ("share:<peer id>") and in a peer
// agent's OriginID on the peer ("share:<remote id>").
const shareOriginPrefix = "share:"

// shareMachine is a peer's computer connected to this owner.
type shareMachine struct {
	peer    string
	session *yamux.Session
	client  *http.Client
}

func (s *Server) shareMachine(peerID string) *shareMachine {
	sh := s.peering
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return sh.machines[peerID]
}

// shareMachineConnect is a peer's daemon connecting its computer to take
// agents: /share/machine, a WebSocket that lasts as long as it is connected.
func (s *Server) shareMachineConnect(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := s.sharePeerOf(w, r, addr)
	if !ok {
		return
	}
	if !p.OwnMachine {
		writeLANError(w, http.StatusForbidden, s.ownMachineOff(r.Context()))
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has answered
	}
	conn.SetReadLimit(-1)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	session, err := yamux.Client(websocket.NetConn(ctx, conn, websocket.MessageBinary), hubapi.TunnelConfig())
	if err != nil {
		_ = conn.CloseNow()
		return
	}
	defer func() { _ = session.Close() }()
	m := &shareMachine{peer: p.ID, session: session, client: &http.Client{Transport: &http.Transport{
		DialContext:         func(context.Context, string, string) (net.Conn, error) { return session.Open() },
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     time.Minute,
	}}}
	sh := s.peering
	sh.mu.Lock()
	if old := sh.machines[p.ID]; old != nil {
		_ = old.session.Close()
	}
	sh.machines[p.ID] = m
	sh.mu.Unlock()
	_, untrack := sh.track(p.ID, shareOpen{machine: true}, &cancel)
	defer untrack()
	s.logf("share: %s (%s) connected its computer from %s to take agents", p.Name, p.ID, addr)
	s.publishShare(ctx)
	select {
	case <-ctx.Done():
	case <-session.CloseChan():
	}
	sh.mu.Lock()
	if sh.machines[p.ID] == m {
		delete(sh.machines, p.ID)
	}
	sh.mu.Unlock()
	s.logf("share: %s's computer disconnected", p.Name)
	s.publishShare(context.WithoutCancel(ctx))
}

func (s *Server) ownMachineOff(ctx context.Context) string {
	return fmt.Sprintf("%s doesn't let this AgentBox run agents on its own computer: its owner can allow it under Settings → Share", s.shareName(ctx))
}

// shareRunOn answers a peer's /share/run-on?project=: whether it may run that
// project's agents on its own computer, and the repository to clone.
func (s *Server) shareRunOn(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := s.sharePeerOf(w, r, addr)
	if !ok {
		return
	}
	ctx := r.Context()
	slug := r.URL.Query().Get("project")
	if proj, err := s.store.Project(ctx, slug); err == nil {
		slug = proj.Name
	}
	if !p.Shares(slug) {
		writeLANError(w, http.StatusForbidden, fmt.Sprintf("project %s isn't shared with %s", slug, p.Name))
		return
	}
	out := api.ShareOwnerRunOn{Allowed: p.OwnMachine, Connected: s.shareMachine(p.ID) != nil}
	if !p.OwnMachine {
		out.Why = s.ownMachineOff(ctx)
	}
	if repo, err := s.projectRepo(ctx, slug); err != nil {
		out.Allowed, out.Why = false, cmp.Or(out.Why, err.Error())
	} else {
		out.Repo = repo
	}
	_ = writeJSON(w, http.StatusOK, out)
}

// shareAgentEventIn takes a peer's notice about an agent it runs for one of
// this machine's projects: /share/agent-events.
func (s *Server) shareAgentEventIn(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := s.sharePeerOf(w, r, addr)
	if !ok {
		return
	}
	var ev api.ShareAgentEvent
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := readJSON(r, &ev); err != nil {
		writeLANError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	if !p.Shares(ev.Project) {
		writeLANError(w, http.StatusForbidden, fmt.Sprintf("project %s isn't shared with %s", ev.Project, p.Name))
		return
	}
	if ev.Agent == "" {
		writeLANError(w, http.StatusBadRequest, "a notice about no agent")
		return
	}
	if err := s.store.SaveRemoteAgent(ctx, state.RemoteAgent{
		Project: ev.Project, MachineID: shareOriginPrefix + p.ID, MachineName: p.Name, Agent: ev.Agent,
		Title: ev.Title, Branch: ev.Branch, Status: ev.Status, Summary: headWords(ev.Summary, 200), PRURL: ev.PRURL, UpdatedAt: time.Now(),
	}); err != nil {
		writeLANError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.events.publish(api.EventRemoteAgents, api.RemoteAgentsChange{Project: ev.Project})
	if ev.Notice != "" {
		s.tellLeadOf(ctx, ev.Project, remoteNotice(p.Name, ev.Agent, ev.Notice), chat.NoticeOptions{Act: ev.Act})
	}
	w.WriteHeader(http.StatusNoContent)
}

// errNoShareMachine is a lead naming a peer's computer that can't take an
// agent now.
type errNoShareMachine struct{ name, why string }

func (e errNoShareMachine) Error() string {
	return fmt.Sprintf("%s's computer %s, so nothing was sent to it: make the agent here (leave machine out), or tell the user", e.name, e.why)
}

// shareMachineFor is the peer a lead's machine names in project, by the
// peer's name or "share:<id>", and its computer if connected. ok is false
// when the name is no peer's, to be looked for on the hub.
func (s *Server) shareMachineFor(ctx context.Context, project, name string) (p state.SharePeer, m *shareMachine, ok bool, err error) {
	peers, err := s.store.SharePeers(ctx)
	if err != nil {
		return state.SharePeer{}, nil, false, err
	}
	name = strings.TrimSpace(name)
	for _, p := range peers {
		named := strings.EqualFold(p.Name, name) || name == shareOriginPrefix+p.ID || name == p.ID
		if !p.Shares(project) || !named {
			continue
		}
		switch m := s.shareMachine(p.ID); {
		case !p.OwnMachine:
			return p, nil, true, errNoShareMachine{p.Name, "isn't allowed to run agents for this project (Settings → Share)"}
		case m == nil:
			return p, nil, true, errNoShareMachine{p.Name, "isn't connected to this AgentBox now"}
		default:
			return p, m, true, nil
		}
	}
	return state.SharePeer{}, nil, false, nil
}

// leadOnShareMachine is leadOnMachine for a peer's computer: the lead route,
// sent there over the peer's own connection.
func (s *Server) leadOnShareMachine(w http.ResponseWriter, r *http.Request, p state.SharePeer, m *shareMachine, route string) error {
	project := r.PathValue("project")
	repo, err := s.projectRepo(r.Context(), project)
	if err != nil {
		return err
	}
	env := hubapi.HubEnvironment{ID: shareOriginPrefix + p.ID, Name: p.Name}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "http", "agentbox", "agentbox"
			pr.Out.URL.Path, pr.Out.URL.RawPath = route, ""
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Set(headerPeerProject, project)
			pr.Out.Header.Set(headerPeerRepo, repo)
		},
		Transport: m.client.Transport,
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode == http.StatusOK && r.Method == http.MethodPost && route == "/v1/project/retire" {
				s.noteRemoteRetired(resp, project, env)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, fmt.Errorf("couldn't reach %s's computer (%v): it may not have got the request; list_agents with machine says what it has", p.Name, err))
		},
		ErrorLog: log.New(io.Discard, "", 0),
	}
	proxy.ServeHTTP(w, r)
	return nil
}

// shareMachines are, for list_resources machines, the computers of the peers
// project is shared with that may run its agents.
func (s *Server) shareMachines(ctx context.Context, project string) []api.Machine {
	peers, err := s.store.SharePeers(ctx)
	if err != nil {
		return nil
	}
	repo, repoErr := s.projectRepo(ctx, project)
	var out []api.Machine
	var wg sync.WaitGroup
	for _, p := range peers {
		if !p.Shares(project) || !p.OwnMachine {
			continue
		}
		m := s.shareMachine(p.ID)
		out = append(out, api.Machine{ID: shareOriginPrefix + p.ID, Name: p.Name, Online: m != nil, Shared: true})
		i := len(out) - 1
		switch {
		case m == nil:
			continue
		case repoErr != nil:
			out[i].Error = repoErr.Error()
			continue
		}
		wg.Go(func() {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(cctx, http.MethodGet, "http://agentbox/v1/peer/project", nil)
			if err != nil {
				return
			}
			req.Header.Set(headerPeerProject, project)
			req.Header.Set(headerPeerRepo, repo)
			resp, err := m.client.Do(req)
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			defer func() { _ = resp.Body.Close() }()
			var found peerProjectAnswer
			if err := json.NewDecoder(resp.Body).Decode(&found); err == nil {
				out[i].HasProject, out[i].Project = found.Has, found.Project
			}
		})
	}
	wg.Wait()
	return out
}

// runOnKey is how peeringState keeps a session's choice.
func runOnKey(project, session string) string {
	if session == "" {
		session = state.MigratedSession
	}
	return project + "/" + session
}

// choseRunOn records where a peer that wrote in a session of a project's
// chat wants the agents it makes there to run, and tells the lead when that
// changed. Someone writing on this machine is the speaker after, and the
// choice stops counting (runsOnPeer).
func (s *Server) choseRunOn(ctx context.Context, lead state.Agent, runOn string) error {
	p, ok := sharePeerFrom(ctx)
	if !ok || s.peering == nil || !lead.IsLead() || lead.IsHome() {
		return nil
	}
	switch runOn {
	case "", api.RunOnOwner:
		runOn = api.RunOnOwner
	case api.RunOnPeer:
		if !p.OwnMachine {
			return shareRefused{what: "run agents on its own computer: " + s.ownMachineOff(ctx)}
		}
	default:
		return fmt.Errorf("runOn is %q: it is %s or %s", runOn, api.RunOnOwner, api.RunOnPeer)
	}
	key := runOnKey(lead.Project, lead.Session())
	sh := s.peering
	sh.mu.Lock()
	if runOn == api.RunOnPeer {
		sh.runOn[key] = p.ID
	} else {
		delete(sh.runOn, key)
	}
	told := sh.runOnTold[key]
	sh.runOnTold[key] = p.ID + "/" + runOn
	sh.mu.Unlock()
	if told == p.ID+"/"+runOn || (told == "" && runOn == api.RunOnOwner) {
		return nil
	}
	// It waits in the conversation, and the lead reads it with the message
	// it came with.
	s.tellSessionOf(ctx, lead.Project, lead.Session(), runOnNotice(p.Name, runOn), chat.NoticeOptions{})
	return nil
}

// runOnNotice is what the lead is told when a peer changes where its agents
// run.
func runOnNotice(peer, runOn string) string {
	if runOn == api.RunOnPeer {
		return fmt.Sprintf("[AgentBox: %[1]s, the paired AgentBox writing in this session, wants the agents you create for it to run on its own computer, "+
			"with its own AI accounts and GitHub access: pass machine %[1]q to create_agent (AgentBox sends it there when you leave machine out), and a fan_out goes there too. "+
			"They count against none of its slots here, and their notices come back to you; act on one as agent-NN@%[1]s.]", peer)
	}
	return fmt.Sprintf("[AgentBox: %s, the paired AgentBox writing in this session, wants the agents you create for it to run on this computer again: leave machine out.]", peer)
}

// runsOnPeer is the peer whose computer a session's agents go to when the
// lead names no machine: the one that chose it, while it is still the last to
// have written there.
func (s *Server) runsOnPeer(project, session string) string {
	if s.peering == nil {
		return ""
	}
	key := runOnKey(project, session)
	sh := s.peering
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if p := sh.runOn[key]; p != "" && sh.speaker[key] == p {
		return p
	}
	return ""
}

// leadCreateOnPeer sends a lead's create_agent to the computer of the peer
// its session chose, or says why it can't.
func (s *Server) leadCreateOnPeer(w http.ResponseWriter, r *http.Request, peerID string) error {
	return s.leadToPeer(w, r, peerID, "/v1/project/agents")
}

// leadToPeer sends the lead route a lead called to the computer of the peer
// its session chose, or says why it can't.
func (s *Server) leadToPeer(w http.ResponseWriter, r *http.Request, peerID, route string) error {
	p, err := s.store.SharePeer(r.Context(), peerID)
	if err != nil {
		return err
	}
	_, m, _, err := s.shareMachineFor(r.Context(), r.PathValue("project"), shareOriginPrefix+p.ID)
	if err != nil {
		return fmt.Errorf("%s asked for its agents to run on its own computer, and %w", p.Name, err)
	}
	return s.leadOnShareMachine(w, r, p, m, route)
}

// leadFanOutOnPeer sends a lead's fan_out to the computer of the peer its
// session chose: the group is made there, every agent of it on that
// computer's AI accounts, and compared there by its user (shareGuestFanOut).
// The lead is told what that changes for it.
func (s *Server) leadFanOutOnPeer(w http.ResponseWriter, r *http.Request, peerID string, req api.FanOutRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	rec := httptest.NewRecorder()
	if err := s.leadToPeer(rec, r, peerID, "/v1/project/groups"); err != nil {
		return err
	}
	var out api.FanOutResult
	if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		copyRecorded(w, rec)
		return nil
	}
	name := "the paired AgentBox"
	if p, err := s.store.SharePeer(r.Context(), peerID); err == nil {
		name = p.Name
	}
	out.Notices = append(out.Notices, fmt.Sprintf("The group runs on %[1]s's computer, as %[1]s asked, on its AI accounts: %[1]s compares it there and keeps one, "+
		"and compare_group can't reach it. You hear as each agent finishes and once when all have (act on one as agent-NN@%[1]s; read_agent reads its work).", name))
	return writeJSON(w, http.StatusAccepted, out)
}

// --- The peer's side: this computer taking agents for an owner. ---

// remoteMachine keeps this computer connected to a remote, to take the agents
// its projects' chats make for this AgentBox, connecting again after a pause
// whenever the connection ends.
func (s *Server) remoteMachine(ctx context.Context, c *remoteConn) {
	wait := time.Second
	for ctx.Err() == nil {
		began := time.Now()
		err := s.remoteMachineOnce(ctx, c)
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		changed := c.machineUp || c.machineErr != errText(err)
		c.machineUp, c.machineErr = false, errText(err)
		if changed {
			c.runOn = nil // what the composer says changes with it
		}
		c.mu.Unlock()
		if changed {
			s.publishShare(ctx)
		}
		if time.Since(began) > time.Minute {
			wait = time.Second
		}
		var refused errMachineRefused
		pause := wait
		if errors.As(err, &refused) {
			pause = refused.retry // its owner said no, or doesn't know the way
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		wait = min(wait*2, 30*time.Second)
	}
}

// errMachineRefused is an owner turning this computer away, which is asked
// again only after retry.
type errMachineRefused struct {
	why   string
	retry time.Duration
}

func (e errMachineRefused) Error() string { return e.why }

func (s *Server) remoteMachineOnce(ctx context.Context, c *remoteConn) error {
	conn, resp, err := websocket.Dial(ctx, strings.TrimRight(c.r.URL, "/")+"/share/machine", &websocket.DialOptions{
		HTTPClient: s.peering.remotes.client,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}},
	})
	if err != nil {
		switch {
		case resp == nil:
			return fmt.Errorf("%s can't be reached: %w", c.r.Name, err)
		case resp.StatusCode == http.StatusForbidden:
			return errMachineRefused{remoteStatusError(c, resp).Error(), 20 * time.Second}
		case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound:
			// Its phone page answered: an AgentBox from before this.
			return errMachineRefused{fmt.Sprintf("%s's AgentBox is too old to run agents on this computer: it has to be updated", c.r.Name), 10 * time.Minute}
		default:
			return remoteStatusError(c, resp)
		}
	}
	conn.SetReadLimit(-1)
	session, err := yamux.Server(websocket.NetConn(ctx, conn, websocket.MessageBinary), hubapi.TunnelConfig())
	if err != nil {
		_ = conn.CloseNow()
		return err
	}
	defer func() { _ = session.Close() }()
	c.mu.Lock()
	c.machineUp, c.machineErr, c.runOn = true, "", nil
	c.mu.Unlock()
	s.publishShare(ctx)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.serveOwner(w, r, c); err != nil {
			writeError(w, err)
		}
	}), ReadHeaderTimeout: 30 * time.Second}
	stop := context.AfterFunc(ctx, func() { _ = session.Close() })
	defer stop()
	_ = srv.Serve(session)
	return fmt.Errorf("the connection to %s closed", c.r.Name)
}

// shareOrigin is a remote as the origin of the agents it makes here.
func shareOrigin(c *remoteConn) hubapi.HubEnvironment {
	return hubapi.HubEnvironment{ID: shareOriginPrefix + c.r.ID, Name: c.r.Name}
}

// shareGuestRoutes are the lead routes an owner's lead may send this
// computer, of those another machine of the hub account may
// (peerLeadRoutes): its own agents', never those of this computer's user.
var shareGuestRoutes = func() *http.ServeMux {
	mux := http.NewServeMux()
	for _, pattern := range []string{
		"GET /v1/project/agents",
		"POST /v1/project/agents",
		"POST /v1/project/groups",
		"GET /v1/project/agents/{agent}/chat",
		"POST /v1/project/agents/{agent}/chat/messages",
		"PUT /v1/project/agents/{agent}/goal",
		"DELETE /v1/project/agents/{agent}/goal",
		"GET /v1/project/agents/{agent}/diff",
		"POST /v1/project/agents/{agent}/run",
		"POST /v1/project/retire",
		"GET /v1/project/questions",
		"POST /v1/project/questions/{id}/answer",
		"POST /v1/project/questions/{id}/escalate",
	} {
		mux.Handle(pattern, http.NotFoundHandler())
	}
	return mux
}()

// shareGuestRoute is the pattern of a lead route an owner may send here, ""
// for one it may not.
func shareGuestRoute(r *http.Request) string {
	_, pattern := shareGuestRoutes.Handler(r)
	return pattern
}

// serveOwner serves what an owner sends over this computer's connection: the
// lead routes of the project cloned from the repository it names, for the
// agents its lead made here and no others.
func (s *Server) serveOwner(w http.ResponseWriter, r *http.Request, c *remoteConn) error {
	ctx := r.Context()
	origin := shareOrigin(c)
	repo := r.Header.Get(headerPeerRepo)
	if r.Method == http.MethodGet && r.URL.Path == "/v1/peer/project" {
		p, _, err := s.peerProjectFor(ctx, repo, false)
		if errors.Is(err, state.ErrNotFound) {
			return writeJSON(w, http.StatusOK, peerProjectAnswer{})
		} else if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, peerProjectAnswer{Has: true, Project: p.Name})
	}
	pattern := shareGuestRoute(r)
	if pattern == "" {
		return writeJSON(w, http.StatusForbidden, api.Error{Error: fmt.Sprintf("%s's lead can't send %s %s to this computer", c.r.Name, r.Method, r.URL.Path)})
	}
	if repo == "" {
		return errors.New("the other AgentBox didn't say which repository the project is")
	}
	switch pattern {
	case "POST /v1/project/agents":
		return s.shareGuestCreate(w, r, origin, repo)
	case "POST /v1/project/groups":
		return s.shareGuestFanOut(w, r, origin, repo)
	}
	p, _, err := s.peerProjectFor(ctx, repo, false)
	if err != nil {
		return err
	}
	theirs := func(agent string) bool {
		o, err := s.store.PeerAgent(ctx, p.Name, agent)
		return err == nil && o.OriginID == origin.ID
	}
	// The route's own path values: the lead routes' mux sets them again.
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := s.serveOwnerRoute(w, r, p, pattern, theirs); err != nil {
			writeError(w, err)
		}
	})
	mux.ServeHTTP(w, r)
	return nil
}

// errNotTheirs is an owner's lead reaching an agent it didn't make here.
type errNotTheirs struct{ agent string }

func (e errNotTheirs) Error() string {
	return e.agent + " isn't one of the agents your lead made on this computer"
}

func (e errNotTheirs) Unwrap() error { return state.ErrNotFound }

func (s *Server) serveOwnerRoute(w http.ResponseWriter, r *http.Request, p state.Project, pattern string, theirs func(string) bool) error {
	ctx := r.Context()
	lead := s.leadRoutes(p.Name, "")
	if agent := r.PathValue("agent"); agent != "" && !theirs(agent) {
		return errNotTheirs{agent}
	}
	if id := r.PathValue("id"); id != "" {
		q, err := s.store.Question(ctx, id)
		if err != nil || q.Project != p.Name || !theirs(q.Agent) {
			return fmt.Errorf("question %s isn't one of your agents' on this computer: %w", id, state.ErrNotFound)
		}
	}
	switch pattern {
	case "POST /v1/project/retire":
		var req api.RetireRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		if len(req.Agents) == 0 {
			// Every idle one is every idle one of its own.
			agents, err := s.store.Agents(ctx, p.Name)
			if err != nil {
				return err
			}
			for _, a := range agents {
				if !a.IsLead() && theirs(a.Name) {
					req.Agents = append(req.Agents, a.Name)
				}
			}
			if len(req.Agents) == 0 {
				return writeJSON(w, http.StatusOK, api.RetireResult{How: cmp.Or(req.How, api.RetireStop)})
			}
		}
		for _, name := range req.Agents {
			if !theirs(name) {
				return errNotTheirs{name}
			}
		}
		body, _ := json.Marshal(req)
		r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	case "GET /v1/project/agents", "GET /v1/project/questions":
		rec := httptest.NewRecorder()
		lead.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			copyRecorded(w, rec)
			return nil
		}
		out, err := onlyTheirs(rec.Body.Bytes(), pattern == "GET /v1/project/agents", theirs)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
		return nil
	}
	lead.ServeHTTP(w, r)
	return nil
}

// onlyTheirs cuts a fleet (fleet) or a list of questions down to the agents
// an owner's lead made here.
func onlyTheirs(body []byte, fleet bool, theirs func(string) bool) ([]byte, error) {
	if !fleet {
		var qs []map[string]json.RawMessage
		if err := json.Unmarshal(body, &qs); err != nil {
			return nil, err
		}
		out := []map[string]json.RawMessage{}
		for _, q := range qs {
			var agent string
			_ = json.Unmarshal(q["agent"], &agent)
			if theirs(agent) {
				out = append(out, q)
			}
		}
		return json.Marshal(out)
	}
	var f api.Fleet
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, err
	}
	f.Agents = slices.DeleteFunc(f.Agents, func(a api.FleetAgent) bool { return !theirs(a.Name) })
	f.Idle = 0
	for _, a := range f.Agents {
		if a.Idle {
			f.Idle++
		}
	}
	f.Creating, f.Remote = nil, nil
	return json.Marshal(f)
}

// shareGuestCreate makes an agent for an owner's lead, in the project cloned
// from repo with this computer's GitHub account, on this computer's AI
// accounts: what the owner's lead says of accounts and connectors names the
// owner's, which aren't here.
func (s *Server) shareGuestCreate(w http.ResponseWriter, r *http.Request, origin hubapi.HubEnvironment, repo string) error {
	var req api.CreateAgentRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	req.ClaudeAccount, req.CodexAccount, req.CursorAccount, req.GeminiAccount, req.CopilotAccount, req.GitHubAccount = "", "", "", "", "", ""
	req.OpenCodeAccounts = nil
	none := []string{}
	req.Connectors = &none
	data, _ := json.Marshal(req)
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(data)), int64(len(data))
	return s.peerCreateAgent(w, r, origin, repo)
}

// shareGuestFanOut makes a group for an owner's lead, as shareGuestCreate
// makes an agent: in the project cloned from repo, on this computer's AI
// accounts, each agent the owner's lead's. The group stays here, to be
// compared and kept by this computer's user, so the owner's lead can't judge
// it, and a project cloned for it goes if none of its agents is made.
func (s *Server) shareGuestFanOut(w http.ResponseWriter, r *http.Request, origin hubapi.HubEnvironment, repo string) error {
	var req api.FanOutRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Judge {
		return errors.New("this group is compared here, by this computer's user, so you can't judge it: compare_group reaches only the groups of your own AgentBox. " +
			"Leave judge out, and read the agents with read_agent when they have finished")
	}
	for i := range req.Variants {
		v := &req.Variants[i]
		v.ClaudeAccount, v.CodexAccount, v.CursorAccount, v.GeminiAccount, v.CopilotAccount = "", "", "", "", ""
		v.OpenCodeAccounts = nil
	}
	ctx := r.Context()
	p, err := s.claimPeerProject(ctx, repo)
	if err != nil {
		return err
	}
	// The request's own hold, until its agents' jobs hold the project in its stead.
	defer s.releasePeerProject(context.WithoutCancel(ctx), p.Name, false)
	req.Project = p.Name
	req.OriginID, req.OriginName, req.OriginProject = origin.ID, origin.Name, r.Header.Get(headerPeerProject)
	out, err := s.fanOut(ctx, req, true)
	if err != nil {
		return err
	}
	s.logf("share: %s's lead (%s) is running group %s in %s here", origin.Name, req.OriginProject, out.Group.ID, p.Name)
	return writeJSON(w, http.StatusAccepted, out)
}

// sendShareEvent tells a remote about an agent its lead made on this
// computer, in the background, trying again while it can't be reached.
func (s *Server) sendShareEvent(remoteID string, ev peerEvent) {
	ctx := s.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	body, _ := json.Marshal(api.ShareAgentEvent{
		Project: ev.Project, Agent: ev.Agent, Title: ev.Title, Branch: ev.Branch, Status: ev.Status,
		Summary: ev.Summary, PRURL: ev.PRURL, Notice: ev.Notice, Act: ev.Act,
	})
	go func() {
		var err error
		for i := 0; ; i++ {
			c := s.remoteConn(remoteID)
			if c == nil {
				s.logf("share: telling the AgentBox that made %s: it isn't paired with this one any more", ev.Agent)
				return
			}
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = s.postShareEvent(cctx, c, body)
			cancel()
			if err == nil {
				return
			}
			if i >= len(peerRetries) {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(peerRetries[i]):
			}
		}
		s.logf("share: couldn't tell %s about %s (%s): %v", ev.Project, ev.Agent, ev.Status, err)
	}()
}

func (s *Server) postShareEvent(ctx context.Context, c *remoteConn, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.r.URL, "/")+"/share/agent-events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.peering.remotes.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return remoteStatusError(c, resp)
	}
	return nil
}

// shareRunOnTTL is how long what remoteRunOn found is believed: the composer
// asks each time it opens, and a GitHub call each time is too many.
const shareRunOnTTL = 20 * time.Second

// remoteRunOn answers GET /v1/projects/<remote>~<project>/run-on: whether this
// computer can run the agents that project's chat makes for this AgentBox,
// and why not when it can't.
func (s *Server) remoteRunOn(ctx context.Context, c *remoteConn, project string) api.ShareRunOn {
	c.mu.Lock()
	if hit, ok := c.runOn[project]; ok && time.Since(hit.at) < shareRunOnTTL {
		c.mu.Unlock()
		return hit.out
	}
	c.mu.Unlock()
	out := api.ShareRunOn{Owner: c.r.Name}
	out.Why = s.runOnHereWhy(ctx, c, project)
	out.Here = out.Why == ""
	c.mu.Lock()
	if c.runOn == nil {
		c.runOn = map[string]runOnHit{}
	}
	c.runOn[project] = runOnHit{out: out, at: time.Now()}
	c.mu.Unlock()
	return out
}

type runOnHit struct {
	out api.ShareRunOn
	at  time.Time
}

func (s *Server) runOnHereWhy(ctx context.Context, c *remoteConn, project string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.requestAt(ctx, s.peering.remotes.client, http.MethodGet, "/share/run-on?project="+url.QueryEscape(project))
	if err != nil {
		return fmt.Sprintf("%s can't be reached: %v", c.r.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return fmt.Sprintf("%s's AgentBox is too old to run agents on this computer: it has to be updated", c.r.Name)
	}
	if resp.StatusCode != http.StatusOK {
		return remoteStatusError(c, resp).Error()
	}
	var info api.ShareOwnerRunOn
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return err.Error()
	}
	if !info.Allowed {
		return info.Why
	}
	c.mu.Lock()
	up, why := c.machineUp, c.machineErr
	c.mu.Unlock()
	if !up && !info.Connected {
		return fmt.Sprintf("this computer isn't connected to %s to take agents yet%s", c.r.Name, cmp.Or(prefixed(": ", why), ": try again in a moment"))
	}
	if ready, err := image.Ready(ctx, s.cfg.Incus); err == nil && !ready {
		return "this computer has no base image to make agents from yet: build it in Setup first"
	}
	return s.pushWhy(ctx, info.Repo)
}

func prefixed(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}

// pushWhy is why this computer's GitHub account can't clone a repository
// and push to it, "" when it can or when only GitHub could say and the
// repository isn't on GitHub.
func (s *Server) pushWhy(ctx context.Context, repoURL string) string {
	repo, err := github.ParseRemote(repoURL)
	if err != nil {
		return "" // not GitHub: cloning it says
	}
	m := s.manager(nil)
	account, err := m.GitHubAccountFor(state.Project{}, "")
	if err != nil {
		if errors.As(err, new(*fs.PathError)) {
			return "reading this computer's GitHub accounts: " + err.Error()
		}
		return fmt.Sprintf("this computer has no GitHub account to clone %s/%s and push to it with: add one under Settings → Accounts", repo.Owner, repo.Name)
	}
	token, err := m.Creds.GitHubToken(account)
	if err != nil || token == "" {
		return fmt.Sprintf("this computer has no GitHub account to clone %s/%s and push to it with: add one under Settings → Accounts", repo.Owner, repo.Name)
	}
	info, err := s.gitHub(token).Info(ctx, repo)
	if err != nil {
		return fmt.Sprintf("your GitHub account (%s) can't reach %s/%s: ask its owner for access (%v)", account, repo.Owner, repo.Name, err)
	}
	if info.CanPushKnown && !info.CanPush {
		return fmt.Sprintf("your GitHub account (%s) can't push to %s/%s: ask its owner for write access", account, repo.Owner, repo.Name)
	}
	return ""
}
