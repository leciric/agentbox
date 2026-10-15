package daemon

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// reviewFixture is a project with agents that finished, each listed ready to
// review.
func reviewFixture(t *testing.T, names ...string) (testDaemon, string, []state.Agent) {
	t.Helper()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	var agents []state.Agent
	for _, n := range names {
		a := addAgent(t, d, repo, "hello-stack", n, "Task "+n)
		d.srv.record(ctx, finishedEvent(a, api.AgentChanges{}, nil, "Done.", time.Now()))
		agents = append(agents, a)
	}
	return d, repo, agents
}

func isArchived(t *testing.T, d testDaemon, a state.Agent) bool {
	t.Helper()
	_, err := d.srv.store.ArchivedAgentByName(context.Background(), a.Project, a.Name)
	return err == nil
}

func agentHasMachine(d testDaemon, a state.Agent) bool {
	_, err := d.srv.store.Agent(context.Background(), a.Project, a.Name)
	return err == nil
}

// Reviewing an agent whose work is all committed and pushed gives its machine
// back: it becomes a past agent.
func TestReviewedAgentWithNothingToLoseIsRetired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, _, agents := reviewFixture(t, "agent-01")
	a := agents[0]
	if err := d.client.Review(ctx, a.Ref(), api.ReviewRequest{Verdict: api.ReviewReviewed}); err != nil {
		t.Fatal(err)
	}
	if agentHasMachine(d, a) || !isArchived(t, d, a) {
		t.Fatalf("a reviewed agent with nothing to lose kept its machine (live %v, past %v)", agentHasMachine(d, a), isArchived(t, d, a))
	}
	if events := eventsOfType(t, d, "hello-stack", "agent_retired"); len(events) != 1 || events[0].Agent != a.Name {
		t.Errorf("agent_retired events = %+v, want one for %s", events, a.Name)
	}
}

// A dismissed agent isn't a reviewed one: it keeps its machine.
func TestDismissedAgentKeepsItsMachine(t *testing.T) {
	t.Parallel()
	d, _, agents := reviewFixture(t, "agent-01")
	if err := d.client.Review(context.Background(), agents[0].Ref(), api.ReviewRequest{Verdict: api.ReviewDismissed}); err != nil {
		t.Fatal(err)
	}
	if !agentHasMachine(d, agents[0]) {
		t.Fatal("dismissing freed the machine")
	}
}

// Uncommitted or unpushed work keeps the machine, says so on the item, and
// tells the lead.
func TestReviewedAgentWithWorkOnlyOnTheMachineIsKept(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, _, agents := reviewFixture(t, "agent-01", "agent-02")
	dirty, unpushedAgent := agents[0], agents[1]
	holdMachine(t, dirty)
	commitOn(t, unpushedAgent, "feature.txt")

	for _, a := range agents {
		if err := d.client.Review(ctx, a.Ref(), api.ReviewRequest{Verdict: api.ReviewReviewed}); err != nil {
			t.Fatal(err)
		}
		if !agentHasMachine(d, a) || isArchived(t, d, a) {
			t.Errorf("%s lost its machine with work only there", a.Name)
		}
	}
	done, err := d.client.Reviewed(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 {
		t.Fatalf("Reviewed = %+v, want both", done)
	}
	for _, i := range done {
		if !i.MachineKept {
			t.Errorf("%s isn't marked machine kept", i.Agent)
		}
	}
}

// An agent still at work isn't interrupted: its machine goes when its turn
// ends, and not if the user asked it for changes meanwhile.
func TestReviewedWhileWorkingFreesWhenTheTurnEnds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, _, agents := reviewFixture(t, "agent-01", "agent-02")
	waits, asked := agents[0], agents[1]
	d.srv.setFreeWhenIdle(waits.Ref(), true)
	d.srv.setFreeWhenIdle(asked.Ref(), true)
	d.srv.setFreeWhenIdle(asked.Ref(), false) // what a request for changes does

	d.srv.freeAfterTurn(ctx, waits)
	d.srv.freeAfterTurn(ctx, asked)
	if agentHasMachine(d, waits) || !isArchived(t, d, waits) {
		t.Error("the machine wasn't freed when the turn ended")
	}
	if !agentHasMachine(d, asked) {
		t.Error("an agent with no pending free lost its machine")
	}
}

// Merging a pull request from the review frees the machine, so the review the
// app saves after it finds a past agent: that is no error, with or without
// the fingerprint it saw, and the verdict is kept.
func TestReviewAfterTheMachineWasFreedSucceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, _, agents := reviewFixture(t, "agent-01")
	a := agents[0]
	p, err := d.srv.store.Project(ctx, a.Project)
	if err != nil {
		t.Fatal(err)
	}
	items := d.srv.reviewItems(ctx, p, agents)
	fingerprint := items[0].Fingerprint
	d.srv.freeReviewed(ctx, a, "its pull request #1 was merged", nil)
	if agentHasMachine(d, a) || !isArchived(t, d, a) {
		t.Fatal("the machine was not freed")
	}
	if err := d.client.Review(ctx, a.Ref(), api.ReviewRequest{Verdict: api.ReviewReviewed, Fingerprint: fingerprint}); err != nil {
		t.Fatalf("review with a fingerprint after the free: %v", err)
	}
	if err := d.client.Review(ctx, a.Ref(), api.ReviewRequest{Verdict: api.ReviewReviewed}); err != nil {
		t.Fatalf("review without one after the free: %v", err)
	}
	if r, ok, _ := d.srv.store.AgentReviewOf(ctx, a.Project, a.Name); !ok || r.Verdict != api.ReviewReviewed {
		t.Errorf("verdict = %+v, %v; want reviewed", r, ok)
	}
	if err := d.client.Review(ctx, a.Project+"/agent-99", api.ReviewRequest{Verdict: api.ReviewReviewed}); err == nil {
		t.Error("an agent that never existed was accepted")
	}
}
