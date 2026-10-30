package chat

import (
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A new session's tool takes seconds to start, and the composer doesn't wait
// for it: a message sent meanwhile is taken at once, says it waits for the
// tool, and is the turn the tool runs once it is ready.
func TestAMessageSentWhileTheToolStartsWaitsAndGoesIn(t *testing.T) {
	store := openStore(t)
	lead := state.Agent{Project: "hello", Name: state.LeadName, Role: state.RoleLead, AI: "claude", Worktree: "/work/hello"}
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	starting := make(chan struct{})
	release := make(chan struct{})
	f.during = func(method, _ string) {
		if method == acp.MethodSessionNew {
			close(starting)
			<-release
		}
	}
	if _, err := m.Start(lead); err != nil {
		t.Fatal(err)
	}
	<-starting
	it, err := m.Send(lead, "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if it.Kind != "user" || it.Delivery != api.ChatAsideStarting {
		t.Errorf("sent while starting: kind %q, delivery %q; want a user message marked %q", it.Kind, it.Delivery, api.ChatAsideStarting)
	}
	th, err := m.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	if th.Session.State != api.ChatStarting {
		t.Errorf("session %q while the tool starts, want %q", th.Session.State, api.ChatStarting)
	}
	if n := len(f.called(acp.MethodSessionPrompt)); n != 0 {
		t.Errorf("%d prompts before the tool was ready, want none", n)
	}
	close(release)
	th = waitThread(t, m, lead, "the turn", turnsEnded(1))
	if got := find(th, "user", 0); got.Delivery != "" {
		t.Errorf("once it went in, delivery = %q, want none", got.Delivery)
	}
	if n := len(f.called(acp.MethodSessionPrompt)); n != 1 {
		t.Errorf("%d prompts, want the message's one", n)
	}
}

// A message to an agent whose machine is still being made waits in its chat,
// marked starting, without starting a tool that has no machine to run in; the
// daemon releases it once the agent is ready.
func TestAMessageHeldWhileTheMachineIsMadeGoesInOnRelease(t *testing.T) {
	store := openStore(t)
	a := state.Agent{Project: "hello", Name: "agent-01", AI: "claude", Worktree: "/work/agent-01", Status: state.AgentCreating}
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	it, err := m.HoldStarting(a, api.ChatMessageRequest{Text: "and add tests"})
	if err != nil {
		t.Fatal(err)
	}
	if it.Delivery != api.ChatAsideStarting {
		t.Errorf("held: delivery %q, want %q", it.Delivery, api.ChatAsideStarting)
	}
	if n := len(f.launchedOn()); n != 0 {
		t.Fatalf("holding launched the tool %d times, want none", n)
	}
	a.Status = state.AgentReady
	if err := m.Release(a); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, a, "the released turn", turnsEnded(1))
	if got := find(th, "user", 0); got.Text != "and add tests" || got.Delivery != "" {
		t.Errorf("released turn = %q, delivery %q; want the held message, delivered", got.Text, got.Delivery)
	}
}
