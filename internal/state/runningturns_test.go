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
