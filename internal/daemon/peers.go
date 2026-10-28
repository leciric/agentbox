package daemon

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/hubapi"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/gitrepo"
	"agentbox/internal/remote"
	"agentbox/internal/state"
)

// A lead directing agents on another machine of its hub account, like T3
// Code's linked environments. Both machines connect to the same hub as
// environments; nothing else is set up, and nothing secret passes through a
// chat.
//
//   - The lead's tools take a machine (agentbox mcp). Its socket's
//     /v1/project/machines/<name>/api/<lead route> comes here (leadOnMachine),
//     which sends the same lead route to that machine through the hub's peer
//     API (hubapi.PeerAPI), with this machine's environment token: the
//     pairing is the authentication.
//   - The hub passes it on only to an environment of the same owner, saying
//     which environment asked (hubapi.HeaderPeer). The other machine checks
//     that against its own list of its owner's machines (checkPeer), finds
//     the project by its repository's URL (peerProjectFor), cloning it the
//     first time an agent is made there, and serves the request with that
//     project's lead routes, as if its own lead had sent it (servePeer).
//   - An agent made that way remembers where it came from (state.PeerAgent),
//     and its notices — finished, a question, failing — go back to the lead
//     that made it (tellLeadAbout, sendPeerEvent) instead of to the project's
//     lead there. This machine keeps what they said (state.RemoteAgent), which
//     the app shows beside the lead and list_agents lists.

// Headers a machine's daemon sends a peer along with a lead route: the
// repository the project is found by, and the project's name here, which the
// peer's notices name it by. The hub passes them through untouched; the peer
// trusts the hub's hubapi.HeaderPeer for who is asking, not these.
const (
	headerPeerRepo    = "X-AgentBox-Peer-Repo"
	headerPeerProject = "X-AgentBox-Peer-Project"
)

// peerCacheFor is how long the hub's list of this account's machines is
// trusted before it is asked again.
const peerCacheFor = 20 * time.Second

// peerState is what peers.go keeps.
type peerState struct {
	mu      sync.Mutex
	listed  time.Time
	peers   []hubapi.HubEnvironment
	listErr error
	// cloning holds new projects back while one is cloned for a peer, so
	// two agents asked for at once don't clone the same repository twice.
	cloning sync.Mutex
}

// errNoHub is a lead naming a machine on a machine that connects to no hub.
var errNoHub = errors.New("this machine isn't connected to a hub, so it has no other machines: connect both to the same hub account " +
	"(Settings → Remote), or leave machine out to work here")

// hubPeers lists the other machines of this machine's hub account, from a
// cache unless fresh is set.
func (s *Server) hubPeers(ctx context.Context, fresh bool) (*remoteConfig, []hubapi.HubEnvironment, error) {
	cfg := s.readRemoteConfig()
	if cfg == nil {
		return nil, nil, errNoHub
	}
	s.peerState.mu.Lock()
	defer s.peerState.mu.Unlock()
	if !fresh && time.Since(s.peerState.listed) < peerCacheFor {
		return cfg, s.peerState.peers, s.peerState.listErr
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	peers, err := remote.Peers(ctx, nil, cfg.Hub, cfg.Token)
	if err != nil {
		err = fmt.Errorf("asking the hub %s for this account's machines: %w", cfg.Hub, err)
	}
	s.peerState.listed, s.peerState.peers, s.peerState.listErr = time.Now(), peers, err
	return cfg, peers, err
}

// findMachine is the machine a lead named, by its name on the hub or its ID,
// and an error the lead can act on when there is none or it is offline.
func (s *Server) findMachine(ctx context.Context, name string) (*remoteConfig, hubapi.HubEnvironment, error) {
	cfg, peers, err := s.hubPeers(ctx, false)
	if err != nil {
		return nil, hubapi.HubEnvironment{}, err
	}
	find := func() (hubapi.HubEnvironment, bool) {
		for _, p := range peers {
			if p.Name == name || p.ID == name {
				return p, true
			}
		}
		return hubapi.HubEnvironment{}, false
	}
	m, ok := find()
	if !ok || !m.Online {
		// Just added, or just back online: the cache is the last to know.
		if cfg, peers, err = s.hubPeers(ctx, true); err != nil {
			return nil, hubapi.HubEnvironment{}, err
		}
		m, ok = find()
	}
	if !ok {
		names := make([]string, 0, len(peers))
		for _, p := range peers {
			names = append(names, p.Name)
		}
		if len(names) == 0 {
			return nil, m, fmt.Errorf("no machine is called %q: this hub account has no other machine; connect one to it first", name)
		}
		return nil, m, fmt.Errorf("no machine is called %q on this hub account; its machines are %s (list_resources machines)", name, strings.Join(names, ", "))
	}
	if !m.Online {
		last := "never"
		if !m.LastSeenAt.IsZero() {
			last = m.LastSeenAt.Format(time.RFC3339)
		}
		return nil, m, fmt.Errorf("%s is offline (last connected: %s), so nothing was sent to it: work here, or wait until it is back", m.Name, last)
	}
	return cfg, m, nil
}

// projectRepo is the URL another machine finds a project by: its origin
// remote's.
func (s *Server) projectRepo(ctx context.Context, project string) (string, error) {
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return "", err
	}
	u, err := gitrepo.RemoteURL(p.Root, "origin")
	if errors.Is(err, gitrepo.ErrNoRemote) || (err == nil && u == "") {
		return "", fmt.Errorf("%s has no origin remote, so another machine can't find or clone it: push it to a git host first", project)
	}
	return u, err
}

