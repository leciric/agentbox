package state

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/api"
)

// An agent's task is kept, goal and all, until it is handed over; the agent
// going takes it with it, and an agent retired before its task ever reached
// its chat is archived with the task as what it was asked to do.
func TestAgentTasksAreKeptUntilHandedOver(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	if err := st.AddProject(ctx, Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	task := "Move agents' worktrees onto the VM's disk.\n\n- keep Open in editor working\n- leave the hub alone"
	goal := &api.GoalSpec{Objective: "a PR is open", Checks: []api.GoalCheck{{Kind: api.GoalCheckPROpen}}, Budget: 3}
	at := time.Now().Truncate(time.Millisecond)
	for _, tk := range []AgentTask{
		{Project: "pawly", Agent: "agent-02", Text: "second", CreatedAt: at.Add(time.Second)},
		{Project: "pawly", Agent: "agent-01", Text: task, Goal: goal, ByLead: true, CreatedAt: at},
	} {
		if err := st.SaveAgentTask(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveAgentTask(ctx, AgentTask{Project: "pawly", Agent: "agent-03", CreatedAt: at}); err == nil {
		t.Error("an empty task was kept")
	}

	got, ok, err := st.AgentTaskOf(ctx, "pawly", "agent-01")
	if err != nil || !ok {
		t.Fatalf("AgentTaskOf(agent-01) = %v, %v", ok, err)
	}
	if got.Text != task || !got.ByLead || !got.CreatedAt.Equal(at) || got.Goal == nil ||
		got.Goal.Objective != "a PR is open" || len(got.Goal.Checks) != 1 || got.Goal.Budget != 3 {
		t.Fatalf("AgentTaskOf(agent-01) = %+v (goal %+v)", got, got.Goal)
	}
	all, err := st.AgentTasks(ctx)
	if err != nil || len(all) != 2 || all[0].Agent != "agent-01" || all[1].Agent != "agent-02" || all[1].Goal != nil {
		t.Fatalf("AgentTasks() = %+v, %v; want agent-01's, then agent-02's", all, err)
	}

	if err := st.DeleteAgentTask(ctx, "pawly", "agent-02"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.AgentTaskOf(ctx, "pawly", "agent-02"); ok {
		t.Error("agent-02's task is still kept after it was handed over")
	}

	// Removing an agent takes its task with it.
	a := archiveTestAgent(t, st, "pawly", "agent-04", "Gone")
	if err := st.SaveAgentTask(ctx, AgentTask{Project: "pawly", Agent: a.Name, Text: "never sent", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveAgent(ctx, "pawly", a.Name); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.AgentTaskOf(ctx, "pawly", a.Name); ok {
		t.Error("a removed agent's task is still kept")
	}

	// agent-01 is retired with an empty chat: the task it never got is what
	// the archive says it was asked.
	if err := st.AddAgent(ctx, Agent{
		Project: "pawly", Name: "agent-01", Instance: "ab-pawly-agent-01", AI: "claude", Branch: "agentbox/agent-01",
		BaseRef: "main", BaseCommit: "abc", Worktree: "/w/agent-01", Status: AgentCreating, CreatedAt: at, Interface: InterfaceChat,
	}); err != nil {
		t.Fatal(err)
	}
	a, err = st.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ArchiveAgent(ctx, a, "", ArchivedPR{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	archived, err := st.ArchivedAgentByName(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if archived.Task != task {
		t.Errorf("archived with task %q, want the one it never got", archived.Task)
	}
	if _, ok, _ := st.AgentTaskOf(ctx, "pawly", "agent-01"); ok {
		t.Error("an archived agent's task is still kept")
	}
}
