package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Destroying an agent archives it: gone from the agents list, kept in the
// past agents list with its chat readable, out of the chat search unless it
// asks for past agents, and only deleted when the user deletes it.
func TestDestroyArchivesAndDeleteForgets(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", AI: "claude", Interface: state.InterfaceChat,
		Title: "Reminders", Branch: "agentbox/reminders", BaseRef: "main", Worktree: "/worktrees/agent-01",
		Status: state.AgentReady, CreatedAt: time.Now(),
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.SaveChatItems(ctx, a.Project, a.Name, []state.ChatItem{
		{ID: "u1", Position: 0, Data: []byte(`{"id":"u1","kind":"user","text":"Add a reminders page"}`)},
		{ID: "a1", Position: 1, Data: []byte(`{"id":"a1","kind":"assistant","text":"The reminders page lists every reminder, newest first."}`)},
	}); err != nil {
		t.Fatal(err)
	}

	if err := d.client.DestroyAgent(ctx, a.Ref(), true, false, false); err != nil {
		t.Fatal(err)
	}
	if agents, _ := d.client.Agents(ctx, ""); len(agents) != 0 {
		t.Fatalf("a destroyed agent is still listed: %+v", agents)
	}
	past, err := d.client.ArchivedAgents(ctx, api.ArchivedOptions{Project: "hello-stack"})
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 || past[0].Ref != a.Ref() || past[0].Title != "Reminders" || past[0].Task != "Add a reminders page" || past[0].Branch != a.Branch {
		t.Fatalf("past agents %+v", past)
	}
	if got, err := d.client.ArchivedAgent(ctx, a.Ref()); err != nil || got.Name != a.Name {
		t.Fatalf("ArchivedAgent = %+v, %v", got, err)
	}
	if past, _ := d.client.ArchivedAgents(ctx, api.ArchivedOptions{PR: api.PRStateMerged}); len(past) != 0 {
		t.Fatalf("an agent with no pull request was listed as merged: %+v", past)
	}

	// Its chat reads as a live one's does.
	thread, err := d.client.Chat(ctx, a.Ref())
	if err != nil {
		t.Fatal(err)
	}
	// Its two messages, and the notice that its machine went.
	if len(thread.Items) < 2 || thread.Items[0].ID != "u1" || thread.Items[1].ID != "a1" {
		t.Fatalf("the past agent's chat is %+v", thread.Items)
	}

	found := func(archived bool) []api.SearchHit {
		t.Helper()
		res, err := d.client.SearchWith(ctx, "reminders page", 10, api.SearchOptions{Kinds: []string{api.SearchChats}, Archived: archived})
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range res.Groups {
			if g.Kind == api.SearchChats {
				return g.Hits
			}
		}
		return nil
	}
	if hits := found(false); len(hits) != 0 {
		t.Fatalf("a past agent's chat was searched without asking: %+v", hits)
	}
	hits := found(true)
	if len(hits) != 2 || !hits[0].Archived || hits[0].Agent != a.Name {
		t.Fatalf("with past agents: %+v", hits)
	}

	// Its name is never handed out again.
	if err := d.srv.store.AddAgent(ctx, state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-x", AI: "claude", CreatedAt: time.Now()}); !errors.Is(err, state.ErrExists) {
		t.Fatalf("AddAgent with a past agent's name = %v", err)
	}

	res, err := d.client.DeleteArchived(ctx, []string{a.Ref(), "hello-stack/agent-99"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || res.Failed["hello-stack/agent-99"] == "" {
		t.Fatalf("bulk delete = %+v", res)
	}
	if past, _ := d.client.ArchivedAgents(ctx, api.ArchivedOptions{}); len(past) != 0 {
		t.Fatalf("deleted, still listed: %+v", past)
	}
	if _, err := d.client.Chat(ctx, a.Ref()); err == nil {
		t.Fatal("a deleted agent's chat is still readable")
	}
}

func TestArchivedFilterErrors(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.ArchivedAgents(ctx, api.ArchivedOptions{PR: "draft"}); err == nil {
		t.Error("an unknown pull request state was accepted")
	}
	if _, err := d.client.ArchivedAgents(ctx, api.ArchivedOptions{Since: "last week"}); err == nil {
		t.Error("a date that isn't one was accepted")
	}
	if past, err := d.client.ArchivedAgents(ctx, api.ArchivedOptions{Since: "2026-10-01", Until: "2026-10-14"}); err != nil || len(past) != 0 {
		t.Errorf("dates: %+v, %v", past, err)
	}
}

func TestContinueBrief(t *testing.T) {
	a := state.ArchivedAgent{
		Project: "pawly", Name: "agent-07", Title: "Login fix", Task: "Fix the login redirect",
		Branch: "agentbox/fix-login", BaseRef: "main", ArchivedAt: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC),
		PR: state.ArchivedPR{Number: 42, Title: "fix: login redirect", State: "open", URL: "https://github.com/o/r/pull/42"},
	}
	brief := continueBrief(a, "refs/heads/agentbox/fix-login", "I fixed the loop in auth.go, and the test that covers it passes.", "Now add a test for logout.")
	for _, want := range []string{
		`agent-07 ("Login fix")`, "2026-10-12", "where its branch agentbox/fix-login ended", "#42",
		"> Fix the login redirect", "> I fixed the loop in auth.go", "Now add a test for logout.",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief lacks %q:\n%s", want, brief)
		}
	}
	gone := continueBrief(a, "", "Done.", "")
	if !strings.Contains(gone, "is gone, so you start from main") || strings.Contains(gone, "What it said last") || !strings.Contains(gone, "carry it on") {
		t.Errorf("with its branch gone and no summary:\n%s", gone)
	}
}
