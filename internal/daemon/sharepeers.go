package daemon

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"

	"github.com/coder/websocket"
)

// Sharing projects with another AgentBox. The owner's side is here; the
// side of the AgentBox that paired with an owner is shareremote.go.
//
// Pairing reuses the phone page (lan.go): the owner makes a one-time link
// under Settings → Share (POST /v1/share/pairings), its phone page's address
// with a secret in the fragment, valid for pairingTTL. The other AgentBox,
// the peer, posts the secret to /share/pair at that address and gets a token
// of its own; state keeps its hash (state.AddSharePeer). It reaches the page
// as a phone does, over the local network or the Cloudflare Tunnel, with the
// token in an Authorization header, under /share/api/.
//
// A peer reaches only the projects it was given (all of them, or a list),
// and in them what a local user does (shareRouteOf): their chats, agents,
// events, terminals and files. Never the machine's secrets, accounts,
// settings or other projects, nor making, changing or removing a project.
// The lists it reads (projects, agents, the event stream) are cut down to
// its projects.
//
// Agents a peer makes run on this machine, on its AI accounts: so many at a
// time as its slots allow (checkShareSlots), its own limit or the one set
// for every peer. An agent counts as the peer's when the peer made it, or
// when a project chat made it after the peer was the last to write in that
// session of the chat (peeringState.speaker). A create takes its slot when
// it is accepted, before its machine runs (holdShareSlot).

type peeringState struct {
	mu       sync.Mutex
	pairings map[string]time.Time                         // one-time secrets, to when each expires
	open     map[string]map[*context.CancelFunc]shareOpen // what each peer has open (track)
	seen     map[string]time.Time                         // when each peer's last visit was recorded
	// speaker is, by "project/session", the peer that wrote last in that
	// session of a project's chat, until someone here does (heardInChat).
	speaker map[string]string
	// heard is, by the same key, which of heardInChat's writes (heardN
	// counts them) was the last, so undoing one never undoes a later one.
	heard  map[string]uint64
	heardN uint64
	// holds are the slots of agents being made for peers, by id, from when
	// their create is accepted until its job ends (holdShareSlot).
	holds map[string]*shareHold
	// slotMu makes counting a peer's slots and holding one a single step, so
	// creates at once can't all take its last slot.
	slotMu sync.Mutex
	// running counts, of refs a peer made, those running now; nil is
	// shareRunning's. A field for tests.
	running func(ctx context.Context, refs []string) int

	remotes *shareRemotes // shareremote.go
	here    *hereState    // sharepeople.go

	// machines are the peers' computers connected to take agents, by peer
	// (sharemachines.go); runOn is, by "project/session", the peer that chose
	// its own computer for the agents that session makes, and runOnTold the
	// last choice its lead was told, as "<peer>/<runOn>".
	machines  map[string]*shareMachine
	runOn     map[string]string
	runOnTold map[string]string
}

func newPeeringState() *peeringState {
	return &peeringState{
		pairings: map[string]time.Time{}, open: map[string]map[*context.CancelFunc]shareOpen{},
		seen: map[string]time.Time{}, speaker: map[string]string{}, heard: map[string]uint64{}, holds: map[string]*shareHold{},
		remotes: newShareRemotes(), here: newHereState(),
		machines: map[string]*shareMachine{}, runOn: map[string]string{}, runOnTold: map[string]string{},
	}
}

// shareOpen is what one of a peer's open requests is in.
type shareOpen struct {
	project string // "" for one in no project
	events  bool   // its event stream
	machine bool   // its computer's connection to take agents (sharemachines.go)
}

// track records a peer's request as open until untrack, and says whether its
// visit is to be recorded (once a minute at most).
func (sh *peeringState) track(peer string, o shareOpen, cancel *context.CancelFunc) (record bool, untrack func()) {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.open[peer] == nil {
		sh.open[peer] = map[*context.CancelFunc]shareOpen{}
	}
	sh.open[peer][cancel] = o
	if record = time.Since(sh.seen[peer]) > time.Minute; record {
		sh.seen[peer] = time.Now()
	}
	return record, func() {
		sh.mu.Lock()
		defer sh.mu.Unlock()
		delete(sh.open[peer], cancel)
		if len(sh.open[peer]) == 0 {
			delete(sh.open, peer)
		}
	}
}

// end cancels those of a peer's open requests that ends picks.
func (sh *peeringState) end(peer string, ends func(shareOpen) bool) {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	for cancel, o := range sh.open[peer] {
		if ends(o) {
			(*cancel)()
		}
	}
}

// sharePeerKey is the context key of the peer a request came from.
type sharePeerKey struct{}

func sharePeerFrom(ctx context.Context) (state.SharePeer, bool) {
	p, ok := ctx.Value(sharePeerKey{}).(state.SharePeer)
	return p, ok
}

func (s *Server) shareName(ctx context.Context) string {
	if v, _ := s.store.Setting(ctx, state.SettingShareName); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "AgentBox"
}

