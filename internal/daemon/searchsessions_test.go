package daemon

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A chat's sessions, open and closed, are found by topic, by what was said in
// them, opening at the message, and by the agents they made, their branches
// and pull requests; a lead's message in the chats group says its session.
func TestSearchSessions(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	store := d.srv.store
	now := time.Now()
	login, err := store.AddLeadSession(ctx, a.Project, "Prod: login 500s", now)
	if err != nil {
		t.Fatal(err)
	}
	release, err := store.AddLeadSession(ctx, a.Project, "Release notes", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseLeadSession(ctx, a.Project, release.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []state.LeadSession{login, release} {
		if err := store.AddAgent(ctx, state.Agent{Project: a.Project, Name: state.LeadSessionName(s.ID), Instance: "ab-" + s.ID, Role: state.RoleLead, Status: state.AgentReady, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	say := func(session, id, text string) {
		t.Helper()
		if err := store.SaveChatItems(ctx, a.Project, state.LeadSessionName(session), []state.ChatItem{{ID: id, Data: []byte(`{"id":"` + id + `","kind":"user","text":"` + text + `","createdAt":"` + now.Format(time.RFC3339Nano) + `"}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	say(login.ID, "m-login", "Here is the stack trace from the gateway")
	say(release.ID, "m-release", "Draft the changelog; the gateway fix goes first")
	if err := store.SetAgentSession(ctx, a.Project, a.Name, release.ID); err != nil {
		t.Fatal(err)
	}
	d.srv.pulls.readFor("acme/hello", a.Project)
	d.srv.pulls.mu.Lock()
	d.srv.pulls.byRepo["acme/hello"] = pullsEntry{prs: []api.PullRequest{{Number: 97531, Agent: a.Name, Title: "docs: the changelog", State: "open"}}}
	d.srv.pulls.mu.Unlock()

	sessions := func(q string) []api.SearchHit {
		t.Helper()
		got, err := d.client.SearchWith(ctx, q, 0, api.SearchOptions{Kinds: []string{api.SearchSessions}, Project: a.Project})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Groups) == 0 {
			return nil
		}
		return got.Groups[0].Hits
	}

	// By topic: no message to open at.
	if hits := sessions("login"); len(hits) != 1 || hits[0].ID != login.ID || hits[0].Session != login.ID || hits[0].Item != "" || hits[0].Archived || hits[0].Title != "Prod: login 500s" {
		t.Errorf("login = %+v", hits)
	}
	// By what was said, open or closed, each at its message.
	hits := sessions("gateway")
	byID := map[string]api.SearchHit{}
	for _, h := range hits {
		byID[h.ID] = h
	}
	if len(hits) != 2 || byID[login.ID].Item != "m-login" || !strings.Contains(byID[login.ID].Detail, "stack trace") || byID[release.ID].Item != "m-release" || !byID[release.ID].Archived {
		t.Errorf("gateway = %+v", hits)
	}
	// By the agent it made: its pull request's number, its branch.
	for _, q := range []string{"97531", "#97531", a.Branch} {
		if hits := sessions(q); len(hits) != 1 || hits[0].ID != release.ID || hits[0].Tag != "#97531" || hits[0].Detail == "" {
			t.Errorf("%q = %+v", q, hits)
		}
	}
	if hits := sessions("zebra"); len(hits) != 0 {
		t.Errorf("zebra = %+v", hits)
	}
	// Topic and message both: the topic's match ranks it first.
	say(release.ID, "m-login-too", "the login page copy")
	if hits := sessions("login"); len(hits) != 2 || hits[0].ID != login.ID {
		t.Errorf("login after release mentioned it = %+v", hits)
	}

	// Unscoped, every project's; and the chats group says a lead's session.
	got, err := d.client.Search(ctx, "stack trace", 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var chat api.SearchHit
	for _, g := range got.Groups {
		kinds = append(kinds, g.Kind)
		if g.Kind == api.SearchChats {
			chat = g.Hits[0]
		}
	}
	if !slices.Equal(kinds, []string{api.SearchSessions, api.SearchChats}) || chat.ID != "m-login" || chat.Agent != "" || chat.Session != login.ID {
		t.Errorf("stack trace = %v, chat hit %+v", kinds, chat)
	}
}

// Pins and the tabs' order are kept by the daemon: the open sessions come in
// their tabs' order, pinned first.
func TestSessionPinsAndOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, topic := range []string{"One", "Two", "Three"} {
		s, err := d.client.NewLeadSession(ctx, "hello-stack", topic)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
	}
	order := func() (out []string) {
		t.Helper()
		list, err := d.client.LeadSessions(ctx, "hello-stack")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range list {
			if s.Pinned {
				out = append(out, "*"+s.ID)
			} else {
				out = append(out, s.ID)
			}
		}
		return out
	}
	start := order()
	if pinned, err := d.client.PinLeadSession(ctx, "hello-stack", ids[2], true); err != nil || !pinned.Pinned {
		t.Fatalf("PinLeadSession() = %+v, %v", pinned, err)
	}
	if got := order(); got[0] != "*"+ids[2] || len(got) != len(start) {
		t.Errorf("after pinning three = %v", got)
	}
	reordered, err := d.client.OrderLeadSessions(ctx, "hello-stack", []string{ids[1], ids[0]})
	if err != nil {
		t.Fatal(err)
	}
	if reordered[0].ID != ids[2] || reordered[1].ID != ids[1] || reordered[2].ID != ids[0] {
		t.Errorf("OrderLeadSessions() = %v", reordered)
	}
	if got := order(); !slices.Equal(got[:3], []string{"*" + ids[2], ids[1], ids[0]}) {
		t.Errorf("after the drag = %v", got)
	}
	// A rename leaves the pin alone.
	if renamed, err := d.client.RenameLeadSession(ctx, "hello-stack", ids[2], "Three, renamed"); err != nil || !renamed.Pinned || renamed.Topic != "Three, renamed" {
		t.Errorf("RenameLeadSession() = %+v, %v", renamed, err)
	}
	if _, err := d.client.OrderLeadSessions(ctx, "hello-stack", []string{"nope"}); err == nil {
		t.Error("ordering a session that isn't open wasn't refused")
	}
}
