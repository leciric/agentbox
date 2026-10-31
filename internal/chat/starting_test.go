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

// A deleted session's id goes to the next new one, so its ref is used again,
// and the app still holds the old thread under it: the new conversation's
// events are numbered on from the old one's, past a gap, so the app reads it
// again rather than skipping them as events it has seen (lib/chat.ts advance),
// which lost the new session's first message and its answer.
func TestAConversationMadeAgainUnderAForgottenRefNumbersItsEventsOn(t *testing.T) {
	store := openStore(t)
	lead := state.Agent{Project: "hello", Name: state.LeadSessionName("session"), Role: state.RoleLead, AI: "claude", Worktree: "/work/hello"}
	f := newFakeTool(answerHello)
	m, rec := newManager(t, store, f)
	if _, err := m.Send(lead, "hello there"); err != nil {
		t.Fatal(err)
	}
	old := waitThread(t, m, lead, "the first session's turn", turnsEnded(1)).Seq
	m.Forget(lead.Ref())
	last := rec.all()[len(rec.all())-1].Seq
	if last < old {
		t.Fatalf("last event %d before the thread's %d", last, old)
	}

	from := len(rec.all())
	if _, err := m.Send(lead, "a new session"); err != nil {
		t.Fatal(err)
	}
	evs := rec.all()[from:]
	if len(evs) == 0 {
		t.Fatal("the new conversation published nothing")
	}
	if evs[0].Seq <= last+1 {
		t.Errorf("the new conversation's first event is %d after the forgotten one's last %d: want past a gap, so the app reads it again", evs[0].Seq, last)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq != evs[i-1].Seq+1 {
			t.Fatalf("events %d then %d: want them numbered one after the other", evs[i-1].Seq, evs[i].Seq)
		}
	}
	th, err := m.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	if th.Seq <= last+1 {
		t.Errorf("the new thread's seq %d, want past the forgotten %d", th.Seq, last)
	}
}
