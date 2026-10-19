package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func archiveTestAgent(t *testing.T, st *Store, project, name, title string) Agent {
	t.Helper()
	ctx := context.Background()
	a := Agent{
		Project: project, Name: name, Instance: "ab-" + project + "-" + name, Title: title,
		AI: "claude", Branch: "agentbox/" + name, BaseRef: "main", BaseCommit: "abc123",
		Worktree: "/data/worktrees/" + project + "/" + name, Status: AgentReady,
		CreatedAt: time.Now().Add(-time.Hour), Interface: InterfaceChat,
	}
	if err := st.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	a, err := st.Agent(ctx, project, name)
	if err != nil {
		t.Fatal(err)
	}
	items := []ChatItem{
		{ID: "u1", Position: 0, Data: []byte(`{"id":"u1","kind":"user","text":"  Fix the login redirect  "}`)},
		{ID: "a1", Position: 1, Data: []byte(`{"id":"a1","kind":"assistant","text":"I fixed the redirect loop in auth.go"}`)},
	}
	if err := st.SaveChatItems(ctx, project, name, items); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChat(ctx, project, name, Chat{SessionID: "sess", Options: map[string]string{"model": "opus"}}); err != nil {
		t.Fatal(err)
	}
	return a
}

// Archiving takes the agent out of the live list and keeps its record and
// its whole chat; only deleting it takes the chat, and its name is never
// handed out again either way.
func TestArchiveKeepsChatUntilDeleted(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	if err := st.AddProject(ctx, Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	a := archiveTestAgent(t, st, "pawly", "agent-01", "Login fix")
	if err := st.SetSecret(ctx, Secret{Project: "pawly", Agent: "agent-01", Name: "KEY", Value: []byte("v"), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddTokenRows(ctx, []TokenRow{{Project: "pawly", Agent: "agent-01", AgentID: a.ID, AI: "claude", Kind: "turn", At: time.Now(), Input: 10, Output: 5, CostUSD: 0.5}}); err != nil {
		t.Fatal(err)
	}

	pr := ArchivedPR{Number: 42, Title: "fix: login redirect", URL: "https://github.com/o/r/pull/42", State: PROpen}
	if err := st.ArchiveAgent(ctx, a, "", pr, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agent(ctx, "pawly", "agent-01"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the live agent is still there: %v", err)
	}
	got, err := st.ArchivedAgentByName(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Login fix" || got.Task != "Fix the login redirect" || got.Branch != "agentbox/agent-01" || got.PR != pr || got.ID != a.ID {
		t.Fatalf("archived as %+v", got)
	}
	if items, _ := st.ChatItems(ctx, "pawly", "agent-01"); len(items) != 2 {
		t.Fatalf("the chat kept %d items, want 2", len(items))
	}
	if c, _ := st.Chat(ctx, "pawly", "agent-01"); c.SessionID != "" || c.Options["model"] != "opus" {
		t.Fatalf("chat row is %+v: the session goes with the machine, the options stay", c)
	}
	if n := countRows(t, st, "secrets", "pawly"); n != 0 {
		t.Fatalf("the agent's secret survived it: %d", n)
	}
	sums, err := st.ArchivedTokens(ctx, []string{a.ID})
	if err != nil || sums[a.ID].Tokens != 15 || sums[a.ID].CostUSD != 0.5 {
		t.Fatalf("tokens %+v, %v", sums, err)
	}

	// The name is taken for good: neither added again nor handed out.
	if err := st.AddAgent(ctx, Agent{Project: "pawly", Name: "agent-01", Instance: "ab-other", AI: "claude", CreatedAt: time.Now()}); !errors.Is(err, ErrExists) {
		t.Fatalf("an archived agent's name was given again: %v", err)
	}
	if names, _ := st.PastAgentNames(ctx, "pawly"); len(names) != 1 || names[0] != "agent-01" {
		t.Fatalf("past names %v", names)
	}

	if err := st.DeleteArchivedAgent(ctx, "pawly", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchivedAgentByName(ctx, "pawly", "agent-01"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted, still there: %v", err)
	}
	if n := countRows(t, st, "chat_items", "pawly") + countRows(t, st, "chats", "pawly"); n != 0 {
		t.Fatalf("deleting left %d chat rows", n)
	}
	if n := countRows(t, st, "token_usage", "pawly"); n != 1 {
		t.Fatalf("token rows %d, want the one kept for accounting", n)
	}
	if err := st.DeleteArchivedAgent(ctx, "pawly", "agent-01"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}
}

func TestArchivedAgentsFilters(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	for _, p := range []string{"pawly", "other"} {
		if err := st.AddProject(ctx, Project{Name: p, Root: "/src/" + p, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	archive := func(project, name, title string, pr ArchivedPR, at time.Time) {
		t.Helper()
		a := archiveTestAgent(t, st, project, name, title)
		if err := st.ArchiveAgent(ctx, a, "", pr, at); err != nil {
			t.Fatal(err)
		}
	}
	archive("pawly", "agent-01", "Login fix", ArchivedPR{Number: 7, Title: "fix: login", State: PRMerged}, now.Add(-72*time.Hour))
	archive("pawly", "agent-02", "Dark mode", ArchivedPR{Number: 9, Title: "feat: dark mode", State: PROpen}, now.Add(-time.Hour))
	archive("other", "agent-01", "Docs", ArchivedPR{}, now)

	names := func(q ArchivedQuery) []string {
		t.Helper()
		found, err := st.ArchivedAgents(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, a := range found {
			out = append(out, a.Ref())
		}
		return out
	}
	for _, c := range []struct {
		name string
		q    ArchivedQuery
		want []string
	}{
		{"every project, newest first", ArchivedQuery{}, []string{"other/agent-01", "pawly/agent-02", "pawly/agent-01"}},
		{"one project", ArchivedQuery{Project: "pawly"}, []string{"pawly/agent-02", "pawly/agent-01"}},
		{"by title", ArchivedQuery{Query: "dark"}, []string{"pawly/agent-02"}},
		{"by task", ArchivedQuery{Query: "LOGIN REDIRECT"}, []string{"other/agent-01", "pawly/agent-02", "pawly/agent-01"}},
		{"by PR number", ArchivedQuery{Query: "#7"}, []string{"pawly/agent-01"}},
		{"by bare PR number", ArchivedQuery{Query: "7"}, []string{"pawly/agent-01"}},
		{"by PR number with pr", ArchivedQuery{Query: "PR #7"}, []string{"pawly/agent-01"}},
		{"by PR number, no #", ArchivedQuery{Query: "pr 7"}, []string{"pawly/agent-01"}},
		{"merged", ArchivedQuery{PRState: PRMerged}, []string{"pawly/agent-01"}},
		{"no PR", ArchivedQuery{PRState: PRNone}, []string{"other/agent-01"}},
		{"since", ArchivedQuery{Since: now.Add(-2 * time.Hour)}, []string{"other/agent-01", "pawly/agent-02"}},
		{"until", ArchivedQuery{Until: now.Add(-2 * time.Hour)}, []string{"pawly/agent-01"}},
		{"limit", ArchivedQuery{Limit: 1}, []string{"other/agent-01"}},
	} {
		got := names(c.q)
		if len(got) != len(c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
	if _, err := st.ArchivedAgents(ctx, ArchivedQuery{PRState: "draft"}); err == nil {
		t.Fatal("an unknown PR state was accepted")
	}

	// Chat search leaves archived chats out unless asked.
	hits, _, err := st.SearchChats(ctx, ChatSearch{Query: "redirect"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("archived chats found by default: %v, %v", hits, err)
	}
	hits, _, err = st.SearchChats(ctx, ChatSearch{Query: "redirect", Archived: true})
	if err != nil || len(hits) != 6 {
		t.Fatalf("with archived: %d hits, %v", len(hits), err)
	}
	hits, _, err = st.SearchChats(ctx, ChatSearch{Query: "redirect", Project: "pawly", Agent: "agent-02"})
	if err != nil || len(hits) != 2 {
		t.Fatalf("one archived chat by name: %d hits, %v", len(hits), err)
	}

	if err := st.SetArchivedPR(ctx, "pawly", "agent-02", ArchivedPR{Number: 9, Title: "feat: dark mode", State: PRMerged}); err != nil {
		t.Fatal(err)
	}
	if got := names(ArchivedQuery{PRState: PRMerged}); len(got) != 2 {
		t.Fatalf("after the PR merged: %v", got)
	}
	// Removing a project takes its past agents with it.
	if err := st.RemoveProject(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if got := names(ArchivedQuery{}); len(got) != 2 {
		t.Fatalf("after removing a project: %v", got)
	}
}

func TestArchiveRefusesALead(t *testing.T) {
	st := openRemoveTest(t)
	err := st.ArchiveAgent(context.Background(), Agent{Project: "pawly", Name: LeadName, Role: RoleLead}, "", ArchivedPR{}, time.Now())
	if err == nil {
		t.Fatal("a lead was archived")
	}
}
