package daemon

import (
	"context"
	"os"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// With pausing whole agents off, as it is by default, a machine still boots
// only once the VM has the memory for it: meanwhile its agent is On hold,
// marked in the database (so a daemon that restarts starts it again), with
// its place in line; it boots by itself as room frees, and the mark goes.
func TestBootWaitsOnHoldForMemory(t *testing.T) {
	t.Parallel()
	f := newFleetTest(t, false)
	ctx := context.Background()
	f.setAvailable(2, 4) // 2 GiB: a 1.25 GiB boot over a 1.6 GiB floor doesn't fit
	f.step(t)
	waiter := state.Agent{
		Project: "hello-stack", Name: "agent-03", Instance: "ab-hello-stack-agent-03", AI: "none",
		Branch: "agentbox/agent-03", Worktree: "/worktrees/agent-03", Status: state.AgentReady, CreatedAt: time.Now().Add(2 * time.Minute),
	}
	if err := f.srv.store.AddAgent(ctx, waiter); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.srv.admitMachine(ctx, waiter) }()
	waitFor(t, "the boot to wait On hold", func() bool {
		held := f.srv.memoryHold(waiter.Ref())
		a, _ := f.srv.store.Agent(ctx, waiter.Project, waiter.Name)
		return held != nil && held.Machine == "waiting" && held.Ahead == 0 && held.Why == api.HoldWhyMemory && a.Hold == state.HoldMemory
	})
	select {
	case err := <-done:
		t.Fatalf("booted with 2 GiB available: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if got := holdOf(mustAgent(t, f, waiter), "stopped"); got != api.HoldMemory {
		t.Errorf("the app is told hold %q", got)
	}
	f.setAvailable(8, 1)
	f.step(t)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the boot still waits with 8 GiB available")
	}
	if a := mustAgent(t, f, waiter); a.Hold != "" {
		t.Errorf("still On hold (%q) once it booted", a.Hold)
	}
	if held := f.srv.memoryHold(waiter.Ref()); held != nil {
		t.Errorf("still held: %+v", held)
	}
}

// While a build waits for memory, a new machine doesn't boot ahead of it.
func TestBootWaitsBehindHeldBuilds(t *testing.T) {
	t.Parallel()
	f := newFleetTest(t, false)
	f.srv.heavy.mu.Lock()
	f.srv.heavy.g.Ask(f.newer.Ref(), "k", "cargo build", time.Hour, time.Now())
	f.srv.heavy.g.Ask(f.newer.Ref(), "k2", "cargo test", time.Hour, time.Now())
	f.srv.heavy.mu.Unlock()
	f.srv.admit.mu.Lock()
	f.srv.admit.running = 2
	f.srv.admit.mu.Unlock()
	if !f.srv.buildsHeld() {
		t.Fatal("two builds asked and none started: they should be waiting")
	}
	f.srv.stepAdmission(time.Now(), roomyMem(), true)
	f.srv.admit.mu.Lock()
	f.srv.admit.a.Ask("hello-stack/agent-09", time.Now(), time.Now())
	f.srv.admit.mu.Unlock()
	f.srv.stepAdmission(time.Now(), roomyMem(), true)
	if held := f.srv.admitHold("hello-stack/agent-09"); held == nil || held.Why != api.HoldWhyBuilds {
		t.Errorf("held = %+v, want waiting behind the builds", held)
	}
}

func mustAgent(t *testing.T, f *fleetTest, a state.Agent) state.Agent {
	t.Helper()
	got, err := f.srv.store.Agent(context.Background(), a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func roomyMem() pressure.Mem {
	const gib = 1 << 30
	return pressure.Mem{Total: 16 * gib, Cap: 16 * gib, Available: 12 * gib, SwapTotal: 4 * gib, SwapFree: 4 * gib}
}

// A start request whose boot must wait for memory answers at once, the agent
// On hold, rather than waiting in the request for as long as the memory
// takes: the app gives up on a request after minutes.
func TestStartRequestDoesNotWaitForMemory(t *testing.T) {
	t.Parallel()
	f := newFleetTest(t, false)
	ctx := context.Background()
	f.setAvailable(2, 4)
	f.step(t)
	waiter := state.Agent{
		Project: "hello-stack", Name: "agent-03", Instance: "ab-hello-stack-agent-03", AI: "none",
		Branch: "agentbox/agent-03", Worktree: "/worktrees/agent-03", Status: state.AgentReady, CreatedAt: time.Now().Add(2 * time.Minute),
	}
	if err := f.srv.store.AddAgent(ctx, waiter); err != nil {
		t.Fatal(err)
	}
	// Its machine stopped, beside agent-01's running one.
	script := `#!/bin/sh
case "$1" in
  list) echo '[{"name": "ab-hello-stack-agent-01", "status": "Running"}, {"name": "ab-hello-stack-agent-03", "status": "Stopped"}]' ;;
  query) echo '{}' ;;
esac`
	if err := os.WriteFile(f.srv.cfg.Incus.Bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if !f.srv.bootWaits() {
		t.Fatal("with 2 GiB available a boot should wait")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := f.client.AgentAction(reqCtx, waiter.Ref(), "start"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		if t.Failed() {
			t.Log(f.log.String())
		}
	}()
	waitFor(t, "the boot to wait On hold", func() bool {
		return mustAgent(t, f, waiter).Hold == state.HoldMemory
	})
}

// A boot whose wait is given up on (its request cancelled) takes the agent
// off hold: nothing would ever boot it from there, and it would show as
// waiting for good.
func TestCancelledBootLeavesNoHold(t *testing.T) {
	t.Parallel()
	f := newFleetTest(t, false)
	ctx := context.Background()
	f.setAvailable(2, 4)
	f.step(t)
	waiter := state.Agent{
		Project: "hello-stack", Name: "agent-03", Instance: "ab-hello-stack-agent-03", AI: "none",
		Branch: "agentbox/agent-03", Worktree: "/worktrees/agent-03", Status: state.AgentReady, CreatedAt: time.Now().Add(2 * time.Minute),
	}
	if err := f.srv.store.AddAgent(ctx, waiter); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- f.srv.admitBoot(waitCtx, waiter) }()
	waitFor(t, "the boot to wait On hold", func() bool { return mustAgent(t, f, waiter).Hold == state.HoldMemory })
	cancel()
	if err := <-done; err == nil {
		t.Fatal("a cancelled wait booted")
	}
	if a := mustAgent(t, f, waiter); a.Hold != "" {
		t.Errorf("still On hold (%q) after its wait was given up", a.Hold)
	}
}
