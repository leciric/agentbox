package daemon

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// lostTurn is how an agent's open turn ends when its machine goes down under
// it: the prompt fails on the AI tool's closed output, and the tool's exit is
// reported on its own.
func lostTurn(d testDaemon, a state.Agent) {
	d.srv.agentFinished(a, chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "failed"}, Lost: true, Error: "connection closed"})
	d.srv.agentLost(a, "Claude Code exited (exit status 137): Killed")
}

// retireWhileTurnOpen sets up an agent that finished its work and is waiting
// on its pull request's checks in an open turn, and retires it with req.
func retireWhileTurnOpen(t *testing.T, req api.RetireRequest, background bool) (testDaemon, state.Agent, *[]string) {
	t.Helper()
	ctx := context.Background()
	d, a := newAutoStopIdleTest(t, "Running", time.Now().Add(-time.Hour))
	var mu sync.Mutex
	told := &[]string{}
	d.srv.stallTell = func(_ context.Context, _ string, note string) {
		mu.Lock()
		defer mu.Unlock()
		*told = append(*told, note)
	}
	if background {
		// A real chat, its turn ended on a CI watch left running: busy, so
		// only force retires it.
		a.AI = "claude"
		watchInBackground(t, d, a)
	}
	d.srv.leadAsked(a)
	// Its finish: done, with something to show.
	d.srv.setOutcome(a, api.AgentOutcomeDone, "")

	req.Agents = []string{a.Name}
	res, err := d.client.Retire(ctx, a.Project, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Retired) != 1 {
		t.Fatalf("retire %s: retired %+v, skipped %+v", req.How, res.Retired, res.Skipped)
	}
	return d, a, told
}

// An agent retired while its turn is open — stopped, its chat cancelled
// first — keeps its finish: no failure, no event, nobody told.
func TestRetireWhileATurnIsOpenIsNotAFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, a, told := retireWhileTurnOpen(t, api.RetireRequest{How: api.RetireStop}, false)
	// The retire cancels the open turn, and the AI tool exits with the
	// machine all the same.
	d.srv.agentFinished(a, chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "cancelled"}})
	lostTurn(d, a)

	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.kind != api.AgentOutcomeDone {
		t.Errorf("after the retire, the outcome is %+v, want done as it finished", o)
	}
	for _, k := range eventKinds(t, d, a.Project) {
		if k == api.AgentFailed || k == api.AgentChatLost {
			t.Errorf("events = %q: the retire recorded a failure", eventKinds(t, d, a.Project))
		}
	}
	if len(*told) != 0 {
		t.Errorf("the lead was told %q", *told)
	}

	// Started again, it is back: a crash now is a real one.
	if err := d.srv.startMachine(ctx, d.srv.manager(nil), a, "a test"); err != nil {
		t.Fatal(err)
	}
	lostTurn(d, a)
	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.kind != api.AgentOutcomeFailed {
		t.Errorf("a crash after it started again left %+v, want failed", o)
	}
	if !slices.Contains(eventKinds(t, d, a.Project), api.AgentChatLost) {
		t.Errorf("events = %q, want the crash", eventKinds(t, d, a.Project))
	}
	if len(*told) != 1 || !strings.Contains((*told)[0], "exit status 137") {
		t.Errorf("the lead was told %q, want the crash once", *told)
	}
}

// Forced, an agent waiting on its CI watch is retired all the same: whatever
// its AI tool does as the machine goes is the retire's doing too.
func TestForceRetireWhileATurnIsOpenIsNotAFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, a, told := retireWhileTurnOpen(t, api.RetireRequest{How: api.RetireStop, Force: true}, true)
	lostTurn(d, a)

	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.kind != api.AgentOutcomeDone {
		t.Errorf("after a forced retire, the outcome is %+v", o)
	}
	for _, k := range eventKinds(t, d, a.Project) {
		if k == api.AgentFailed || k == api.AgentChatLost {
			t.Errorf("events = %q: the forced retire recorded a failure", eventKinds(t, d, a.Project))
		}
	}
	if len(*told) != 0 {
		t.Errorf("the lead was told %q", *told)
	}
}

// Destroyed, the machine goes before the chat is stopped, so an open turn
// dies with it rather than being cancelled: not a failure either.
func TestDestroyWhileATurnIsOpenIsNotAFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	var told []string
	d.srv.stallTell = func(_ context.Context, _ string, note string) { told = append(told, note) }
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	d.srv.leadAsked(a)
	d.srv.setOutcome(a, api.AgentOutcomeDone, "")
	res, err := d.client.Retire(ctx, a.Project, api.RetireRequest{How: api.RetireDestroy, Force: true, Agents: []string{a.Name}})
	if err != nil || len(res.Retired) != 1 {
		t.Fatalf("retire destroy: %+v, %v", res, err)
	}
	lostTurn(d, a)
	for _, k := range eventKinds(t, d, a.Project) {
		if k == api.AgentFailed || k == api.AgentChatLost {
			t.Errorf("events = %q: the destroy recorded a failure", eventKinds(t, d, a.Project))
		}
	}
	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.kind != api.AgentOutcomeDone {
		t.Errorf("after the destroy, the outcome is %+v", o)
	}
	if len(told) != 0 {
		t.Errorf("the lead was told %q", told)
	}
}

// Nobody stopped it: an agent whose AI tool exits mid-turn has failed, and
// the lead hears so, as ever.
func TestACrashNobodyAskedForStillFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	var told []string
	d.srv.stallTell = func(_ context.Context, _ string, note string) { told = append(told, note) }
	a := state.Agent{Project: "p", Name: "agent-02", Title: "Import the catalogue"}
	d.srv.setOutcome(a, api.AgentOutcomeDone, "")
	d.srv.agentLost(a, "Claude Code exited (exit status 137): Killed")
	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.kind != api.AgentOutcomeFailed {
		t.Errorf("outcome %+v, want failed", o)
	}
	if len(told) != 1 {
		t.Errorf("the lead was told %q, want the crash", told)
	}

	// A stop that failed to happen leaves no mark behind.
	d.srv.expectStop(a.Ref())
	d.srv.agentBack(a.Ref())
	if d.srv.stopExpected(a.Ref()) {
		t.Error("the mark outlived the agent coming back")
	}
}
