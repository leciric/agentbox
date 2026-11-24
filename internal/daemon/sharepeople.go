package daemon

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The people of a shared project (sharepeers.go, shareremote.go): who wrote a
// message, and who has what open now.
//
// Authors. A message a paired AgentBox sends (to a project's chat, to an
// agent, or an answer to an agent's question) carries its peer as author
// (shareAuthor), kept with the item. A message with none was written on the
// machine itself. The peer's daemon names people as its own app expects
// (remapPeople): itself as "" (its user), and the owner, whose own messages
// carry no author, by its remote id.
//
// Presence. Each app says what it has open (PUT /v1/here) every
// api.HereRenewSeconds, and at once when that changes; what isn't renewed in
// api.HereTTLSeconds is taken as closed (sweepHere). A paired AgentBox's
// daemon passes on what its user has open in the owner's projects to that
// owner (forwardHere), and the owner tells every peer that reaches the
// project who is in it, itself included, over the event stream (EventHere),
// which the peer's daemon keeps (remoteHere) for its app's GET /v1/here.
// Desktops and terminals are not locked: being there is only shown.

type hereState struct {
	mu      sync.Mutex
	entries map[hereKey]*hereEntry
	// remote is, by remote id, who its owner last said is in each of its
	// projects, named as they are here.
	remote map[string]map[string][]api.PersonHere
	// forwarded is, by this machine's client, the remote it last told what
	// that client has open, which is told when the client leaves it.
	forwarded map[string]string
	// pending and sending pass on one client's place at a time per remote,
	// the latest only, in order (sendHere).
	pending map[string]*api.HereRequest
	sending map[string]bool
}

func newHereState() *hereState {
	return &hereState{
		entries: map[hereKey]*hereEntry{}, remote: map[string]map[string][]api.PersonHere{},
		forwarded: map[string]string{}, pending: map[string]*api.HereRequest{}, sending: map[string]bool{},
	}
}

// hereKey is one window of one person: Person.ID ("" on this machine) and
// the client it named.
type hereKey struct{ person, client string }

type hereEntry struct {
	who     api.Person
	here    api.Here
	since   time.Time
	expires time.Time
}

const hereTTL = api.HereTTLSeconds * time.Second

// shareAuthor is the person a request came from when a paired AgentBox sent
// it; nil for one made on this machine.
func shareAuthor(ctx context.Context) *api.Person {
	if p, ok := sharePeerFrom(ctx); ok {
		return &api.Person{ID: p.ID, Name: p.Name}
	}
	return nil
}

