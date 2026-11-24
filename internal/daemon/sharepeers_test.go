package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// Two daemons: the owner shares one of its two projects with the other, which
// pairs with the owner's link and shows the project as its own.
func TestShareProjectsWithAnotherAgentBox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := startTestDaemon(t, t.TempDir(), fakeIncus)
	peer := startTestDaemon(t, t.TempDir(), fakeIncus)
	owner.srv.peering.running = func(context.Context, []string) int { return 0 } // none of the peer's agents run
	owner.srv.lan.mu.Lock()
	owner.srv.lan.own = true
	owner.srv.lan.addresses = func() []string { return []string{"127.0.0.1"} }
	owner.srv.lan.mu.Unlock()
	for _, name := range []string{"shared", "private"} {
		if err := owner.srv.store.AddProject(ctx, state.Project{Name: name, Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}

	st, err := owner.client.Share(ctx)
	if err != nil || st.Ready || st.Slots != state.DefaultShareSlots {
		t.Fatalf("Share() with phones off = %+v, %v; want not ready, default slots", st, err)
	}
	if _, err := owner.client.PairShare(ctx); err == nil {
		t.Error("PairShare() with phones off succeeded")
	}
	port, on, name := freePort(t), true, "Alice's AgentBox"
	if _, err := owner.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &on, Port: &port}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.client.UpdateShare(ctx, api.UpdateShareRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	pairing, err := owner.client.PairShare(ctx)
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	if err != nil || !strings.HasPrefix(pairing.Link, base+"/#share=") {
		t.Fatalf("PairShare() = %+v, %v", pairing, err)
	}

	if _, err := peer.client.AddShareRemote(ctx, base+"/"); err == nil || !strings.Contains(err.Error(), "no pairing code") {
		t.Errorf("AddShareRemote(a link with no code) = %v", err)
	}
	remote, err := peer.client.AddShareRemote(ctx, pairing.Link)
	if err != nil || remote.Name != name || remote.URL != base {
		t.Fatalf("AddShareRemote() = %+v, %v", remote, err)
	}
	if _, err := peer.client.AddShareRemote(ctx, pairing.Link); err == nil || !strings.Contains(err.Error(), "expired or was used") {
		t.Errorf("pairing twice with one link = %v", err)
	}

	st, err = owner.client.Share(ctx)
	if err != nil || len(st.Peers) != 1 || st.Peers[0].AllProjects || len(st.Peers[0].Projects) != 0 || st.Peers[0].Slots != -1 {
		t.Fatalf("Share() after pairing = %+v, %v; want one peer that reaches nothing yet", st, err)
	}
	p := st.Peers[0]
	// Nothing is shared until the owner chooses.
	if projects := remoteNames(t, peer); len(projects) != 0 {
		t.Errorf("the peer's remote projects before sharing = %v", projects)
	}
	zero := 0
	if _, err := owner.client.UpdateSharePeer(ctx, p.ID, api.UpdateSharePeerRequest{Projects: &[]string{"shared"}, Slots: &zero}); err != nil {
		t.Fatal(err)
	}
	local := remote.ID + "~shared"
	waitFor(t, "the shared project on the peer", func() bool { return slices.Equal(remoteNames(t, peer), []string{local}) })
	projects, _ := peer.client.Projects(ctx)
	for _, pr := range projects {
		if pr.Name == local && (pr.Remote == nil || pr.Remote.Owner != name || pr.Root != "") {
			t.Errorf("the remote project = %+v (remote %+v): want its owner and no root", pr, pr.Remote)
		}
	}

	// The peer's app reaches the shared project as it does a local one; the
	// other one, and what isn't in a project, it doesn't.
	app := newPhone(t, "http://agentbox", peer.client.HTTPClient().Transport)
	if code, body := app.do("GET", "/v1/projects/"+local+"/sessions", ""); code != http.StatusOK {
		t.Errorf("GET a shared project's sessions from the peer = %d %s", code, body)
	}
	if code, body := app.do("GET", "/v1/projects/"+remote.ID+"~private/sessions", ""); code != http.StatusForbidden || !strings.Contains(body, "isn't shared") {
		t.Errorf("GET an unshared project's sessions from the peer = %d %s", code, body)
	}

	// Over its slots, making an agent is refused, with why.
	_, err = peer.client.CreateAgent(ctx, api.CreateAgentRequest{Project: local, AI: "claude"})
	var apiErr *api.StatusError
	if err == nil || !strings.Contains(err.Error(), "may not run agents on this AgentBox") || !errors.As(err, &apiErr) || apiErr.Reason != api.ErrorShareSlotsFull || apiErr.Code != http.StatusConflict {
		t.Errorf("CreateAgent() with no slots = %v; want refused for its slots", err)
	}

	// The gateway itself, with the peer's token.
	sealed, _ := peer.srv.store.ShareRemotes(ctx)
	token, err := peer.srv.secrets().Open(sealed[0].Token)
	if err != nil {
		t.Fatal(err)
	}
	gw := newPhone(t, base, nil)
	bearer := []string{"Authorization", "Bearer " + token}
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/share/api/v1/projects/shared/sessions", http.StatusOK},
		{"GET", "/share/api/v1/projects/private/sessions", http.StatusForbidden},
		{"GET", "/share/api/v1/settings", http.StatusForbidden},
		{"GET", "/share/api/v1/lan", http.StatusForbidden},
		{"GET", "/share/api/v1/share", http.StatusForbidden},
		{"GET", "/share/api/v1/projects/shared/secrets", http.StatusForbidden},
		{"PATCH", "/share/api/v1/projects/shared", http.StatusForbidden},
		{"DELETE", "/share/api/v1/projects/shared", http.StatusForbidden},
		{"POST", "/share/api/v1/projects", http.StatusForbidden},
		{"GET", "/share/api/v1/projects/shared/schedules", http.StatusForbidden},
		{"POST", "/share/api/v1/projects/shared/schedules", http.StatusForbidden},
		{"GET", "/share/api/v1/projects/" + remote.ID + "~shared/sessions", http.StatusForbidden},
	} {
		if code, body := gw.do(c.method, c.path, "{}", bearer...); code != c.want {
			t.Errorf("%s %s with the peer's token = %d %s; want %d", c.method, c.path, code, body, c.want)
		}
	}
	if code, body := gw.do("GET", "/share/api/v1/projects", "", bearer...); code != http.StatusOK || !strings.Contains(body, `"shared"`) || strings.Contains(body, `"private"`) {
		t.Errorf("GET /v1/projects through the gateway = %d %s; want only the shared one", code, body)
	}
	if code, _ := gw.do("GET", "/share/api/v1/projects", "", "Authorization", "Bearer share_peer_x.nope"); code != http.StatusUnauthorized {
		t.Errorf("a wrong token = %d", code)
	}
	// A phone's token isn't a peer's.
	if code, _ := gw.do("GET", "/share/api/v1/projects", "", "Authorization", "Bearer phone_x.nope"); code != http.StatusUnauthorized {
		t.Errorf("a phone's token at the share gateway = %d", code)
	}

	// A memory for every project of the owner's isn't the peer's to write,
	// nor to close or replace; one for the project it was given is.
	ownerApp := newPhone(t, "http://agentbox", owner.client.HTTPClient().Transport)
	code, body := ownerApp.do("POST", "/v1/global/memory/memories", `{"title":"Prefers tabs"}`)
	var global api.Memory
	if code != http.StatusCreated || json.Unmarshal([]byte(body), &global) != nil || global.ID == "" {
		t.Fatalf("a global memory = %d %s", code, body)
	}
	for _, c := range []struct {
		path, body string
		want       int
	}{
		{"/memories", `{"title":"Use spaces","scope":"all"}`, http.StatusForbidden},
		{"/memories", `{"title":"Use spaces","supersedesId":"` + global.ID + `"}`, http.StatusBadRequest},
		{"/resolve", `{"id":"` + global.ID + `"}`, http.StatusForbidden},
		{"/memories", `{"title":"This project uses spaces"}`, http.StatusCreated},
	} {
		if code, body := gw.do("POST", "/share/api/v1/projects/shared/memory"+c.path, c.body, bearer...); code != c.want {
			t.Errorf("POST memory%s %s with the peer's token = %d %s; want %d", c.path, c.body, code, body, c.want)
		}
	}
	if got, err := owner.srv.memory().Memory(ctx, memory.Global, global.ID); err != nil || got.Resolved() || got.Superseded {
		t.Errorf("the global memory after the peer's tries = %+v, %v", got, err)
	}

	// Its slots count starting one of its agents again, however it names
	// the project.
	if err := owner.srv.store.AddAgent(ctx, state.Agent{Project: "shared", Name: "agent-09", Instance: "ab-shared-agent-09", AI: "claude", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := owner.srv.store.AddSharePeerAgent(ctx, p.ID, "shared", "agent-09"); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"shared", "Shared"} {
		if code, body := gw.do("POST", "/share/api/v1/agents/"+project+"/agent-09/start", "", bearer...); code != http.StatusConflict || !strings.Contains(body, "no slots") {
			t.Errorf("starting its agent in %q with no slots = %d %s; want refused", project, code, body)
		}
	}
	// A local project's agents are its own, with no remote's among them.
	if err := peer.srv.store.AddProject(ctx, state.Project{Name: "mine", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the owner's agent on the peer", func() bool {
		_, body := app.do("GET", "/v1/agents", "")
		return strings.Contains(body, `"`+local+`"`)
	})
	if code, body := app.do("GET", "/v1/agents?project=mine", ""); code != http.StatusOK || strings.Contains(body, local) {
		t.Errorf("GET /v1/agents?project=mine on the peer = %d %s; want none of the remote's", code, body)
	}

	// Unpaired, it reaches nothing, and the peer says why.
	if err := owner.client.RemoveSharePeer(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := gw.do("GET", "/share/api/v1/projects/shared/sessions", "", bearer...); code != http.StatusUnauthorized {
		t.Errorf("a revoked peer = %d", code)
	}
	waitFor(t, "the peer to see it was unpaired", func() bool {
		st, err := peer.client.Share(ctx)
		return err == nil && len(st.Remotes) == 1 && strings.Contains(st.Remotes[0].Error, "unpaired")
	})
}

func remoteNames(t *testing.T, d testDaemon) []string {
	t.Helper()
	projects, err := d.client.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range projects {
		if p.Remote != nil {
			out = append(out, p.Name)
		}
	}
	return out
}

// A peer's slots count the agents it made that run, those a project's chat
// made after the peer was the last to write in that session of it, and those
// being made for it.
func TestShareSlots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if err := d.srv.store.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	peer, _, err := d.srv.store.AddSharePeer(ctx, "Bob")
	if err != nil {
		t.Fatal(err)
	}
	if peer, err = d.srv.store.SetSharePeer(ctx, peer.ID, false, []string{"p"}, -1); err != nil {
		t.Fatal(err)
	}
	running := map[string]bool{}
	d.srv.peering.running = func(_ context.Context, refs []string) int {
		n := 0
		for _, r := range refs {
			if running[r] {
				n++
			}
		}
		return n
	}
	for _, name := range []string{"agent-01", "agent-02"} {
		if err := d.srv.store.AddAgent(ctx, state.Agent{Project: "p", Name: name, Instance: "ab-p-" + name, AI: "claude", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := d.srv.store.AddSharePeerAgent(ctx, peer.ID, "p", name); err != nil {
			t.Fatal(err)
		}
	}
	// The default is two.
	if err := d.srv.checkShareSlots(ctx, peer.ID); err != nil {
		t.Errorf("with none running: %v", err)
	}
	running["p/agent-01"], running["p/agent-02"] = true, true
	err = d.srv.checkShareSlots(ctx, peer.ID)
	if full := (shareSlotsFull{}); !errors.As(err, &full) || !strings.Contains(err.Error(), "Bob already has 2 of its 2 agents running") {
		t.Errorf("with two running of two = %v", err)
	}
	three := 3
	if _, err := d.client.UpdateShare(ctx, api.UpdateShareRequest{Slots: &three}); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.checkShareSlots(ctx, peer.ID); err != nil {
		t.Errorf("with the default raised to three: %v", err)
	}
	if _, err := d.srv.store.SetSharePeer(ctx, peer.ID, false, []string{"p"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.checkShareSlots(ctx, peer.ID); err == nil {
		t.Error("its own limit of one didn't hold")
	}
	st, _ := d.client.Share(ctx)
	if len(st.Peers) != 1 || st.Peers[0].Running != 2 || st.Peers[0].Slots != 1 {
		t.Errorf("Share() = %+v; want 2 running, 1 slot", st.Peers)
	}

	// Made by the project's chat: the peer's when it wrote last there.
	pctx := context.WithValue(ctx, sharePeerKey{}, peer)
	if got := d.srv.shareCreator(pctx, "p", "", false); got != peer.ID {
		t.Errorf("shareCreator(the peer's own request) = %q", got)
	}
	if got := d.srv.shareCreator(ctx, "p", "s1", true); got != "" {
		t.Errorf("shareCreator(a chat nobody shared wrote in) = %q", got)
	}
	// The session a message lands in is the lead's it went to, whatever the
	// path said: the project's own chat is the session "main".
	lead := func(session string) state.Agent {
		return state.Agent{Project: "p", Name: state.LeadSessionName(session), Role: state.RoleLead}
	}
	d.srv.heardInChat(pctx, lead("s1"))
	if got := d.srv.shareCreator(ctx, "p", "s1", true); got != peer.ID {
		t.Errorf("shareCreator(the peer's session) = %q", got)
	}
	if got := d.srv.shareCreator(ctx, "p", state.MigratedSession, true); got != "" {
		t.Errorf("shareCreator(another session) = %q", got)
	}
	if got := d.srv.shareCreator(ctx, "p", "s1", false); got != "" {
		t.Errorf("shareCreator(the user here, from the app) = %q", got)
	}
	d.srv.heardInChat(ctx, lead("s1"))
	if got := d.srv.shareCreator(ctx, "p", "s1", true); got != "" {
		t.Errorf("shareCreator(after the user here wrote) = %q", got)
	}
	d.srv.heardInChat(pctx, lead(state.MigratedSession))
	for _, session := range []string{"", state.MigratedSession} {
		if got := d.srv.shareCreator(ctx, "p", session, true); got != peer.ID {
			t.Errorf("shareCreator(the project's own chat, as %q) = %q", session, got)
		}
	}
}

// A create takes a peer's slot as it is accepted: creates at once can't all
// take its last one, and a create that fails gives it back.
func TestShareSlotHolds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), recordingIncus, testConfig{instances: runningAgent01})
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	peer, _, err := d.srv.store.AddSharePeer(ctx, "Bob")
	if err != nil {
		t.Fatal(err)
	}
	if peer, err = d.srv.store.SetSharePeer(ctx, peer.ID, false, []string{"hello-stack"}, 1); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	running := map[string]bool{}
	d.srv.peering.running = func(_ context.Context, refs []string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, r := range refs {
			if running[r] {
				n++
			}
		}
		return n
	}

	// Eight at once, for its one slot: one gets it.
	var got []string
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if id, err := d.srv.holdShareSlot(ctx, peer.ID); err == nil {
				mu.Lock()
				got = append(got, id)
				mu.Unlock()
			} else if !errors.As(err, new(shareSlotsFull)) {
				t.Errorf("holdShareSlot() = %v", err)
			}
		})
	}
	wg.Wait()
	if len(got) != 1 {
		t.Fatalf("%d of 8 creates at once held the peer's one slot", len(got))
	}
	d.srv.releaseShareSlot(got[0])
	d.srv.releaseShareSlot(got[0]) // twice is once
	hold, err := d.srv.holdShareSlot(ctx, peer.ID)
	if err != nil {
		t.Fatalf("after the hold was released: %v", err)
	}

	// The agent it made counts once, as the hold, until the hold goes.
	if err := d.srv.store.AddAgent(ctx, state.Agent{Project: "hello-stack", Name: "agent-09", Instance: "ab-hello-stack-agent-09", AI: "none", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.AddSharePeerAgent(ctx, peer.ID, "hello-stack", "agent-09"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	running["hello-stack/agent-09"] = true
	mu.Unlock()
	d.srv.shareSlotMade(hold, "hello-stack/agent-09")
	two := 2
	if _, err := d.client.UpdateSharePeer(ctx, peer.ID, api.UpdateSharePeerRequest{Slots: &two}); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.checkShareSlots(ctx, peer.ID); err != nil {
		t.Errorf("its made agent counted twice: %v", err)
	}
	d.srv.releaseShareSlot(hold)
	if err := d.srv.checkShareSlots(ctx, peer.ID); err != nil {
		t.Errorf("its made agent, running, of two slots: %v", err)
	}

	// A create the peer asks for holds its slot until its job ends; then
	// its agent counts, and with agent-09 running the next create is
	// refused.
	pctx := context.WithValue(ctx, sharePeerKey{}, peer)
	job, err := d.srv.createAgentJob(pctx, api.CreateAgentRequest{Project: "hello-stack", Name: "agent-01", AI: "none"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJobDone(t, d, job.ID); j.Status != api.JobSucceeded {
		t.Fatalf("the peer's create = %s: %s", j.Status, j.Error)
	}
	d.srv.peering.mu.Lock()
	held := len(d.srv.peering.holds)
	d.srv.peering.mu.Unlock()
	if refs, _ := d.srv.store.SharePeerAgents(ctx, peer.ID); held != 0 || !slices.Contains(refs, "hello-stack/agent-01") {
		t.Fatalf("after the create: %d held, the peer's agents %v", held, refs)
	}
	mu.Lock()
	running["hello-stack/agent-01"] = true
	mu.Unlock()
	if _, err := d.srv.createAgentJob(pctx, api.CreateAgentRequest{Project: "hello-stack", AI: "none"}, false); !errors.As(err, new(shareSlotsFull)) {
		t.Errorf("a create over its two slots, both running = %v", err)
	}
}

// A peer that is given less loses what it had open in the projects it no
// longer reaches, and its events, which it reconnects to at once; given new
// slots, it loses nothing.
func TestSharePeerChangeEndsWhatItLost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	for _, name := range []string{"p", "q"} {
		if err := d.srv.store.AddProject(ctx, state.Project{Name: name, Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	peer, _, err := d.srv.store.AddSharePeer(ctx, "Bob")
	if err != nil {
		t.Fatal(err)
	}
	if peer, err = d.srv.store.SetSharePeer(ctx, peer.ID, false, []string{"p", "q"}, -1); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	ended := map[string]bool{}
	for name, o := range map[string]shareOpen{
		"events": {events: true}, "in p": {project: "p"}, "in q": {project: "q"}, "projects": {},
	} {
		cancel := context.CancelFunc(func() {
			mu.Lock()
			ended[name] = true
			mu.Unlock()
		})
		_, untrack := d.srv.peering.track(peer.ID, o, &cancel)
		t.Cleanup(untrack)
	}
	endedNow := func() []string {
		mu.Lock()
		defer mu.Unlock()
		var out []string
		for name := range ended {
			out = append(out, name)
		}
		slices.Sort(out)
		return out
	}
	one := 1
	if _, err := d.client.UpdateSharePeer(ctx, peer.ID, api.UpdateSharePeerRequest{Slots: &one}); err != nil {
		t.Fatal(err)
	}
	if got := endedNow(); len(got) != 0 {
		t.Errorf("new slots ended %v", got)
	}
	if _, err := d.client.UpdateSharePeer(ctx, peer.ID, api.UpdateSharePeerRequest{Projects: &[]string{"p"}}); err != nil {
		t.Fatal(err)
	}
	if got := endedNow(); !slices.Equal(got, []string{"events", "in q"}) {
		t.Errorf("losing q ended %v; want its events and what it had open in q", got)
	}
}

// Every route of the API is decided on for a paired AgentBox: a route in a
// project reaches it in the projects it was given unless shareNever says why
// not, and a route in no project never does, besides the lists the gateway
// cuts down to its projects.
func TestEveryRouteIsClassifiedForPeers(t *testing.T) {
	t.Parallel()
	_, patterns := (&Server{}).apiRoutes()
	globals := map[string]string{
		"GET /v1/projects": "projects", "GET /v1/agents": "agents", "GET /v1/events": "events", "GET /v1/version": "version",
	}
	for _, pattern := range patterns {
		method, path, ok := strings.Cut(pattern, " ")
		methods := []string{method}
		if !ok {
			path, methods = pattern, []string{http.MethodGet, http.MethodPost}
		}
		for _, m := range methods {
			route := shareRouteOf(m, lanSamplePath(path))
			inProject := strings.Contains(path, "{project}")
			switch {
			case route.never != "":
			case pattern == "POST /v1/agents":
				if !route.bodyProject {
					t.Errorf("%s = %+v: want the project from its body, whose slots checkCreate holds", pattern, route)
				}
			case inProject && route.project != "x":
				t.Errorf("%s is in a project but doesn't reach a peer it's shared with: in internal/daemon/sharepeers.go, add it to shareNever saying why, or let shareProjectRoute match it", pattern)
			case !inProject && route.project != "":
				t.Errorf("%s is in no project, but a peer reaches it as project %q", pattern, route.project)
			case !inProject && route.global != globals[m+" "+path]:
				t.Errorf("%s reaches peers as %q: a route in no project reaches none, but the lists in globals", pattern, route.global)
			}
		}
	}
	for _, c := range []struct {
		method, path string
		never        bool
		project      string
	}{
		{"GET", "/v1/projects/p/chat", false, "p"},
		{"POST", "/v1/projects/p/sessions/s/chat/messages", false, "p"},
		{"GET", "/v1/agents/p/a/terminal", false, "p"},
		{"POST", "/v1/projects/p/secrets", true, ""},
		{"GET", "/v1/agents/p/a/credentials", true, ""},
		{"PATCH", "/v1/projects/p", true, ""},
		{"POST", "/v1/projects/p/retire", true, ""},
		{"PUT", "/v1/projects/layout", true, ""},
		{"POST", "/v1/agents/p/a/fork", true, ""},
		{"GET", "/v1/projects/p/schedules", true, ""},
		{"POST", "/v1/projects/p/schedules", true, ""},
		{"POST", "/v1/projects/p/schedules/s1/run", true, ""},
		{"GET", "/v1/projects/p/../q/chat", false, ""},
		{"GET", "/v1/projects/_home/chat", false, ""},
		{"GET", "/v1/settings", false, ""},
	} {
		route := shareRouteOf(c.method, c.path)
		if (route.never != "") != c.never || route.project != c.project {
			t.Errorf("shareRouteOf(%s %s) = %+v; want never %v, project %q", c.method, c.path, route, c.never, c.project)
		}
	}
	for path, want := range map[string]string{
		"/v1/agents/p/agent-01/start":  "agent-01",
		"/v1/agents/p/agent-01/resume": "agent-01",
		"/v1/agents/p/agent-01/stop":   "",
	} {
		if r := shareRouteOf("POST", path); r.agent != want || r.project != "p" {
			t.Errorf("shareRouteOf(POST %s) = %+v; want agent %q in p", path, r, want)
		}
	}
}

func TestRenameJSON(t *testing.T) {
	t.Parallel()
	in := `{"type":"x","project":"web","ref":"web/agent-01","target":"web","n":12345678901234567890,` +
		`"agent":"web/lead","items":[{"name":"web","displayName":"Web","iconVersion":3},{"name":"agent-01","project":"_home"}]}`
	var got map[string]any
	if err := json.Unmarshal(renameJSON(json.RawMessage(in), "r1"), &got); err != nil {
		t.Fatal(err)
	}
	items := got["items"].([]any)
	for k, want := range map[string]any{
		"project": "r1~web", "ref": "r1~web/agent-01", "target": "web", "agent": "r1~web/lead",
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
	if n := items[0].(map[string]any)["name"]; n != "r1~web" {
		t.Errorf("a project's name = %v", n)
	}
	if n, p := items[1].(map[string]any)["name"], items[1].(map[string]any)["project"]; n != "agent-01" || p != "_home" {
		t.Errorf("an agent's name and the Home project = %v, %v: neither is renamed", n, p)
	}
	if !strings.Contains(string(renameJSON(json.RawMessage(in), "r1")), "12345678901234567890") {
		t.Error("a large number lost its digits")
	}
	if got := string(unrename([]byte(`{"project":"r1~web","text":"see r1~web"}`), "r1")); got != `{"project":"web","text":"see r1~web"}` {
		t.Errorf("unrename() = %s", got)
	}
	if id, p, ok := remoteOf("r1~web"); !ok || id != "r1" || p != "web" {
		t.Errorf("remoteOf() = %q %q %v", id, p, ok)
	}
	if _, _, ok := remoteOf("web"); ok {
		t.Error("a local project is remote")
	}
}

func TestShareLink(t *testing.T) {
	t.Parallel()
	base, secret, err := shareLink(" https://abc.trycloudflare.com/#share=s3cret ")
	if err != nil || base != "https://abc.trycloudflare.com" || secret != "s3cret" {
		t.Errorf("shareLink() = %q %q %v", base, secret, err)
	}
	for _, bad := range []string{"", "abc", "ftp://x/#share=1", "http://x/#pair=1"} {
		if _, _, err := shareLink(bad); err == nil {
			t.Errorf("shareLink(%q) succeeded", bad)
		}
	}
}

// A peer is sent an event when it is about one of its projects, however the
// event names it.
func TestEventProject(t *testing.T) {
	t.Parallel()
	for data, want := range map[string]string{
		`{"project":"web"}`:                  "web",
		`{"ref":"web/agent-01","state":"x"}`: "web",
		`{"target":"web/agent-01"}`:          "web",
		`{"agent":"web/lead","seq":3}`:       "web",
		`{"agent":"agent-01"}`:               "",
		`{"theme":"dark"}`:                   "",
		`[]`:                                 "",
	} {
		if got := eventProject(json.RawMessage(data)); got != want {
			t.Errorf("eventProject(%s) = %q, want %q", data, got, want)
		}
	}
}

// An owner whose agents can't be listed still shows its shared projects.
func TestRemoteListsWithoutAgents(t *testing.T) {
	t.Parallel()
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/share/api/v1/projects":
			_, _ = w.Write([]byte(`[{"name":"web","displayName":"Web","iconVersion":0}]`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"incus list: connection refused"}`))
		}
	}))
	defer owner.Close()
	s := &Server{peering: newPeeringState()}
	c := &remoteConn{r: state.ShareRemote{ID: "r1", Name: "Alice", URL: owner.URL}, token: "tok"}
	projects, agents := s.remoteLists(context.Background(), c)
	if len(projects) != 1 || !strings.Contains(string(projects[0]), `"name":"r1~web"`) || !strings.Contains(string(projects[0]), `"owner":"Alice"`) || len(agents) != 0 {
		t.Errorf("remoteLists() = %s, %s", projects, agents)
	}
}

// An owner that doesn't answer is left alone a while, so lists don't each wait
// on it: what it last shared shows meanwhile.
func TestRemoteListsBackOff(t *testing.T) {
	t.Parallel()
	var asked atomic.Int32
	var up atomic.Bool
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if !up.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer owner.Close()
	s := &Server{peering: newPeeringState()}
	c := &remoteConn{r: state.ShareRemote{ID: "r1", Name: "Alice", URL: owner.URL}, token: "tok"}
	for range 3 {
		s.remoteLists(context.Background(), c)
	}
	if n := asked.Load(); n != 1 {
		t.Errorf("three lists asked a remote that didn't answer %d times; want once", n)
	}
	c.mu.Lock()
	first, err := c.backoff, c.err
	c.retryAt = time.Time{}
	c.mu.Unlock()
	if first != remoteRetryFirst || !strings.Contains(err, "502") {
		t.Errorf("after one failure: backoff %v, error %q", first, err)
	}
	s.remoteLists(context.Background(), c)
	c.mu.Lock()
	second := c.backoff
	c.retryAt = time.Time{}
	c.mu.Unlock()
	if second != 2*remoteRetryFirst {
		t.Errorf("after two failures in a row: backoff %v", second)
	}
	up.Store(true)
	s.remoteLists(context.Background(), c)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != "" || c.backoff != 0 || c.listedAt.IsZero() {
		t.Errorf("once it answers: error %q, backoff %v, listed %v", c.err, c.backoff, c.listedAt)
	}
}
