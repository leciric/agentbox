package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/brief"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

func recapStore(t *testing.T) (*state.Store, *memory.Store) {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.AddProject(context.Background(), state.Project{Name: "pawly", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return st, memory.New(st.DB())
}

// The recap is what a chat started after a compaction is given in place of the
// conversation: what the project is doing, the narrative of the conversation
// so far, and what is still open.
func TestLeadRecap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, mem := recapStore(t)
	m := &Manager{Store: st}

	// Before anything has been remembered there is nothing to say, and the
	// brief leaves the section out rather than printing an empty one.
	recap, err := m.leadRecap(ctx, "pawly")
	if err != nil || recap != "" {
		t.Fatalf("leadRecap() on an empty project = %q, %v", recap, err)
	}

	if _, err := mem.SetWorkingMemory(ctx, "pawly", memory.WorkingMemoryPatch{
		Goal:         ptr("Ship the reminders page"),
		CurrentTask:  ptr("Pagination"),
		ActiveAgents: &[]string{"agent-04"},
	}); err != nil {
		t.Fatal(err)
	}
	event, err := mem.AppendEvent(ctx, memory.Event{Project: "pawly", Type: memory.EventConversationCompacted})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []memory.Memory{
		{Kind: memory.KindEpisodic, Title: "The chat up to today", Content: "The user asked for pagination; agent-04 is on it.", SourceEventID: event.ID},
		{Kind: memory.KindIssue, Title: "The count query is unindexed", Content: "It scans the whole table."},
		{Kind: memory.KindProject, Title: "Reminders live in internal/reminders", Content: "The page reads them through the API."},
	} {
		want.Project = "pawly"
		if _, err := mem.AddMemory(ctx, want); err != nil {
			t.Fatal(err)
		}
	}

	recap, err = m.leadRecap(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Ship the reminders page",       // working memory
		"agent-04",                      // ...and who is on it
		"The user asked for pagination", // the narrative
		"count query is unindexed",      // what is still open
	} {
		if !strings.Contains(recap, want) {
			t.Errorf("the recap doesn't mention %q:\n%s", want, recap)
		}
	}
	// The next compaction's narrative supersedes this one, the way
	// storeConsolidation writes it, so the recap is the newest one only: a
	// pile of stale summaries would be worse than none.
	was, err := mem.LatestFrom(ctx, "pawly", memory.KindEpisodic, memory.EventConversationCompacted)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := mem.AppendEvent(ctx, memory.Event{Project: "pawly", Type: memory.EventConversationCompacted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.AddMemory(ctx, memory.Memory{
		Project: "pawly", Kind: memory.KindEpisodic, Title: "The chat up to now",
		Content: "Pagination landed; the user moved on to search.", SupersedesID: was.ID, SourceEventID: newer.ID,
	}); err != nil {
		t.Fatal(err)
	}
	recap, err = m.leadRecap(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recap, "moved on to search") || strings.Contains(recap, "The user asked for pagination") {
		t.Errorf("the recap isn't the newest narrative:\n%s", recap)
	}
}

// A worker agent's brief carries its own slice of the same memory: smaller
// than the lead's, picked out by what this agent was asked to do, and said to
// be a summary so the agent searches rather than assuming it has everything.
func TestAgentBriefCarriesWhatTheProjectKnows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, mem := recapStore(t)
	m := &Manager{Store: st}
	// No title, so the query is the task and nothing else: what this checks is
	// that the task reaches the search.
	agent := state.Agent{Project: "pawly", Name: "agent-07"}

	// Nothing remembered, no section: a brief with an empty heading in it is
	// worse than a brief without one.
	knowledge, err := m.projectKnowledge(ctx, agent, "Add OAuth to the login page")
	if err != nil || knowledge != "" {
		t.Fatalf("projectKnowledge() on an empty project = %q, %v", knowledge, err)
	}

	for _, want := range []memory.Memory{
		{Kind: memory.KindDiscovery, Title: "The OAuth callback needs the exact port",
			Content: "Google rejects a redirect URI whose port isn't registered."},
		{Kind: memory.KindProject, Title: "Reminders live in internal/reminders", Importance: 5,
			Content: "Nothing to do with logging in."},
	} {
		want.Project = "pawly"
		if _, err := mem.AddMemory(ctx, want); err != nil {
			t.Fatal(err)
		}
	}

	knowledge, err = m.projectKnowledge(ctx, agent, "Add OAuth to the login page")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(knowledge, "OAuth callback needs the exact port") {
		t.Errorf("an agent working on OAuth isn't told what the project knows about OAuth:\n%s", knowledge)
	}

	// A brief rewritten later — when the project's notes change, or its
	// budget does — isn't handed the task, and recovers it from the
	// agent_created event the daemon captured. Without it there is nothing to
	// search for, and the section is only what the project knows generally.
	general, err := m.projectKnowledge(ctx, agent, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(general, "OAuth callback") {
		t.Errorf("a brief with no task to go on found OAuth anyway:\n%s", general)
	}
	if _, err := mem.AppendEvent(ctx, memory.Event{
		Project: "pawly", Agent: "agent-07", Type: "agent_created",
		Payload: json.RawMessage(`{"title":"OAuth login","task":"Add OAuth to the login page"}`),
	}); err != nil {
		t.Fatal(err)
	}
	recovered, err := m.projectKnowledge(ctx, agent, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recovered, "OAuth callback needs the exact port") {
		t.Errorf("a rewritten brief doesn't recover the task:\n%s", recovered)
	}

	// And it reaches the brief, under a heading that tells the agent it is a
	// summary and where to go for the rest.
	text, err := brief.Render(brief.Data{
		Project: "pawly", Agent: "agent-07",
		Worktree: "/tmp/pawly/agent-07", Branch: "agentbox/agent-07", BaseRef: "main",
		Knowledge: knowledge,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## What the project knows",
		"This is a summary, picked out for your task and cut to fit",
		"search_memory",
		"OAuth callback needs the exact port",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the agent's brief doesn't have %q", want)
		}
	}

	// A worker's slice is smaller than its chat's: the chat's recap stands in
	// for a conversation that is gone, a worker's is a starting point.
	budget, err := m.contextBudget(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if memory.AgentBudget(budget) >= budget {
		t.Errorf("an agent's budget is %d of the project's %d", memory.AgentBudget(budget), budget)
	}
}

func ptr[T any](v T) *T { return &v }
