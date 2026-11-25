package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// sharedPair is an owner and a peer paired, with project "shared" shared,
// or the one add made on the owner.
type sharedPair struct {
	owner, peer testDaemon
	remote      api.ShareRemote
	peerID      string
	local       string // the shared project's name on the peer
	gw          *phoneClient
	bearer      []string
	port        int // the owner's phone page, its share gateway
}

func pairShared(t *testing.T, add ...func(owner testDaemon) string) sharedPair {
	t.Helper()
	ctx := context.Background()
	owner := startTestDaemon(t, t.TempDir(), fakeIncus)
	peer := startTestDaemon(t, t.TempDir(), fakeIncus)
	project := "shared"
	owner.srv.peering.running = func(context.Context, []string) int { return 0 }
	owner.srv.lan.mu.Lock()
	owner.srv.lan.own = true
	owner.srv.lan.addresses = func() []string { return []string{"127.0.0.1"} }
	owner.srv.lan.mu.Unlock()
	if len(add) > 0 {
		project = add[0](owner)
	} else if err := owner.srv.store.AddProject(ctx, state.Project{Name: project, Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	port, on := freePort(t), true
	ownerName, peerName := "Alice", "Ana"
	if _, err := owner.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &on, Port: &port}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.client.UpdateShare(ctx, api.UpdateShareRequest{Name: &ownerName}); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.client.UpdateShare(ctx, api.UpdateShareRequest{Name: &peerName}); err != nil {
		t.Fatal(err)
	}
	pairing, err := owner.client.PairShare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := peer.client.AddShareRemote(ctx, pairing.Link)
	if err != nil {
		t.Fatal(err)
	}
	st, err := owner.client.Share(ctx)
	if err != nil || len(st.Peers) != 1 {
		t.Fatalf("Share() = %+v, %v", st, err)
	}
	if _, err := owner.client.UpdateSharePeer(ctx, st.Peers[0].ID, api.UpdateSharePeerRequest{Projects: &[]string{project}}); err != nil {
		t.Fatal(err)
	}
	local := remote.ID + "~" + project
	waitFor(t, "the shared project on the peer", func() bool { return slices.Equal(remoteNames(t, peer), []string{local}) })
	sealed, _ := peer.srv.store.ShareRemotes(ctx)
	token, err := peer.srv.secrets().Open(sealed[0].Token)
	if err != nil {
		t.Fatal(err)
	}
	return sharedPair{
		owner: owner, peer: peer, remote: remote, peerID: st.Peers[0].ID, local: local,
		gw: newPhone(t, "http://127.0.0.1:"+strconv.Itoa(port), nil), bearer: []string{"Authorization", "Bearer " + token}, port: port,
	}
}

// What a peer may do is what its owner gave it: looking, chatting, or
// everything; the peer's app learns which with the project.
func TestShareAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := pairShared(t)
	setAccess := func(access string) {
		t.Helper()
		if _, err := sp.owner.client.UpdateSharePeer(ctx, sp.peerID, api.UpdateSharePeerRequest{Access: &access}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		access string
		want   map[string]int // "METHOD path" → status, http.StatusForbidden meaning refused for its access
	}{
		{state.ShareAccessView, map[string]int{
			"GET /v1/projects/shared/sessions":             http.StatusOK,
			"POST /v1/projects/shared/chat/messages":       http.StatusForbidden,
			"POST /v1/projects/shared/questions/q0/answer": http.StatusForbidden,
			"PUT /v1/here": http.StatusNoContent,
			"GET /v1/agents/shared/agent-01/terminal": http.StatusForbidden,
		}},
		{state.ShareAccessChat, map[string]int{
			"POST /v1/projects/shared/questions/q0/answer": http.StatusNotFound, // past its access: there is no such question
			"POST /v1/agents/shared/agent-01/merge":        http.StatusForbidden,
			"POST /v1/agents":                              http.StatusForbidden,
			"GET /v1/agents/shared/agent-01/terminal":      http.StatusForbidden,
		}},
	} {
		setAccess(c.access)
		for route, want := range c.want {
			method, path, _ := strings.Cut(route, " ")
			body := `{"client":"w1","project":"shared","answer":"yes"}`
			if path == "/v1/agents" {
				body = `{"project":"shared","ai":"claude"}`
			}
			code, got := sp.gw.do(method, "/share/api"+path, body, sp.bearer...)
			if code != want || (want == http.StatusForbidden && !strings.Contains(got, "Settings → Share")) {
				t.Errorf("with %s access, %s = %d %s; want %d", c.access, route, code, got, want)
			}
		}
	}
	// The peer's app reads it from the project, to offer only what works.
	waitFor(t, "the peer to see it may chat", func() bool {
		projects, err := sp.peer.client.Projects(ctx)
		return err == nil && slices.ContainsFunc(projects, func(p api.Project) bool {
			return p.Name == sp.local && p.Remote != nil && p.Remote.Access == state.ShareAccessChat
		})
	})
	// The project's chat can't make an agent for a peer that may only chat.
	if err := sp.owner.srv.checkShareSlots(ctx, sp.peerID); err == nil || !strings.Contains(err.Error(), "only chat") {
		t.Errorf("checkShareSlots() for a peer that may only chat = %v", err)
	}
	st, _ := sp.owner.client.Share(ctx)
	if st.Peers[0].Access != state.ShareAccessChat {
		t.Errorf("the owner's Settings say access %q, want chat", st.Peers[0].Access)
	}
}

// Who has what open in a shared project reaches the other side, both ways,
// with each side named as its own app expects; what a peer opens and answers
// is its activity, and its answer carries it as author.
func TestSharePresenceAuthorsAndActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := pairShared(t)
	peerApp := newPhone(t, "http://agentbox", sp.peer.client.HTTPClient().Transport)
	ownerApp := newPhone(t, "http://agentbox", sp.owner.client.HTTPClient().Transport)
	people := func(d testDaemon, project string) []api.PersonHere {
		all, err := d.client.WhoIsHere(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, ph := range all {
			if ph.Project == project {
				return ph.People
			}
		}
		return nil
	}

	// Ana opens agent-01's desktop in the shared project, on her AgentBox.
	if code, body := peerApp.do("PUT", "/v1/here", `{"client":"ana-1","project":"`+sp.local+`","agent":"agent-01","tab":"desktop"}`); code != http.StatusNoContent {
		t.Fatalf("PUT /v1/here on the peer = %d %s", code, body)
	}
	waitFor(t, "Ana on the owner", func() bool {
		got := people(sp.owner, "shared")
		return len(got) == 1 && got[0].ID == sp.peerID && got[0].Name == "Ana" && got[0].Agent == "agent-01" && got[0].Tab == "desktop"
	})
	waitFor(t, "Ana's desktop in her activity", func() bool {
		acts, err := sp.owner.client.SharePeerActivity(ctx, sp.peerID, 0)
		return err == nil && len(acts) > 0 && acts[0].Kind == api.ShareDidOpenDesktop && acts[0].Agent == "agent-01" && acts[0].Project == "shared"
	})

	// Alice types in the project's chat, on hers: Ana's AgentBox sees her
	// by its remote's id, and Ana as itself.
	if code, body := ownerApp.do("PUT", "/v1/here", `{"client":"alice-1","project":"shared","session":"main","typing":true}`); code != http.StatusNoContent {
		t.Fatalf("PUT /v1/here on the owner = %d %s", code, body)
	}
	waitFor(t, "Alice typing, on the peer", func() bool {
		got := people(sp.peer, sp.local)
		var alice, self bool
		for _, p := range got {
			alice = alice || (p.ID == sp.remote.ID && p.Name == "Alice" && p.Typing && p.Session == "main")
			self = self || (p.ID == "" && p.Name == "Ana")
		}
		return alice && self
	})

	// Ana leaves: the owner forgets her at once.
	if code, _ := peerApp.do("DELETE", "/v1/here/ana-1", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/here on the peer = %d", code)
	}
	waitFor(t, "Ana gone from the owner", func() bool {
		for _, p := range people(sp.owner, "shared") {
			if p.ID == sp.peerID {
				return false
			}
		}
		return true
	})

	// Ana answers an agent's question; Alice another. Each side sees its
	// own answer as its own, and the other's with who wrote it.
	for _, id := range []string{"q-ana", "q-alice"} {
		if err := sp.owner.srv.store.AddQuestion(ctx, state.Question{ID: id, Project: "shared", Agent: "agent-01", Kind: state.QuestionDecision, Text: "Which?", Status: state.QuestionEscalated, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if code, body := peerApp.do("POST", "/v1/projects/"+sp.local+"/questions/q-ana/answer", `{"answer":"the blue one"}`); code != http.StatusOK {
		t.Fatalf("Ana's answer = %d %s", code, body)
	}
	if code, body := ownerApp.do("POST", "/v1/projects/shared/questions/q-alice/answer", `{"answer":"the red one"}`); code != http.StatusOK {
		t.Fatalf("Alice's answer = %d %s", code, body)
	}
	authors := func(app *phoneClient, project string) map[string]*api.Person {
		_, body := app.do("GET", "/v1/projects/"+project+"/questions?all=1", "")
		var qs []api.Question
		if err := json.Unmarshal([]byte(body), &qs); err != nil {
			t.Fatalf("questions on %s: %v (%s)", project, err, body)
		}
		out := map[string]*api.Person{}
		for _, q := range qs {
			out[q.ID] = q.Author
		}
		return out
	}
	onOwner := authors(ownerApp, "shared")
	if a := onOwner["q-ana"]; a == nil || a.ID != sp.peerID || a.Name != "Ana" {
		t.Errorf("Ana's answer on the owner has author %+v, want Ana", a)
	}
	if a := onOwner["q-alice"]; a != nil {
		t.Errorf("Alice's answer on the owner has author %+v, want none (hers)", a)
	}
	onPeer := authors(peerApp, sp.local)
	if a := onPeer["q-ana"]; a != nil {
		t.Errorf("Ana's answer on the peer has author %+v, want none (hers)", a)
	}
	if a := onPeer["q-alice"]; a == nil || a.ID != sp.remote.ID || a.Name != "Alice" {
		t.Errorf("Alice's answer on the peer has author %+v, want Alice by the remote's id", a)
	}
	acts, _ := sp.owner.client.SharePeerActivity(ctx, sp.peerID, 0)
	if len(acts) == 0 || acts[0].Kind != api.ShareDidAnswer || acts[0].Agent != "agent-01" {
		t.Errorf("Ana's latest activity = %+v, want her answer to agent-01", acts)
	}
}

// A remote's chat items name their authors as the peer's app expects.
func TestRemapPeople(t *testing.T) {
	t.Parallel()
	c := &remoteConn{r: state.ShareRemote{ID: "r1", Name: "Alice"}, token: "share_peer_me.secret"}
	in := `{"agent":"web/lead","items":[` +
		`{"id":"1","turn":"1","kind":"user","text":"from Alice"},` +
		`{"id":"2","turn":"2","kind":"user","text":"mine","author":{"id":"peer_me","name":"Ana"}},` +
		`{"id":"3","turn":"3","kind":"user","text":"Bob's","author":{"id":"peer_bob","name":"Bob"}},` +
		`{"id":"4","turn":"3","kind":"assistant","text":"answer"},` +
		`{"id":"5","turn":"5","kind":"user","woken":true}]}`
	var out struct {
		Agent string         `json:"agent"`
		Items []api.ChatItem `json:"items"`
	}
	if err := json.Unmarshal(fromRemote(json.RawMessage(in), c), &out); err != nil {
		t.Fatal(err)
	}
	if out.Agent != "r1~web/lead" {
		t.Errorf("agent = %q, want it renamed", out.Agent)
	}
	want := []*api.Person{{ID: "r1", Name: "Alice"}, nil, {ID: "peer_bob", Name: "Bob"}, nil, nil}
	for i, it := range out.Items {
		if (it.Author == nil) != (want[i] == nil) || (it.Author != nil && *it.Author != *want[i]) {
			t.Errorf("item %s has author %+v, want %+v", it.ID, it.Author, want[i])
		}
	}
}
