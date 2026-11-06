package daemon

import (
	"context"
	"testing"
	"time"

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
	if p.srv.mayReclaimRun(ctx, p.instance, "never-placed") {
		t.Error("a run with no processes may be reclaimed")
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

// What the live VM showed on 2026-11-06: builds waiting for memory asked
// reclaimIdleSoon every 30 seconds, and it pushed out the same idle agents
// every time, whatever the setting, one more stuck writer on each machine
// every 90 seconds. Now it obeys Settings → Idle reclaim, and reclaims an
// agent at most once every idleReclaimAgain.
func TestReclaimIdleSoonObeysTheSettingAndDoesntRepeat(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{false, true} {
		d, a, cg := newIdleReclaimTest(t, on)
		d.srv.machineCgroups = &fakeMachineCgroups{}
		reclaims := func() int {
			cg.mu.Lock()
			defer cg.mu.Unlock()
			return len(cg.reclaimed)
		}
		// A build waits, and waits again 30 seconds later, and again.
		for range 3 {
			d.srv.idle.mu.Lock()
			d.srv.idle.reclaimedSoon = time.Time{}
			d.srv.idle.mu.Unlock()
			d.srv.reclaimIdleSoon(context.Background())
			time.Sleep(50 * time.Millisecond)
		}
		want := 0
		if on {
			want = 1
			waitFor(t, "the idle agent to be reclaimed", func() bool { return reclaims() >= 1 })
		}
		time.Sleep(100 * time.Millisecond)
		if n := reclaims(); n != want {
			t.Errorf("idle reclaim on=%v: %s reclaimed %d times, want %d", on, a.Ref(), n, want)
		}
	}
}