func (s *Server) shareSlots(ctx context.Context) int {
	v, err := s.store.Setting(ctx, state.SettingShareSlots)
	if err != nil || v == "" {
		return state.DefaultShareSlots
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > state.MaxShareSlots {
		return state.DefaultShareSlots
	}
	return n
}

func (s *Server) publishShare(context.Context) {
	s.events.publish(api.EventShare, struct{}{})
}

func (s *Server) shareStatus(ctx context.Context) (api.ShareStatus, error) {
	out := api.ShareStatus{Name: s.shareName(ctx), Slots: s.shareSlots(ctx), Peers: []api.SharePeer{}, Remotes: []api.ShareRemote{}}
	lan, err := s.lanStatus(ctx)
	if err != nil {
		return api.ShareStatus{}, err
	}
	switch {
	case !lan.Enabled:
		out.Error = "turn on chatting from a phone first, under Phones: another AgentBox pairs through the same page"
	case len(lan.URLs) == 0:
		out.Error = cmp.Or(lan.Error, lan.Tunnel.Error, "there's no address another AgentBox could reach yet")
	default:
		out.Ready = true
	}
	peers, err := s.store.SharePeers(ctx)
	if err != nil {
		return api.ShareStatus{}, err
	}
	for _, p := range peers {
		out.Peers = append(out.Peers, s.sharePeerInfo(ctx, p))
	}
	remotes, err := s.store.ShareRemotes(ctx)
	if err != nil {
		return api.ShareStatus{}, err
	}
	for _, r := range remotes {
		out.Remotes = append(out.Remotes, s.shareRemoteInfo(r))
	}
	return out, nil
}

func (s *Server) sharePeerInfo(ctx context.Context, p state.SharePeer) api.SharePeer {
	refs, _ := s.store.SharePeerAgents(ctx, p.ID)
	usage, err := s.store.SharePeerUsage(ctx, p.ID)
	if err != nil {
		s.logf("share: what %s spent: %v", p.Name, err)
	}
	return api.SharePeer{
		ID: p.ID, Name: p.Name, Paired: p.Created, LastSeen: p.LastSeen, LastAddr: p.LastAddr,
		AllProjects: p.AllProjects, Projects: p.Projects, Slots: p.Slots, Running: s.shareRunningCount(ctx, refs),
		Access: p.Access, OwnMachine: p.OwnMachine, Machine: s.shareMachine(p.ID) != nil,
		Usage: api.SharePeerUsage{Tokens: usage.Tokens, CostUSD: usage.CostUSD, AgentMinutes: usage.RunSeconds / 60},
	}
}

func (s *Server) getShare(w http.ResponseWriter, r *http.Request) error {
	st, err := s.shareStatus(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, st)
}

func (s *Server) updateShare(w http.ResponseWriter, r *http.Request) error {
	var req api.UpdateShareRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if req.Slots != nil {
		if *req.Slots < 0 || *req.Slots > state.MaxShareSlots {
			return fmt.Errorf("slots must be between 0 and %d, not %d", state.MaxShareSlots, *req.Slots)
		}
		if err := s.store.SetSetting(ctx, state.SettingShareSlots, strconv.Itoa(*req.Slots)); err != nil {
			return err
		}
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if len([]rune(name)) > state.MaxPhoneNameLen {
			return fmt.Errorf("the name is longer than %d characters", state.MaxPhoneNameLen)
		}
		if err := s.store.SetSetting(ctx, state.SettingShareName, name); err != nil {
			return err
		}
	}
	s.publishShare(ctx)
	return s.getShare(w, r)
}

func (s *Server) addSharePairing(w http.ResponseWriter, r *http.Request) error {
	st, err := s.shareStatus(r.Context())
	if err != nil {
		return err
	}
	if !st.Ready {
		return errors.New(st.Error)
	}
	lan, err := s.lanStatus(r.Context())
	if err != nil {
		return err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	out := api.SharePairing{Expires: time.Now().Add(pairingTTL)}
	for _, u := range lan.URLs {
		out.Links = append(out.Links, u+"#share="+secret)
	}
	out.Link = out.Links[0]
	sh := s.peering
	sh.mu.Lock()
	for k, exp := range sh.pairings {
		if time.Now().After(exp) {
			delete(sh.pairings, k)
		}
	}
	sh.pairings[secret] = out.Expires
	sh.mu.Unlock()
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateSharePeer(w http.ResponseWriter, r *http.Request) error {
	var req api.UpdateSharePeerRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	p, err := s.store.SharePeer(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	all, projects, slots := p.AllProjects, p.Projects, p.Slots
	if req.AllProjects != nil {
		all = *req.AllProjects
	}
	if req.Projects != nil {
		projects = nil
		for _, ref := range *req.Projects {
			proj, err := s.store.Project(ctx, ref)
			if err != nil {
				return err
			}
			projects = append(projects, proj.Name)
		}
	}
	if req.Slots != nil {
		slots = *req.Slots
	}
	was := p
	if req.Access != nil && *req.Access != p.Access {
		if err := s.store.SetSharePeerAccess(ctx, p.ID, *req.Access); err != nil {
			return err
		}
	}
	if p, err = s.store.SetSharePeer(ctx, p.ID, all, projects, slots); err != nil {
		return err
	}
	if req.OwnMachine != nil && *req.OwnMachine != p.OwnMachine {
		if p, err = s.store.SetSharePeerOwnMachine(ctx, p.ID, *req.OwnMachine); err != nil {
			return err
		}
	}
	s.logf("share: %s (%s) reaches %s, slots %d, access %s", p.Name, p.ID, shareWhat(p), p.Slots, p.Access)
	// What it had open in a project it no longer reaches ends. Its event
	// stream ends when what it reaches changed at all: it connects again at
	// once, and lists the projects it reaches now. Its slots changing ends
	// nothing.
	reaches := was.AllProjects != p.AllProjects || !slices.Equal(was.Projects, p.Projects) || was.Access != p.Access
	s.peering.end(p.ID, func(o shareOpen) bool {
		return (o.events && reaches) || (o.project != "" && !p.Shares(o.project)) || (o.machine && !p.OwnMachine)
	})
	s.publishShare(ctx)
	return writeJSON(w, http.StatusOK, s.sharePeerInfo(ctx, p))
}

func shareWhat(p state.SharePeer) string {
	if p.AllProjects {
		return "every project"
	}
	if len(p.Projects) == 0 {
		return "no project"
	}
	return strings.Join(p.Projects, ", ")
}

func (s *Server) removeSharePeer(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if err := s.store.RemoveSharePeer(r.Context(), id); err != nil {
		return err
	}
	sh := s.peering
	sh.end(id, func(shareOpen) bool { return true })
	sh.mu.Lock()
	delete(sh.seen, id)
	for k, v := range sh.speaker {
		if v == id {
			delete(sh.speaker, k)
		}
	}
	for k, v := range sh.runOn {
		if v == id {
			delete(sh.runOn, k)
		}
	}
	sh.mu.Unlock()
	s.publishShare(r.Context())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// sharePair is a peer's daemon pairing with the secret of a link.
func (s *Server) sharePair(w http.ResponseWriter, r *http.Request, addr string) {
	if s.lan.limiter.Blocked(addr, true) {
		s.tooMany(w)
		return
	}
	var req api.SharePairRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := readJSON(r, &req); err != nil {
		writeLANError(w, http.StatusBadRequest, err.Error())
		return
	}
	sh := s.peering
	sh.mu.Lock()
	expires, ok := sh.pairings[req.Secret]
	delete(sh.pairings, req.Secret)
	sh.mu.Unlock()
	if req.Secret == "" || !ok || time.Now().After(expires) {
		s.lan.limiter.Fail(addr)
		s.logf("share: a pairing from %s with a link that has expired, was used, or never was", addr)
		writeLANError(w, http.StatusForbidden, "this link has expired or was used already: make a new one in AgentBox's settings, under Share")
		return
	}
	p, token, err := s.store.AddSharePeer(r.Context(), req.Name)
	if err != nil {
		writeLANError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.store.SawSharePeer(r.Context(), p.ID, addr, time.Now())
	s.logf("share: paired %s (%s) from %s; it reaches no project until you choose", p.Name, p.ID, addr)
	s.publishShare(r.Context())
	_ = writeJSON(w, http.StatusOK, api.SharePairResponse{Token: token, Owner: s.shareName(r.Context())})
}

// sharePeerOf is the peer a request's bearer token says it is.
func (s *Server) sharePeerOf(w http.ResponseWriter, r *http.Request, addr string) (state.SharePeer, bool) {
	token, ok := lanBearer(r)
	if ok && strings.HasPrefix(token, "share_") {
		if s.lan.limiter.Blocked(addr, false) {
			s.tooMany(w)
			return state.SharePeer{}, false
		}
		p, err := s.store.SharePeerByToken(r.Context(), token)
		if err == nil {
			return p, true
		}
		if !errors.Is(err, state.ErrNotFound) {
			writeLANError(w, http.StatusInternalServerError, err.Error())
			return state.SharePeer{}, false
		}
		s.lan.limiter.Fail(addr)
	}
	writeLANError(w, http.StatusUnauthorized, "this AgentBox isn't paired, or was unpaired: pair it again with a new link")
	return state.SharePeer{}, false
}

// shareAPI passes a peer's request on to the API, if it reaches the project
// the request is in, and ends it when the peer is unpaired.
func (s *Server) shareAPI(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := s.sharePeerOf(w, r, addr)
	if !ok {
		return
	}
	ctx := r.Context()
	path := strings.TrimPrefix(r.URL.Path, "/share/api")
	route := shareRouteOf(r.Method, path)
	var body []byte
	switch {
	case route.never != "":
		writeLANError(w, http.StatusForbidden, fmt.Sprintf("a paired AgentBox can't reach %s", route.never))
		return
	case route.bodyProject:
		var err error
		if body, err = io.ReadAll(io.LimitReader(r.Body, 1<<20)); err != nil {
			writeLANError(w, http.StatusBadRequest, err.Error())
			return
		}
		var req struct {
			Project string `json:"project"`
		}
		_ = json.Unmarshal(body, &req)
		route.project = req.Project
	case route.project == "" && route.global == "":
		writeLANError(w, http.StatusForbidden, fmt.Sprintf("%s %s isn't shared with another AgentBox: it works on the AgentBox that shares the project", r.Method, path))
		return
	}
	if !p.May(route.needs) {
		writeLANError(w, http.StatusForbidden, fmt.Sprintf("%s can't %s; its owner chooses what it may do, in Settings → Share", p.Name, shareCant(route.needs)))
		return
	}
	if route.project != "" {
		slug := route.project
		if proj, err := s.store.Project(ctx, slug); err == nil {
			slug = proj.Name
		}
		if !p.Shares(slug) {
			writeLANError(w, http.StatusForbidden, fmt.Sprintf("project %s isn't shared with %s", route.project, p.Name))
			return
		}
		route.project = slug
		// The route goes on naming the project by the slug that was
		// checked, whatever case or name the peer used for it.
		if m := shareProjectRoute.FindStringSubmatch(path); m != nil && !route.bodyProject {
			path = "/v1/" + m[1] + "/" + slug + m[3]
		}
	}
	if route.agent != "" {
		hold, err := s.checkShareStart(ctx, p, route.project, route.agent)
		if err != nil {
			writeError(w, err)
			return
		}
		defer s.releaseShareSlot(hold)
	}

	ctx, cancel := context.WithCancel(context.WithValue(ctx, sharePeerKey{}, p))
	defer cancel()
	record, untrack := s.peering.track(p.ID, shareOpen{project: route.project, events: route.global == "events"}, &cancel)
	defer untrack()
	if record {
		if err := s.store.SawSharePeer(ctx, p.ID, addr, time.Now()); err == nil {
			s.publishShare(ctx)
		}
	}
	if route.global == "events" {
		s.shareEvents(w, r.WithContext(ctx), p)
		return
	}
	s.lan.mu.Lock()
	if s.lan.api == nil {
		s.lan.api = s.routes()
	}
	routes := s.lan.api
	s.lan.mu.Unlock()
	r2 := r.Clone(ctx)
	r2.URL.Path = path
	r2.URL.RawPath = ""
	if body != nil {
		r2.Body = io.NopCloser(bytes.NewReader(body))
		r2.ContentLength = int64(len(body))
	} else if r.Method != http.MethodGet && r.Method != http.MethodHead {
		r2.Body = http.MaxBytesReader(w, r.Body, 96<<20)
	}
	if route.global == "projects" || route.global == "agents" {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, r2)
		if rec.Code != http.StatusOK {
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		out, err := shareFilterList(rec.Body.Bytes(), p, route.global)
		if err != nil {
			writeLANError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
		return
	}
	if act, ok := shareActivityOf(r.Method, path); ok {
		sw := &statusWriter{ResponseWriter: w}
		routes.ServeHTTP(sw, r2)
		s.recordShareRequest(ctx, p, act, sw.status)
		return
	}
	routes.ServeHTTP(w, r2)
}

// shareFilterList cuts GET /v1/projects or /v1/agents down to the projects
// p reaches. Projects lose where they are on this machine.
func shareFilterList(body []byte, p state.SharePeer, what string) ([]byte, error) {
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, err
	}
	key := "project"
	if what == "projects" {
		key = "name"
	}
	out := []map[string]json.RawMessage{}
	for _, it := range items {
		var name string
		_ = json.Unmarshal(it[key], &name)
		if !p.Shares(name) {
			continue
		}
		if what == "projects" {
			it["root"] = json.RawMessage(`""`)
			it["envFiles"] = json.RawMessage(`[]`)
			// What it may do in it, which its daemon moves into the
			// project's remote (withRemote) for its app.
			it[shareAccessKey], _ = json.Marshal(p.Access)
		}
		out = append(out, it)
	}
	return json.Marshal(out)
}

// shareAccessKey carries a peer's access in the projects the gateway lists.
const shareAccessKey = "shareAccess"

// eventProject is the project an event is about: its data's project, or the
// project of the agent its ref (a job's target, a chat event's agent) names;
// "" for one about no
// project, which no peer is sent.
func eventProject(data json.RawMessage) string {
	var d struct {
		Project string `json:"project"`
		Ref     string `json:"ref"`
		Target  string `json:"target"`
		Agent   string `json:"agent"` // a chat event's
	}
	if json.Unmarshal(data, &d) != nil {
		return ""
	}
	if d.Project != "" {
		return d.Project
	}
	for _, ref := range []string{d.Ref, d.Target, d.Agent} {
		if project, _, ok := strings.Cut(ref, "/"); ok {
			return project
		}
	}
	return ""
}

// shareEvents is the event stream as a peer sees it: only its projects'.
//
// A peer asks for it as a WebSocket (remoteEventsOnce), each event a message
// sent the moment it happens. Server-sent events, which a peer from before
// asks for, are held back by a Cloudflare quick tunnel until some 128 KB of
// the stream are waiting ("Quick Tunnels do not support Server-Sent Events",
// Cloudflare's docs say): a lead's reply, a few hundred bytes a word, reached
// a peer seconds late, in bursts, while a WebSocket through the same tunnel
// takes milliseconds.
func (s *Server) shareEvents(w http.ResponseWriter, r *http.Request, p state.SharePeer) {
	// Subscribed first, so nothing published once the peer sees the
	// stream open is missed.
	events, unsubscribe := s.events.subscribe()
	defer unsubscribe()
	ctx := r.Context()
	var send func(api.Event) error
	var beat func() error
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return // Accept has answered
		}
		defer func() { _ = conn.CloseNow() }()
		// The peer sends nothing: reading only answers its pings and sees
		// it go, which ends ctx.
		ctx = conn.CloseRead(ctx)
		send = func(ev api.Event) error {
			data, _ := json.Marshal(ev)
			wctx, cancel := context.WithTimeout(ctx, shareWriteTimeout)
			defer cancel()
			return conn.Write(wctx, websocket.MessageText, data)
		}
		// A message, not a ping: what the peer reads, so it can tell a
		// stream gone silent (remoteSilence) from one with nothing to say.
		beat = func() error { return send(api.Event{Type: shareBeat, Time: time.Now(), Data: json.RawMessage(`{}`)}) }
	} else {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeLANError(w, http.StatusInternalServerError, "streaming isn't supported on this connection")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		send = func(ev api.Event) error {
			data, _ := json.Marshal(ev)
			_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
			flusher.Flush()
			return err
		}
		beat = func() error {
			_, err := fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
			return err
		}
		_, _ = fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()
	}
	for _, change := range s.agentStates() {
		if project, _, _ := strings.Cut(change.Ref, "/"); p.Shares(project) {
			data, _ := json.Marshal(change)
			if send(api.Event{Type: api.EventAgent, Time: time.Now(), Data: data}) != nil {
				return
			}
		}
	}
	for _, ph := range s.hereFor(p) {
		data, _ := json.Marshal(ph)
		if send(api.Event{Type: api.EventHere, Time: time.Now(), Data: data}) != nil {
			return
		}
	}
	keepalive := time.NewTicker(cmp.Or(s.keepalive, 15*time.Second))
	defer keepalive.Stop()
	// What it reaches is read again now and then, so a project unshared
	// stops reaching it without its stream starting over.
	reread := time.NewTicker(10 * time.Second)
	defer reread.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			if beat() != nil {
				return
			}
		case <-reread.C:
			if now, err := s.store.SharePeer(ctx, p.ID); err == nil {
				p = now
			}
		case ev := <-events:
			if ev.Type == api.EventLAN || ev.Type == api.EventShare || !shareNoticeFor(ev.Type, ev.Data, p.ID) {
				continue
			}
			if p.Shares(eventProject(ev.Data)) && send(ev) != nil {
				return
			}
		}
	}
}

// shareBeat is the type of the message a peer's event stream sends when it
// has had nothing to say for a while, over a WebSocket.
const shareBeat = "ping"

// shareWriteTimeout is how long an event may take to reach a peer before its
// stream is taken for lost and ended: the peer connects again.
const shareWriteTimeout = 30 * time.Second

// shareRoute is what a peer's request is: one no peer may make (never says
// what it would reach), one in a project, which the peer must have been
// given, or one of the lists the gateway cuts down to the peer's projects.
type shareRoute struct {
	never   string
	project string
	// global is "projects", "agents", "events" or "version".
	global string
	// bodyProject says the project is in the request's body (POST
	// /v1/agents), which makes an agent: checkCreate holds its slot.
	bodyProject bool
	// agent is the agent it starts again, which the peer's slots count
	// when it is the peer's: checkShareStart.
	agent string
	// needs is the access a peer must have been given to take it
	// (state.ShareAccessView and the rest): shareNeeds.
	needs string
}

// shareNever is refused to every peer, in any project it was given: what
// hands over the machine, its secrets or accounts, pairs more devices, or
// makes, changes and removes projects. Matched against "METHOD /path".
var shareNever = []struct {
	what string
	re   *regexp.Regexp
}{
	{"secrets and credentials", regexp.MustCompile(`^\S+ (/v1/auth(/|$)|.*/(secrets|credentials?|browser-cookies)(/|$))`)},
	{"connectors", regexp.MustCompile(`^\S+ .*/connectors?(/|$)`)},
	{"pairing and the phone, share, remote and tunnel settings", regexp.MustCompile(`^\S+ /v1/(lan|remote|share)(/|$)`)},
	{"adding, changing or removing projects", regexp.MustCompile(`^(POST /v1/projects|\S+ /v1/projects/(layout|clone)|PATCH /v1/projects/[^/]+|DELETE /v1/projects/[^/]+|POST /v1/projects/[^/]+/retire|(POST|DELETE) /v1/projects/[^/]+/base(/revert)?)/?$`)},
	{"the project's memory shared with every project", regexp.MustCompile(`^POST /v1/projects/[^/]+/memory/(share|promote)$`)},
	{"stopping agents, or deleting past agents, of every project at once", regexp.MustCompile(`^POST /v1/(agents/stop|archived-agents/delete)/?$`)},
	{"forking or remaking agents, which its slots can't count yet: make a new agent", regexp.MustCompile(`^POST /v1/agents/[^/]+/[^/]+/(fork|recreate)/?$`)},
	{"scheduled tasks, whose agents its slots can't count", regexp.MustCompile(`^\S+ /v1/projects/[^/]+/schedules(/|$)`)},
}

// shareProjectRoute is a route in one project: the project's, its sessions'
// and its agents', past agents included.
var shareProjectRoute = regexp.MustCompile(`^/v1/(projects|agents|archived-agents)/([^/]+)(/.*)?$`)

// shareStart is starting an agent's machine again.
var shareStart = regexp.MustCompile(`^POST /v1/agents/[^/]+/([^/]+)/(start|resume)$`)

func shareRouteOf(method, path string) shareRoute {
	r := shareRouteIn(method, path)
	if r.never == "" {
		r.needs = shareNeeds(method, path)
	}
	return r
}

// shareChatRoute is what a peer given ShareAccessChat may do besides reading:
// write in a project's chat (any session of it) and in an agent's, stop or
// answer what it is doing, start and tend the chat's sessions, and answer an
// agent's question.
var shareChatRoute = regexp.MustCompile(`^(POST|PUT|PATCH|DELETE) /v1/(` +
	`projects/[^/]+(/sessions/[^/]+)?/chat/(messages|cancel|start|permissions/[^/]+|options/[^/]+)` +
	`|agents/[^/]+/[^/]+/chat/(messages|cancel|start|permissions/[^/]+|options/[^/]+)` +
	`|projects/[^/]+/questions/[^/]+/answer` +
	`|projects/[^/]+/sessions(/[^/]+(/(snooze|close|reopen))?)?` +
	`)/?$`)

// shareViewRoute is what a peer that may only look still sends: saying what
// it has open (sharepeople.go), that it read a session, and that it opened
// an agent (which wakes one stopped while idle, as opening it here does).
var shareViewRoute = regexp.MustCompile(`^(PUT /v1/here|DELETE /v1/here/[^/]+|POST /v1/projects/[^/]+/sessions/[^/]+/seen|PUT /v1/agents/[^/]+/[^/]+/viewing)/?$`)

// shareHandsOn are the reads that hand over the machine: an agent's
// terminal and its desktops, whose streams carry keys and clicks back.
var shareHandsOn = regexp.MustCompile(`^GET /v1/agents/[^/]+/[^/]+/(terminal|browser/view|ios/view|android/stream)/?$`)

// shareNeeds is the access a peer needs for a route no shareNever refuses:
// reading needs ShareAccessView; the chat's routes ShareAccessChat; anything
// else, which makes, changes or runs something on this machine (agents,
// merges, pull requests, files, terminals and desktops), ShareAccessFull.
func shareNeeds(method, path string) string {
	line := method + " " + path
	switch {
	case shareHandsOn.MatchString(line):
		return state.ShareAccessFull
	case method == http.MethodGet || method == http.MethodHead || shareViewRoute.MatchString(line):
		return state.ShareAccessView
	case shareChatRoute.MatchString(line):
		return state.ShareAccessChat
	}
	return state.ShareAccessFull
}

// shareCant is what a level of access doesn't let a peer do, for the error
// it gets.
func shareCant(need string) string {
	if need == state.ShareAccessChat {
		return "write in this project's chats: it may only look"
	}
	return "make or change things in this project (agents, merges, pull requests, terminals, desktops)"
}

func shareRouteIn(method, path string) shareRoute {
	if strings.Contains(path, "/../") || strings.HasSuffix(path, "/..") || strings.Contains(path, "//") {
		return shareRoute{}
	}
	line := method + " " + path
	for _, n := range shareNever {
		if n.re.MatchString(line) {
			return shareRoute{never: n.what}
		}
	}
	get := method == http.MethodGet || method == http.MethodHead
	switch {
	case get && path == "/v1/projects":
		return shareRoute{global: "projects"}
	case get && path == "/v1/agents":
		return shareRoute{global: "agents"}
	case get && path == "/v1/events":
		return shareRoute{global: "events"}
	case get && path == "/v1/version":
		return shareRoute{global: "version"}
	case method == http.MethodPut && path == "/v1/here", method == http.MethodDelete && strings.HasPrefix(path, "/v1/here/") && strings.Count(path, "/") == 3:
		return shareRoute{global: "here"}
	case method == http.MethodPost && path == "/v1/agents":
		return shareRoute{bodyProject: true}
	}
	m := shareProjectRoute.FindStringSubmatch(path)
	if m == nil {
		return shareRoute{}
	}
	project, err := url.PathUnescape(m[2])
	if err != nil || project == "" || project == state.HomeProject {
		return shareRoute{}
	}
	out := shareRoute{project: project}
	if st := shareStart.FindStringSubmatch(line); st != nil {
		if out.agent, err = url.PathUnescape(st[1]); err != nil || out.agent == "" {
			return shareRoute{}
		}
	}
	return out
}

// shareRunningCount is how many of refs run now: a machine whose state can't
// be read counts as running, so a peer is never let past its slots on a
// guess.
func (s *Server) shareRunningCount(ctx context.Context, refs []string) int {
	if len(refs) == 0 {
		return 0
	}
	if s.peering.running != nil {
		return s.peering.running(ctx, refs)
	}
	n := 0
	byProject := map[string][]string{}
	for _, ref := range refs {
		project, name, _ := strings.Cut(ref, "/")
		byProject[project] = append(byProject[project], name)
	}
	for project, names := range byProject {
		statuses, err := s.manager(nil).List(ctx, project)
		if err != nil {
			n += len(names)
			continue
		}
		for _, st := range statuses {
			if slices.Contains(names, st.Name) && st.State != "Stopped" && st.State != "stopped" {
				n++
			}
		}
	}
	return n
}

// shareSlotsFull refuses a peer one more agent.
type shareSlotsFull struct {
	peer           string
	running, slots int
}

func (e shareSlotsFull) Error() string {
	if e.slots == 0 {
		return fmt.Sprintf("%s may not run agents on this AgentBox: its owner gave it no slots (Settings → Share)", e.peer)
	}
	return fmt.Sprintf("%s already has %d of its %d agents running on this AgentBox: stop one of them first, or ask the owner for more slots (Settings → Share)", e.peer, e.running, e.slots)
}

// shareNoAgents refuses an agent to a peer whose access doesn't reach making
// one, whether it asked or the project's chat did after its message.
type shareNoAgents struct{ peer, access string }

func (e shareNoAgents) Error() string {
	may := "only look"
	if e.access == state.ShareAccessChat {
		may = "only chat"
	}
	return fmt.Sprintf("%s may not make agents on this AgentBox: its owner lets it %s (Settings → Share)", e.peer, may)
}

// shareRefused is what a paired AgentBox may not do in a project it was
// given, such as writing a memory every project of this machine reads.
type shareRefused struct{ what string }

func (e shareRefused) Error() string {
	return "a paired AgentBox can't " + e.what + ": the owner of this AgentBox can"
}

// refuseSharePeer is shareRefused when the request came from a peer.
func refuseSharePeer(ctx context.Context, what string) error {
	if _, ok := sharePeerFrom(ctx); ok {
		return shareRefused{what: what}
	}
	return nil
}

// shareHold is the slot of an agent being made for a peer: its create was
// accepted, and its job hasn't ended.
type shareHold struct {
	peer  string
	ref   string // the agent's, once its create has recorded it
	since time.Time
}

// shareHoldTTL is when a hold stops counting even if nothing released it, so
// one a bug left behind can't take a peer's slot for good. A create's job
// records its agent long before, which then counts by its machine.
const shareHoldTTL = time.Hour

// checkShareSlots refuses a peer an agent more than its slots allow: those
// of its agents that run, and those being made or started for it (holds),
// each once. Only under slotMu (holdShareSlot, checkShareStart) does what it
// says still hold when the caller acts on it.
//
// The holds are read before the peer's agents: a create records its agent
// before it names its hold's, and releases the hold after, so an agent whose
// hold is gone by then is among the agents read next, and none is missed.
func (s *Server) checkShareSlots(ctx context.Context, peerID string) error {
	p, err := s.store.SharePeer(ctx, peerID)
	if err != nil {
		return err
	}
	if !p.May(state.ShareAccessFull) {
		// Its messages may still have the project's chat make one for it:
		// refused the same, so what it may do isn't got round that way.
		return shareNoAgents{peer: p.Name, access: p.Access}
	}
	slots := p.Slots
	if slots < 0 {
		slots = s.shareSlots(ctx)
	}
	held, heldRefs := 0, map[string]bool{}
	sh := s.peering
	sh.mu.Lock()
	for id, h := range sh.holds {
		switch {
		case time.Since(h.since) > shareHoldTTL:
			delete(sh.holds, id)
		case h.peer == p.ID:
			held++
			heldRefs[h.ref] = true
		}
	}
	sh.mu.Unlock()
	refs, err := s.store.SharePeerAgents(ctx, p.ID)
	if err != nil {
		return err
	}
	refs = slices.DeleteFunc(refs, func(ref string) bool { return heldRefs[ref] })
	if running := s.shareRunningCount(ctx, refs) + held; running >= slots {
		return shareSlotsFull{peer: p.Name, running: running, slots: slots}
	}
	return nil
}

// holdShareSlot takes one of a peer's slots for an agent about to be made, or
// says why it can't: the slot is the agent's from now, before its machine
// runs, until releaseShareSlot.
func (s *Server) holdShareSlot(ctx context.Context, peerID string) (string, error) {
	sh := s.peering
	sh.slotMu.Lock()
	defer sh.slotMu.Unlock()
	if err := s.checkShareSlots(ctx, peerID); err != nil {
		return "", err
	}
	return s.addShareHold(peerID, "")
}

// addShareHold records a hold for one of a peer's agents (ref, once known):
// only under slotMu, once checkShareSlots let it through.
func (s *Server) addShareHold(peerID, ref string) (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	s.peering.mu.Lock()
	defer s.peering.mu.Unlock()
	s.peering.holds[id] = &shareHold{peer: peerID, ref: ref, since: time.Now()}
	return id, nil
}

// shareSlotMade says which agent a hold's create recorded: until the hold is
// released, the agent counts as the hold, not twice.
func (s *Server) shareSlotMade(id, ref string) {
	if id == "" {
		return
	}
	s.peering.mu.Lock()
	defer s.peering.mu.Unlock()
	if h := s.peering.holds[id]; h != nil {
		h.ref = ref
	}
}

// releaseShareSlot gives a hold's slot back: its create's job ended, made or
// not, or the create never started. Releasing twice is releasing once.
func (s *Server) releaseShareSlot(id string) {
	if id == "" || s.peering == nil {
		return
	}
	s.peering.mu.Lock()
	defer s.peering.mu.Unlock()
	delete(s.peering.holds, id)
}

// checkShareStart is holdShareSlot for starting one of the peer's own agents
// again (project is its slug): the hold lasts the start request, so two
// starts at once can't both pass, and the agent counts by its machine after.
// Starting the owner's agents, or one already running or being started,
// holds nothing ("").
func (s *Server) checkShareStart(ctx context.Context, p state.SharePeer, project, name string) (string, error) {
	ref := project + "/" + name
	refs, err := s.store.SharePeerAgents(ctx, p.ID)
	if err != nil || !slices.Contains(refs, ref) {
		return "", err
	}
	sh := s.peering
	sh.slotMu.Lock()
	defer sh.slotMu.Unlock()
	starting := false
	sh.mu.Lock()
	for _, h := range sh.holds {
		starting = starting || h.ref == ref
	}
	sh.mu.Unlock()
	if starting || s.shareRunningCount(ctx, []string{ref}) > 0 {
		return "", nil
	}
	if err := s.checkShareSlots(ctx, p.ID); err != nil {
		return "", err
	}
	return s.addShareHold(p.ID, ref)
}

// shareCreator is the peer an agent about to be made counts against: the one
// that asked for it, or the one that last wrote in the session of the chat
// that did.
func (s *Server) shareCreator(ctx context.Context, project, session string, byLead bool) string {
	if p, ok := sharePeerFrom(ctx); ok {
		return p.ID
	}
	if !byLead {
		return ""
	}
	if session == "" {
		session = state.MigratedSession
	}
	s.peering.mu.Lock()
	defer s.peering.mu.Unlock()
	return s.peering.speaker[project+"/"+session]
}

// heardInChat records who wrote last in the session of a project's chat that
// lead is, as the message lands there: the peer the request came from, whose
// the agents that session makes are then, or someone on this machine, whose
// they are again. It is set before the message goes in, so the turn it
// starts can't make an agent first; undo puts back what was there when the
// chat refuses the message, unless another message was heard since.
func (s *Server) heardInChat(ctx context.Context, lead state.Agent) (undo func()) {
	if s.peering == nil || !lead.IsLead() || lead.IsHome() {
		return func() {}
	}
	key := lead.Project + "/" + lead.Session()
	who := ""
	if p, ok := sharePeerFrom(ctx); ok {
		who = p.ID
	}
	sh := s.peering
	sh.mu.Lock()
	defer sh.mu.Unlock()
	was, had := sh.speaker[key]
	setSpeaker(sh.speaker, key, who)
	sh.heardN++
	mine := sh.heardN
	sh.heard[key] = mine
	return func() {
		sh.mu.Lock()
		defer sh.mu.Unlock()
		if sh.heard[key] == mine {
			if had {
				setSpeaker(sh.speaker, key, was)
			} else {
				delete(sh.speaker, key)
			}
		}
	}
}

// setSpeaker is speaker[key] = who, where no one ("") is no entry.
func setSpeaker(speaker map[string]string, key, who string) {
	if who == "" {
		delete(speaker, key)
	} else {
		speaker[key] = who
	}
}