func (s *Server) putHere(w http.ResponseWriter, r *http.Request) error {
	var req api.HereRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Client == "" || len(req.Client) > 200 {
		return errors.New("client must name the window, in up to 200 characters")
	}
	s.setHereFrom(r.Context(), req.Client, req.Here)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) leaveHere(w http.ResponseWriter, r *http.Request) error {
	s.setHereFrom(r.Context(), r.PathValue("client"), api.Here{})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// setHereFrom records what a client of the person a request came from has
// open, passing it on to the owner of a remote project it is in.
func (s *Server) setHereFrom(ctx context.Context, client string, here api.Here) {
	here.Project = strings.TrimSpace(here.Project)
	if here.Project == "" {
		here = api.Here{}
	}
	who := api.Person{Name: s.shareName(ctx)}
	peer, isPeer := sharePeerFrom(ctx)
	if isPeer {
		who = api.Person{ID: peer.ID, Name: peer.Name}
		if slug := s.projectSlug(ctx, here.Project); peer.Shares(slug) {
			here.Project = slug
		} else {
			here = api.Here{}
		}
	} else {
		s.forwardHere(client, here)
	}
	was, changed := s.setHere(hereKey{person: who.ID, client: client}, who, here)
	if isPeer && changed && here.Agent != "" && (was.Agent != here.Agent || was.Tab != here.Tab || was.Project != here.Project) {
		switch here.Tab {
		case "desktop":
			s.recordShareActivity(ctx, peer.ID, api.ShareDidOpenDesktop, here.Project, here.Agent, "")
		case "terminal":
			s.recordShareActivity(ctx, peer.ID, api.ShareDidOpenTerminal, here.Project, here.Agent, "")
		}
	}
}

// setHere records what one window has open (nothing, for a zero here), and
// tells the projects it was and is in when that changed. It returns what the
// window had open before.
func (s *Server) setHere(key hereKey, who api.Person, here api.Here) (was api.Here, changed bool) {
	sh := s.peering.here
	now := time.Now()
	sh.mu.Lock()
	old := sh.entries[key]
	live := old != nil && now.Before(old.expires)
	if live {
		was = old.here
	}
	changed = was != here || (live && here.Project != "" && old.who.Name != who.Name)
	switch {
	case here.Project == "":
		delete(sh.entries, key)
	case live && samePlace(old.here, here):
		old.here, old.who, old.expires = here, who, now.Add(hereTTL)
	default:
		sh.entries[key] = &hereEntry{who: who, here: here, since: now, expires: now.Add(hereTTL)}
	}
	sh.mu.Unlock()
	if changed {
		s.publishHere(was.Project, here.Project)
	}
	return was, changed
}

func samePlace(a, b api.Here) bool {
	return a.Project == b.Project && a.Session == b.Session && a.Agent == b.Agent && a.Tab == b.Tab
}

// publishHere tells who is in each of projects now, those of this machine
// only: a remote project's people come from its owner.
func (s *Server) publishHere(projects ...string) {
	done := map[string]bool{}
	for _, p := range projects {
		if p == "" || done[p] || strings.Contains(p, remoteSep) {
			continue
		}
		done[p] = true
		s.events.publish(api.EventHere, api.ProjectHere{Project: p, People: s.peopleIn(p)})
	}
}

// peopleIn is who is in a project of this machine now: each person once, at
// the place of the window they last moved in, the longest there first.
func (s *Server) peopleIn(project string) []api.PersonHere {
	sh := s.peering.here
	now := time.Now()
	sh.mu.Lock()
	byPerson := map[string]*hereEntry{}
	for key, e := range sh.entries {
		if e.here.Project != project || now.After(e.expires) {
			continue
		}
		if had := byPerson[key.person]; had == nil || e.since.After(had.since) {
			byPerson[key.person] = e
		}
	}
	sh.mu.Unlock()
	out := []api.PersonHere{}
	for _, e := range byPerson {
		out = append(out, api.PersonHere{Person: e.who, Here: e.here, Since: e.since})
	}
	slices.SortFunc(out, func(a, b api.PersonHere) int {
		return cmp.Or(a.Since.Compare(b.Since), strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
	})
	return out
}

// hereProjects is every project of this machine someone is in now.
func (s *Server) hereProjects() []string {
	sh := s.peering.here
	now := time.Now()
	sh.mu.Lock()
	defer sh.mu.Unlock()
	var out []string
	for _, e := range sh.entries {
		if p := e.here.Project; now.Before(e.expires) && !strings.Contains(p, remoteSep) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// getHere is who is in each project, this machine's and the remote ones.
func (s *Server) getHere(w http.ResponseWriter, r *http.Request) error {
	out := []api.ProjectHere{}
	for _, p := range s.hereProjects() {
		out = append(out, api.ProjectHere{Project: p, People: s.peopleIn(p)})
	}
	sh := s.peering.here
	sh.mu.Lock()
	for _, projects := range sh.remote {
		for p, people := range projects {
			out = append(out, api.ProjectHere{Project: p, People: people})
		}
	}
	sh.mu.Unlock()
	slices.SortFunc(out, func(a, b api.ProjectHere) int { return strings.Compare(a.Project, b.Project) })
	return writeJSON(w, http.StatusOK, out)
}

// sweepHere forgets the windows that stopped renewing, and tells their
// projects.
func (s *Server) sweepHere(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		sh := s.peering.here
		now := time.Now()
		var gone []string
		sh.mu.Lock()
		for key, e := range sh.entries {
			if now.After(e.expires) {
				gone = append(gone, e.here.Project)
				delete(sh.entries, key)
			}
		}
		sh.mu.Unlock()
		s.publishHere(gone...)
	}
}

// forwardHere passes what one of this machine's windows has open on to the
// owner of the remote project it is in, and tells the owner of the one it was
// in before that it left.
func (s *Server) forwardHere(client string, here api.Here) {
	target := ""
	there := here
	if id, project, ok := remoteOf(here.Project); ok && s.remoteConn(id) != nil {
		target, there.Project = id, project
	}
	sh := s.peering.here
	sh.mu.Lock()
	was := sh.forwarded[client]
	if target == "" {
		delete(sh.forwarded, client)
	} else {
		sh.forwarded[client] = target
	}
	sh.mu.Unlock()
	if was != "" && was != target {
		s.sendHere(was, api.HereRequest{Client: client})
	}
	if target != "" {
		s.sendHere(target, api.HereRequest{Client: client, Here: there})
	}
}

// sendHere tells a remote what a client has open: one request at a time per
// client, the latest place only, so a quick "typing" and "stopped typing"
// can't arrive the wrong way round.
func (s *Server) sendHere(remote string, req api.HereRequest) {
	sh := s.peering.here
	key := remote + "\n" + req.Client
	sh.mu.Lock()
	sh.pending[key] = &req
	if sh.sending[key] {
		sh.mu.Unlock()
		return
	}
	sh.sending[key] = true
	sh.mu.Unlock()
	go func() {
		for {
			sh.mu.Lock()
			next := sh.pending[key]
			delete(sh.pending, key)
			if next == nil {
				delete(sh.sending, key)
				sh.mu.Unlock()
				return
			}
			sh.mu.Unlock()
			c := s.remoteConn(remote)
			if c == nil {
				continue
			}
			body, _ := json.Marshal(next)
			base := s.runCtx
			if base == nil {
				base = context.Background()
			}
			ctx, cancel := context.WithTimeout(base, 10*time.Second)
			resp, err := c.request(ctx, s.peering.remotes.client, http.MethodPut, "/v1/here", bytes.NewReader(body))
			if err == nil {
				_ = resp.Body.Close()
			}
			cancel()
		}
	}()
}

// remoteHere keeps who a remote's owner says is in one of its projects
// (EventHere's data, already named as here), for GET /v1/here.
func (s *Server) remoteHere(remote string, data json.RawMessage) {
	var ph api.ProjectHere
	if json.Unmarshal(data, &ph) != nil || ph.Project == "" {
		return
	}
	sh := s.peering.here
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.remote[remote] == nil {
		sh.remote[remote] = map[string][]api.PersonHere{}
	}
	if len(ph.People) == 0 {
		delete(sh.remote[remote], ph.Project)
	} else {
		sh.remote[remote][ph.Project] = ph.People
	}
}

// forgetRemoteHere drops what a remote said of its projects' people, when
// its stream ends or it is unpaired: nobody is known to be there until it
// says again.
func (s *Server) forgetRemoteHere(remote string) {
	sh := s.peering.here
	sh.mu.Lock()
	projects := sh.remote[remote]
	delete(sh.remote, remote)
	sh.mu.Unlock()
	for p := range projects {
		s.events.publish(api.EventHere, api.ProjectHere{Project: p, People: []api.PersonHere{}})
	}
}

// hereFor is who is in each of the projects a peer reaches, for the start of
// its event stream.
func (s *Server) hereFor(p state.SharePeer) []api.ProjectHere {
	var out []api.ProjectHere
	for _, project := range s.hereProjects() {
		if p.Shares(project) {
			out = append(out, api.ProjectHere{Project: project, People: s.peopleIn(project)})
		}
	}
	return out
}

// selfPeer is the id the owner knows this AgentBox by: its token is
// "share_<peer id>.<secret>" (state.AddSharePeer).
func (c *remoteConn) selfPeer() string {
	id, _, _ := strings.Cut(strings.TrimPrefix(c.token, "share_"), ".")
	return id
}

// remapPeople names the people in what a remote's owner sent as this
// AgentBox's app expects (api.Person): this AgentBox itself as "", and the
// owner, whose own messages carry no author and whose own presence has the
// id "", by the remote's id. A message somebody sent is a user item, an
// aside, a notice told to an agent, or a question's answer by a user.
func remapPeople(v any, c *remoteConn) any {
	self := c.selfPeer()
	person := func(p map[string]any) {
		switch id, _ := p["id"].(string); id {
		case self:
			p["id"] = ""
		case "":
			p["id"] = c.r.ID
			if name, _ := p["name"].(string); name == "" {
				p["name"] = c.r.Name
			}
		}
	}
	authored := func(x map[string]any) {
		a, ok := x["author"].(map[string]any)
		if !ok {
			x["author"] = map[string]any{"id": c.r.ID, "name": c.r.Name}
			return
		}
		if id, _ := a["id"].(string); id == self {
			delete(x, "author")
			return
		}
		person(a)
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, it := range x {
				walk(it)
			}
		case map[string]any:
			kind, _ := x["kind"].(string)
			_, isItem := x["turn"]
			to, _ := x["to"].(string)
			if isItem && (kind == "user" || kind == "aside" || (kind == "notice" && to != "")) {
				woken, _ := x["woken"].(bool)
				hidden, _ := x["hidden"].(bool)
				if !woken && !hidden {
					authored(x)
				}
			}
			if by, _ := x["answeredBy"].(string); by == "user" {
				authored(x)
			}
			if people, ok := x["people"].([]any); ok {
				for _, p := range people {
					if m, ok := p.(map[string]any); ok {
						person(m)
					}
				}
			}
			for k, val := range x {
				if k != "people" && k != "author" {
					walk(val)
				}
			}
		}
	}
	walk(v)
	return v
}

// fromRemote is what a remote's owner sent, named as it is known here: its
// projects (renameJSON) and its people (remapPeople).
func fromRemote(data json.RawMessage, c *remoteConn) json.RawMessage {
	if len(data) == 0 || (data[0] != '{' && data[0] != '[') {
		return data
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return data
	}
	out, err := json.Marshal(remapPeople(renameValue(v, c.r.ID), c))
	if err != nil {
		return data
	}
	return out
}
