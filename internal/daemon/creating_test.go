package daemon

import (
	"context"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// An agent whose create job is still running is starting, whatever its chat
// says while the machine comes up: it shows no error or outcome, and a turn
// that failed on the half-made machine tells nobody anything.
func TestAnAgentMidCreateNeverShowsAnErrorOrFires(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "New")
	if err := d.srv.store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentCreating); err != nil {
		t.Fatal(err)
	}
	a.Status = state.AgentCreating

	failed := chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "failed"}, Error: "the machine is not running"}
	d.srv.agentFinished(a, failed)

	info := d.srv.agentInfo(agent.Status{Agent: a, State: "initializing"})
	if info.State != "initializing" || info.Chat != "" || info.Outcome != "" || info.OutcomeNote != "" || info.ChangesRequested != "" {
		t.Errorf("a mid-create agent reads %+v, want initializing with no chat, outcome or review", info)
	}
	if kinds := eventKinds(t, d, a.Project); len(kinds) != 0 {
		t.Errorf("events for a mid-create agent = %q, want none", kinds)
	}
	notes, err := d.client.Notifications(ctx)
	if err == nil && len(notes) != 0 {
		t.Errorf("notifications for a mid-create agent = %d, want none", len(notes))
	}
}
