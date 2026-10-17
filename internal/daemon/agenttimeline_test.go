package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// An agent's timeline is read from its whole chat and its media, and stays
// readable once the agent is past.
func TestAgentTimeline(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project: "hello-stack", Name: "agent-07", Instance: "ab-hello-stack-agent-07", AI: "claude", Branch: "agentbox/agent-07",
		Worktree: "/gone/agent-07", Status: state.AgentReady, CreatedAt: time.Now(), Interface: state.InterfaceChat,
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	items := []api.ChatItem{
		{ID: "u1", Turn: "u1", Kind: "user", Text: "Fix the footer", Result: &api.ChatTurnResult{State: "done"}, CreatedAt: t0, UpdatedAt: t0},
		{ID: "t1", Turn: "u1", Kind: "tool", Tool: &api.ChatTool{Kind: "execute", Command: "go test ./...", Status: "completed", Output: "ok"}, CreatedAt: t0.Add(time.Minute), UpdatedAt: t0.Add(2 * time.Minute)},
	}
	var rows []state.ChatItem
	for i, it := range items {
		data, _ := json.Marshal(it)
		rows = append(rows, state.ChatItem{ID: it.ID, Position: int64(i), Data: data})
	}
	if err := d.srv.store.SaveChatItems(ctx, a.Project, a.Name, rows); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.AddMedia(ctx, state.Media{ID: "shot", Project: a.Project, Agent: a.Name, Kind: "screenshot", Name: "after", Source: "agent", Meta: "{}", CreatedAt: t0.Add(90 * time.Second)}); err != nil {
		t.Fatal(err)
	}

	checkTimeline(t, d, a.Ref())
	if a, err := d.srv.store.Agent(ctx, a.Project, a.Name); err != nil {
		t.Fatal(err)
	} else if err := d.srv.store.ArchiveAgent(ctx, a, "", state.ArchivedPR{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	checkTimeline(t, d, a.Ref())
	if _, err := d.client.AgentTimeline(ctx, a.Project+"/agent-nope"); err == nil {
		t.Error("an agent that doesn't exist has a timeline")
	}
}

func checkTimeline(t *testing.T, d testDaemon, ref string) {
	t.Helper()
	tl, err := d.client.AgentTimeline(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Steps) != 2 || tl.Steps[0].Kind != "task" || tl.Steps[1].Kind != "test" || tl.Steps[1].Status != "passed" {
		t.Fatalf("steps: %+v", tl.Steps)
	}
	if m := tl.Steps[1].Media; len(m) != 1 || m[0].ID != "shot" || m[0].Agent != ref {
		t.Errorf("media: %+v", m)
	}
}
