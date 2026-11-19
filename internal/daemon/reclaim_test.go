package daemon

import (
	"context"
	"testing"

	"agentbox/internal/state"
)

// stoppedMachineCgroups is fakeMachineCgroups with no machine running.
type stoppedMachineCgroups struct{ fakeMachineCgroups }

func (*stoppedMachineCgroups) Running(string) (bool, bool) { return false, false }

// A reclaim decided earlier never reaches an agent that is gone, not ready
// (stopped, On hold, being removed), or whose machine stopped meanwhile: on
// 2026-11-06 one spun on a machine no longer in the daemon's list.
func TestMayReclaimOnlyReadyRunningAgents(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	ctx := context.Background()
	p.srv.machineCgroups = &fakeMachineCgroups{}
	if !p.srv.mayReclaim(ctx, p.instance) {
		t.Fatal("a ready, running agent may not be reclaimed")
	}
	if p.srv.mayReclaim(ctx, "ab-hello-stack-agent-37") || p.srv.mayReclaim(ctx, "") {
		t.Error("an instance the daemon doesn't know may be reclaimed")
	}
	p.srv.machineCgroups = &stoppedMachineCgroups{}
	if p.srv.mayReclaim(ctx, p.instance) {
		t.Error("an agent whose machine stopped may be reclaimed")
	}
	p.srv.machineCgroups = &fakeMachineCgroups{}
	a, err := p.srv.store.AgentByInstance(ctx, p.instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.srv.store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentDestroying); err != nil {
		t.Fatal(err)
	}
	if p.srv.mayReclaim(ctx, p.instance) {
		t.Error("an agent being removed may be reclaimed")
	}
}
