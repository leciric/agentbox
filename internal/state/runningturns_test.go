package state_test

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/state"
)

// TestRunningTurns: a chat keeps one running turn, replaced by the next;
// ending an older turn leaves the newer one, and each kind of chat says
// which it is.
func TestRunningTurns(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	save := func(rt state.RunningTurn) {
		t.Helper()
		if err := st.SaveRunningTurn(ctx, rt); err != nil {
			t.Fatal(err)
		}
	}
	save(state.RunningTurn{Project: "p", Agent: "agent-01", Kind: state.TurnOfAgent, Turn: "t1", Prompt: "build it", StartedAt: now})
	save(state.RunningTurn{Project: "p", Agent: "lead", Kind: state.TurnOfLead, Turn: "l1", Prompt: "plan", StartedAt: now.Add(time.Second)})
	save(state.RunningTurn{Project: "p", Agent: "agent-01", Kind: state.TurnOfAgent, Turn: "t2", Prompt: "carry on", StartedAt: now.Add(2 * time.Second), Resumes: 1})
	if err := st.EndRunningTurn(ctx, "p", "agent-01", "t1"); err != nil {
		t.Fatal(err)
	}
	got, err := st.RunningTurns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Agent != "lead" || got[1].Turn != "t2" || got[1].Resumes != 1 || !got[1].StartedAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("RunningTurns() = %+v, want the lead's, then agent-01's t2 resumed once", got)
	}
	if err := st.EndRunningTurn(ctx, "p", "agent-01", "t2"); err != nil {
		t.Fatal(err)
	}
	if err := st.DropRunningTurn(ctx, "p", "lead"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.RunningTurns(ctx); len(got) != 0 {
		t.Fatalf("RunningTurns() after ending both = %+v", got)
	}
	for _, c := range []struct {
		a    state.Agent
		want string
	}{
		{state.Agent{Project: "p", Name: "agent-01", Role: state.RoleWorker}, state.TurnOfAgent},
		{state.Agent{Project: "p", Name: state.LeadName, Role: state.RoleLead}, state.TurnOfLead},
		{state.Agent{Project: state.HomeProject, Name: state.LeadName, Role: state.RoleLead}, state.TurnOfHome},
	} {
		if got := state.TurnKind(c.a); got != c.want {
			t.Errorf("TurnKind(%s) = %q, want %q", c.a.Ref(), got, c.want)
		}
	}
}

// A held turn stays held, read back as such, until the chat's next turn
// replaces it.
func TestAHeldRunningTurnStaysHeldUntilTheNextTurn(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now()
	rt := state.RunningTurn{Project: "p", Agent: "agent-01", Kind: state.TurnOfAgent, Turn: "t1", Prompt: "build it", StartedAt: now}
	if err := st.SaveRunningTurn(ctx, rt); err != nil {
		t.Fatal(err)
	}
	if err := st.HoldRunningTurn(ctx, "p", "agent-01", "t0", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.RunningTurns(ctx); len(got) != 1 || got[0].Held {
		t.Fatalf("RunningTurns() = %+v; holding another turn held this one", got)
	}
	if err := st.HoldRunningTurn(ctx, "p", "agent-01", "t1", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.RunningTurns(ctx); len(got) != 1 || !got[0].Held {
		t.Fatalf("RunningTurns() = %+v; want t1 held", got)
	}
	rt.Turn = "t2"
	if err := st.SaveRunningTurn(ctx, rt); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.RunningTurns(ctx); len(got) != 1 || got[0].Held || got[0].Turn != "t2" {
		t.Fatalf("RunningTurns() = %+v; want t2, not held", got)
	}
}

// Automatic resumes are counted across restarts in runs: one whose last was
// before the window starts a run of its own, and forgetting ends it.
func TestAutoResumesCountInRuns(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	t0 := time.Now().Add(-3 * time.Hour)
	count := func(at time.Time) {
		t.Helper()
		if err := st.CountAutoResume(ctx, "p", "agent-01", at, at.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	n := func(now time.Time) int {
		t.Helper()
		got, err := st.AutoResumes(ctx, "p", "agent-01", now.Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := n(t0); got != 0 {
		t.Fatalf("AutoResumes() = %d before any", got)
	}
	count(t0)
	count(t0.Add(20 * time.Minute))
	count(t0.Add(40 * time.Minute))
	if got := n(t0.Add(50 * time.Minute)); got != 3 {
		t.Errorf("AutoResumes() = %d after three within the hour of each other, want 3", got)
	}
	if got := n(t0.Add(2 * time.Hour)); got != 0 {
		t.Errorf("AutoResumes() = %d over an hour after the last, want 0", got)
	}
	count(t0.Add(2 * time.Hour))
	if got := n(t0.Add(2 * time.Hour)); got != 1 {
		t.Errorf("AutoResumes() = %d; one over an hour after the last starts a new run", got)
	}
	if got, _ := st.AutoResumes(ctx, "p", "agent-02", t0); got != 0 {
		t.Errorf("another agent's AutoResumes() = %d", got)
	}
	if err := st.ForgetAutoResumes(ctx, "p", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if got := n(t0.Add(2 * time.Hour)); got != 0 {
		t.Errorf("AutoResumes() = %d after ForgetAutoResumes", got)
	}
}