// sameRepo reports whether two remote URLs name one repository, however each
// is spelled: https or ssh, with or without a user, ".git" or a trailing
// slash.
func sameRepo(a, b string) bool {
	return a != "" && repoKey(a) == repoKey(b)
}

// repoKey is a remote URL as host/path, lowercase.
func repoKey(u string) string {
	u = strings.TrimSpace(u)
	if rest, ok := strings.CutPrefix(u, "file://"); ok {
		u = rest
	} else if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if at := strings.LastIndex(strings.SplitN(u, "/", 2)[0], "@"); at >= 0 {
			u = u[at+1:]
		}
		if host, rest, ok := strings.Cut(u, "/"); ok {
			if h, _, ok := strings.Cut(host, ":"); ok {
				host = h // a port
			}
			u = host + "/" + rest
		}
	} else if host, rest, ok := strings.Cut(u, ":"); ok && !strings.Contains(host, "/") {
		// scp-like: git@github.com:owner/repo
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		u = host + "/" + rest
	}
	u = strings.TrimSuffix(strings.TrimRight(u, "/"), ".git")
	return strings.ToLower(u)
}

// cloneable refuses a repository URL another machine can't mean to clone: a
// path on the machine that asked.
func cloneable(u string) error {
	if i := strings.Index(u, "://"); i > 0 {
		switch strings.ToLower(u[:i]) {
		case "https", "http", "ssh", "git":
			return nil
		}
	} else if host, _, ok := strings.Cut(u, ":"); ok && len(host) > 1 && !strings.ContainsAny(host, `/\`) {
		// scp-like, git@github.com:owner/repo, and not a Windows drive (C:)
		return nil
	}
	return fmt.Errorf("%s is a path on the other machine, which this one can't clone: give the project a remote on a git host", u)
}

// peerProjectFor is this machine's project for a repository, found by its
// origin remote. With clone, a repository no project has is cloned into
// ~/src and added, and cloned reports it; without, it is state.ErrNotFound.
func (s *Server) peerProjectFor(ctx context.Context, repo string, clone bool) (p state.Project, cloned bool, err error) {
	find := func() (state.Project, bool, error) {
		projects, err := s.store.Projects(ctx)
		if err != nil {
			return state.Project{}, false, err
		}
		for _, p := range projects {
			if p.Name == state.HomeProject {
				continue
			}
			if u, err := gitrepo.RemoteURL(p.Root, "origin"); err == nil && sameRepo(u, repo) {
				return p, true, nil
			}
		}
		return state.Project{}, false, nil
	}
	if p, ok, err := find(); err != nil || ok {
		return p, false, err
	}
	if !clone {
		return state.Project{}, false, fmt.Errorf("this machine has no project cloned from %s: create an agent on it first, which clones it: %w", repo, state.ErrNotFound)
	}
	if err := cloneable(repo); err != nil {
		return state.Project{}, false, err
	}
	s.peerState.cloning.Lock()
	defer s.peerState.cloning.Unlock()
	if p, ok, err := find(); err != nil || ok {
		return p, false, err
	}
	base := strings.TrimSuffix(path.Base(strings.TrimRight(repoKey(repo), "/")), ".git")
	name := base
	for n := 2; s.projectNameFree(ctx, name) != nil; n++ {
		if n > 50 {
			return state.Project{}, false, fmt.Errorf("every name like %s is taken by another project here", base)
		}
		name = fmt.Sprintf("%s-%d", base, n)
	}
	root := s.peerCloneRoot
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return state.Project{}, false, err
		}
		root = filepath.Join(home, "src")
	}
	slug, err := s.store.ProjectSlug(ctx, name)
	if err != nil {
		return state.Project{}, false, err
	}
	dest := filepath.Join(root, slug)
	if err := s.cloneForPeer(ctx, repo, dest); err != nil {
		return state.Project{}, false, err
	}
	p, err = s.addProjectRepo(ctx, api.AddProjectRequest{Path: dest, Name: name})
	if err != nil {
		_ = os.RemoveAll(dest)
		return state.Project{}, false, err
	}
	s.logf("peers: cloned %s into %s as the project %s, for another machine's lead", repo, dest, p.Name)
	return p, true, nil
}

// cloneForPeer clones a repository; tests replace it.
func (s *Server) cloneForPeer(ctx context.Context, repo, dest string) error {
	if s.peerClone != nil {
		return s.peerClone(ctx, repo, dest)
	}
	return s.manager(s.cfg.Log).CloneForHome(ctx, repo, dest)
}

// forgetPeerProject takes back a project cloned for a request that then
// failed, so a refusal leaves nothing behind.
func (s *Server) forgetPeerProject(ctx context.Context, p state.Project) {
	s.stopLeadAPI(p.Name)
	if err := s.store.RemoveProject(ctx, p.Name); err != nil {
		s.logf("peers: taking back the project %s: %v", p.Name, err)
		return
	}
	_ = os.RemoveAll(p.Root)
	s.events.publish(api.EventProject, api.ProjectChange{Name: p.Name, Removed: true})
}

// leadMachines answers GET /v1/project/machines: the account's other
// machines, and whether each has the project.
func (s *Server) leadMachines(w http.ResponseWriter, r *http.Request) error {
	cfg, peers, err := s.hubPeers(r.Context(), true)
	if err != nil {
		return err
	}
	repo, repoErr := s.projectRepo(r.Context(), r.PathValue("project"))
	out := make([]api.Machine, len(peers))
	var wg sync.WaitGroup
	for i, p := range peers {
		out[i] = api.Machine{ID: p.ID, Name: p.Name, Online: p.Online, Hostname: p.Hostname}
		switch {
		case !p.Online:
			continue
		case repoErr != nil:
			out[i].Error = repoErr.Error()
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			var found peerProjectAnswer
			if err := s.peerCall(ctx, cfg, p.ID, http.MethodGet, "/v1/peer/project", r.PathValue("project"), repo, nil, &found); err != nil {
				out[i].Error = err.Error()
				return
			}
			out[i].HasProject, out[i].Project = found.Has, found.Project
		})
	}
	wg.Wait()
	return writeJSON(w, http.StatusOK, out)
}

// peerProjectAnswer is GET /v1/peer/project's answer.
type peerProjectAnswer struct {
	Has     bool   `json:"has"`
	Project string `json:"project,omitempty"`
}

// peerCall sends one request to a peer through the hub and reads its JSON
// answer into out.
func (s *Server) peerCall(ctx context.Context, cfg *remoteConfig, peer, method, route, project, repo string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, remote.PeerURL(cfg.Hub, peer)+route, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerPeerProject, project)
	req.Header.Set(headerPeerRepo, repo)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		var e api.Error
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("the other machine answered %s", resp.Status)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// leadOnMachine answers /v1/project/machines/{machine}/api/{path...}: the
// lead route after api/, sent to that machine through the hub, its answer
// passed back as it is.
func (s *Server) leadOnMachine(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	cfg, m, err := s.findMachine(r.Context(), r.PathValue("machine"))
	if err != nil {
		return err
	}
	repo, err := s.projectRepo(r.Context(), project)
	if err != nil {
		return err
	}
	route := "/" + r.PathValue("path")
	if !peerLeadRoute(r.Method, route) {
		return fmt.Errorf("%s %s can't be sent to another machine", r.Method, route)
	}
	target, err := url.Parse(remote.PeerURL(cfg.Hub, m.ID))
	if err != nil {
		return err
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = target.Scheme, target.Host, target.Host
			pr.Out.URL.Path, pr.Out.URL.RawPath = target.Path+route, ""
			pr.Out.Header.Set("Authorization", "Bearer "+cfg.Token)
			pr.Out.Header.Set(headerPeerProject, project)
			pr.Out.Header.Set(headerPeerRepo, repo)
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode == http.StatusOK && r.Method == http.MethodPost && route == "/v1/project/retire" {
				s.noteRemoteRetired(resp, project, m)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, fmt.Errorf("couldn't reach %s through the hub (%v): it may not have got the request; list_agents with machine says what it has", m.Name, err))
		},
		ErrorLog: log.New(io.Discard, "", 0),
	}
	proxy.ServeHTTP(w, r)
	return nil
}

// noteRemoteRetired records the agents a peer's retire answer says it
// retired, leaving the answer to be read again.
func (s *Server) noteRemoteRetired(resp *http.Response, project string, m hubapi.HubEnvironment) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil {
		return
	}
	var result api.RetireResult
	if json.Unmarshal(data, &result) != nil {
		return
	}
	for _, a := range result.Retired {
		status := state.RemoteRetired
		if result.How == api.RetirePause || result.How == api.RetireStop {
			status = state.RemoteDone // back in seconds; told again if it works
		}
		_ = s.store.SaveRemoteAgent(context.WithoutCancel(resp.Request.Context()), state.RemoteAgent{
			Project: project, MachineID: m.ID, MachineName: m.Name, Agent: a.Name, Status: status, Summary: "retired (" + result.How + ")", UpdatedAt: time.Now(),
		})
	}
	if len(result.Retired) > 0 {
		s.events.publish(api.EventRemoteAgents, api.RemoteAgentsChange{Project: project})
	}
}

// peerLeadRoutes are the lead routes another machine's lead may send here:
// those its tools take a machine on. The rest — notes, memory, skills,
// schedules, accounts — are this machine's own lead's.
var peerLeadRoutes = func() *http.ServeMux {
	mux := http.NewServeMux()
	for _, pattern := range []string{
		"GET /v1/project/agents",
		"POST /v1/project/agents",
		"GET /v1/project/archived-agents",
		"GET /v1/project/agents/{agent}/chat",
		"POST /v1/project/agents/{agent}/chat/messages",
		"GET /v1/project/agents/{agent}/diff",
		"POST /v1/project/agents/{agent}/run",
		"POST /v1/project/retire",
		"POST /v1/project/copy",
		"GET /v1/project/questions",
		"POST /v1/project/questions/{id}/answer",
		"POST /v1/project/questions/{id}/escalate",
	} {
		mux.Handle(pattern, http.NotFoundHandler())
	}
	return mux
}()

// peerLeadRoute reports whether a lead route may be sent to another machine.
func peerLeadRoute(method, route string) bool {
	r, err := http.NewRequest(method, "http://agentbox"+route, nil)
	if err != nil {
		return false
	}
	_, pattern := peerLeadRoutes.Handler(r)
	return pattern != ""
}

// hubHandler serves the hub's tunnel: what a device sends, as remoteHandler
// does, and what another machine of the account sends (servePeer), which
// the hub marks as a peer's.
func (s *Server) hubHandler() http.Handler {
	devices := s.remoteHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(hubapi.HeaderPeer) != "" {
			if err := s.servePeer(w, r); err != nil {
				writeError(w, err)
			}
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/peer/") {
			_ = writeJSON(w, http.StatusForbidden, api.Error{Error: "only another machine of this hub account reaches " + r.URL.Path})
			return
		}
		devices.ServeHTTP(w, r)
	})
}

// errNotPeer is a request from an environment that isn't one of this
// machine's account's.
type errNotPeer struct{ name string }

func (e errNotPeer) Error() string {
	return fmt.Sprintf("the machine %s isn't connected to this machine's hub account, so it can't direct agents here", e.name)
}

// checkPeer refuses a peer that isn't one of the machines the hub lists for
// this machine's own account: the hub already refuses another account's, and
// this is the machine not taking the hub's word for it.
func (s *Server) checkPeer(ctx context.Context, id, name string) error {
	for _, fresh := range []bool{false, true} {
		_, peers, err := s.hubPeers(ctx, fresh)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(peers, func(p hubapi.HubEnvironment) bool { return p.ID == id }) {
			return nil
		}
	}
	return errNotPeer{cmp.Or(name, id)}
}

// servePeer serves another machine of the account: whether it has a
// project, the notices of the agents it made there, and the lead routes its
// lead sends for the project.
func (s *Server) servePeer(w http.ResponseWriter, r *http.Request) error {
	peer := hubapi.HubEnvironment{ID: r.Header.Get(hubapi.HeaderPeer), Name: r.Header.Get(hubapi.HeaderPeerName)}
	if err := s.checkPeer(r.Context(), peer.ID, peer.Name); err != nil {
		var notPeer errNotPeer
		if errors.As(err, &notPeer) {
			s.logf("peers: refused %s %s from %s (%s): not this account's", r.Method, r.URL.Path, peer.Name, peer.ID)
			return writeJSON(w, http.StatusForbidden, api.Error{Error: err.Error()})
		}
		return err
	}
	repo := r.Header.Get(headerPeerRepo)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/peer/project":
		p, _, err := s.peerProjectFor(r.Context(), repo, false)
		if errors.Is(err, state.ErrNotFound) {
			return writeJSON(w, http.StatusOK, peerProjectAnswer{})
		} else if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, peerProjectAnswer{Has: true, Project: p.Name})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/peer/events":
		return s.peerEventIn(w, r, peer)
	case !peerLeadRoute(r.Method, r.URL.Path):
		return writeJSON(w, http.StatusForbidden, api.Error{Error: fmt.Sprintf("another machine can't send %s %s here", r.Method, r.URL.Path)})
	}
	if repo == "" {
		return errors.New("the other machine didn't say which repository the project is")
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/project/agents" {
		return s.peerCreateAgent(w, r, peer, repo)
	}
	p, _, err := s.peerProjectFor(r.Context(), repo, false)
	if err != nil {
		return err
	}
	s.leadRoutes(p.Name).ServeHTTP(w, r)
	return nil
}

// peerCreateAgent makes an agent for another machine's lead, in the project
// cloned from repo, cloning it first if no project here is.
func (s *Server) peerCreateAgent(w http.ResponseWriter, r *http.Request, peer hubapi.HubEnvironment, repo string) error {
	var req api.CreateAgentRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p, cloned, err := s.peerProjectFor(r.Context(), repo, true)
	if err != nil {
		return err
	}
	req.Project = p.Name
	req.OriginID, req.OriginName, req.OriginProject = peer.ID, peer.Name, r.Header.Get(headerPeerProject)
	j, err := s.createAgentJob(r.Context(), req, true)
	if err != nil {
		if cloned {
			s.forgetPeerProject(context.WithoutCancel(r.Context()), p)
		}
		return err
	}
	s.logf("peers: %s's lead (%s) is creating an agent in %s here", peer.Name, req.OriginProject, p.Name)
	return writeJSON(w, http.StatusAccepted, j)
}

// peerEvent is what one machine tells another about an agent the other's
// lead made: POST /v1/peer/events.
type peerEvent struct {
	Project string `json:"project"` // the project on the machine told
	Agent   string `json:"agent"`
	Title   string `json:"title,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
	PRURL   string `json:"prUrl,omitempty"`
	// Notice is what the lead is told, if anything, and Act whether it
	// starts a turn of the lead's.
	Notice string `json:"notice,omitempty"`
	Act    bool   `json:"act,omitempty"`
}

// peerEventIn takes a notice about one of the agents this machine's lead
// made on the peer.
func (s *Server) peerEventIn(w http.ResponseWriter, r *http.Request, peer hubapi.HubEnvironment) error {
	var ev peerEvent
	if err := readJSON(r, &ev); err != nil {
		return err
	}
	if _, err := s.store.Project(r.Context(), ev.Project); err != nil {
		return err
	}
	if ev.Agent == "" {
		return errors.New("a notice about no agent")
	}
	if err := s.store.SaveRemoteAgent(r.Context(), state.RemoteAgent{
		Project: ev.Project, MachineID: peer.ID, MachineName: peer.Name, Agent: ev.Agent,
		Title: ev.Title, Branch: ev.Branch, Status: ev.Status, Summary: headWords(ev.Summary, 200), PRURL: ev.PRURL, UpdatedAt: time.Now(),
	}); err != nil {
		return err
	}
	s.events.publish(api.EventRemoteAgents, api.RemoteAgentsChange{Project: ev.Project})
	if ev.Notice != "" {
		s.tellLeadOf(r.Context(), ev.Project, remoteNotice(peer.Name, ev.Agent, ev.Notice), chat.NoticeOptions{Act: ev.Act})
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// remoteNotice is a notice about an agent on another machine, as its lead
// reads it.
func remoteNotice(machine, agent, notice string) string {
	return fmt.Sprintf("[On the machine %s] %s\n(%s runs on %s: name it %s@%s to act on it.)", machine, notice, agent, machine, agent, machine)
}

// tellLeadAbout tells the lead about one of its agents: the project's own,
// or, for an agent another machine's lead made, that lead, with what the
// notice changes about the agent in ev (Status, Summary, PRURL).
func (s *Server) tellLeadAbout(ctx context.Context, a state.Agent, notice string, opts chat.NoticeOptions, ev peerEvent) {
	origin, err := s.store.PeerAgent(ctx, a.Project, a.Name)
	if err != nil {
		s.tellLeadOf(ctx, a.Project, notice, opts)
		return
	}
	ev.Project, ev.Agent, ev.Title, ev.Branch = origin.OriginProject, a.Name, a.Title, a.Branch
	ev.Notice, ev.Act = notice, opts.Act
	s.sendPeerEvent(origin.OriginID, origin.OriginName, ev)
}

// peerRetries are the waits between tries at telling a machine about its
// lead's agent, which may be offline for a while.
var peerRetries = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// sendPeerEvent tells another machine about an agent its lead made here, in
// the background, trying again while it can't be reached.
func (s *Server) sendPeerEvent(peerID, peerName string, ev peerEvent) {
	ctx := s.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		var err error
		for i := 0; ; i++ {
			cfg := s.readRemoteConfig()
			if cfg == nil {
				err = errNoHub
			} else {
				cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				err = s.peerCall(cctx, cfg, peerID, http.MethodPost, "/v1/peer/events", "", "", ev, nil)
				cancel()
				if err == nil {
					return
				}
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
		s.logf("peers: couldn't tell %s about %s (%s): %v", peerName, ev.Agent, ev.Status, err)
	}()
}

// remoteAgents are a project's agents on other machines, for its fleet.
func (s *Server) remoteAgents(ctx context.Context, project string) []api.RemoteAgent {
	rows, err := s.store.RemoteAgents(ctx, project)
	if err != nil {
		s.logf("listing %s's agents on other machines: %v", project, err)
		return nil
	}
	var out []api.RemoteAgent
	for _, a := range rows {
		out = append(out, api.RemoteAgent{
			Machine: a.MachineName, MachineID: a.MachineID, Name: a.Agent, Title: a.Title, Branch: a.Branch,
			Status: a.Status, Summary: a.Summary, PRURL: a.PRURL, UpdatedAt: a.UpdatedAt,
		})
	}
	return out
}

// isPeerAgent reports whether another machine's lead made this agent.
func (s *Server) isPeerAgent(ctx context.Context, a state.Agent) bool {
	_, err := s.store.PeerAgent(ctx, a.Project, a.Name)
	return err == nil
}
