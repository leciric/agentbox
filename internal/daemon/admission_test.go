package daemon

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// waiterAgent is a third agent, made after the boot test's two, whose
// machine asks to boot.
func waiterAgent(t *testing.T, f *bootTest) state.Agent {
	t.Helper()
	a := state.Agent{
		Project: "hello-stack", Name: "agent-03", Instance: "ab-hello-stack-agent-03", AI: "none",
		Branch: "agentbox/agent-03", Worktree: "/worktrees/agent-03", Status: state.AgentReady, CreatedAt: time.Now().Add(2 * time.Minute),
	}
	if err := f.srv.store.AddAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

// belowFloor has the host report, as its supervisor does, that it is below
// its hard floor (on) or not. Tests that use it give the daemon a reading
// first (setAvailable): a test daemon with none, and no loop, boots at once.
func (f *bootTest) belowFloor(t *testing.T, on bool) {
	t.Helper()
	load := api.HostLoad{OS: "linux", Memory: 0, HoldBoots: on, MemoryTotal: 30 << 30, Available: 1 << 30, At: time.Now()}
	if err := f.client.PutHostLoad(context.Background(), load); err != nil {
		t.Fatal(err)
	}
}

// A machine boots when asked, never held for the VM's memory, nor for
// builds, nor for the host being busy or under its reserve (19 Nov 2026,
// the user's decision): the readings of that day held every boot for twenty
// minutes and more.
func TestBootDoesNotWaitForMemory(t *testing.T) {
	t.Parallel()
	f := newBootTest(t)
	ctx := context.Background()
	f.setAvailable(0.5, 4) // under the floor, swap full
	f.set(62, 8)           // every agent stalled at the shared limit
	f.step(t)
	f.srv.heavy.mu.Lock()
	f.srv.heavy.g.Ask(f.newer.Ref(), "k", "cargo build", time.Hour, time.Now())
	f.srv.heavy.mu.Unlock()
	// The host busy, under its reserve but over its floor: nothing held.
	if err := f.client.PutHostLoad(ctx, api.HostLoad{OS: "linux", Memory: 30, Busy: true, Why: "memory", MemoryTotal: 30 << 30, Available: 8 << 30, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if f.srv.bootWaits() {
		t.Fatal("a boot would wait")
	}
	waiter := waiterAgent(t, f)
	done := make(chan error, 1)
	go func() { done <- f.srv.admitBoot(ctx, waiter) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the boot waits: %+v", f.srv.memoryHold(waiter.Ref()))
	}
	if a := mustAgent(t, f, waiter); a.Hold != "" {
		t.Errorf("On hold (%q) though it booted", a.Hold)
	}
}

// Machines asked for at once boot a BootStep apart: the next waits On hold
// for its turn, says so with no place in line, and boots by itself.
func TestBootsAreStaggered(t *testing.T) {
	t.Parallel()
	f := newBootTest(t)
	ctx := context.Background()
	f.setAvailable(12, 0)
	f.step(t)
	f.srv.admit.mu.Lock()
	f.srv.admit.a.BootStep = time.Hour
	f.srv.admit.mu.Unlock()
	if err := f.srv.admitBoot(ctx, f.newer); err != nil {
		t.Fatal(err)
	}
	waiter := waiterAgent(t, f)
	done := make(chan error, 1)
	go func() { done <- f.srv.admitBoot(ctx, waiter) }()
	waitFor(t, "the next boot to wait its turn On hold", func() bool {
		held := f.srv.memoryHold(waiter.Ref())
		return held != nil && held.Machine == "waiting" && held.Why == api.HoldWhyTurn && held.Ahead == 0 && mustAgent(t, f, waiter).Hold == state.HoldMemory
	})
	if !f.srv.bootWaits() {
		t.Error("a start request now wouldn't wait its turn")
	}
	f.srv.admit.mu.Lock()
	f.srv.admit.a.BootStep = 0
	f.srv.admit.mu.Unlock()
	f.step(t)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the next boot still waits once its turn came")
	}
	if a := mustAgent(t, f, waiter); a.Hold != "" {
		t.Errorf("still On hold (%q) once it booted", a.Hold)
	}
	if held := f.srv.memoryHold(waiter.Ref()); held != nil {
		t.Errorf("still held: %+v", held)
	}
}

// While the user's computer is below its hard floor (the supervisor's
// HoldBoots), no machine boots, the first included, and the hold says it is
// the computer; once it is over it, the machine boots at the next step.
func TestBootWaitsOnlyWhileTheHostIsBelowItsFloor(t *testing.T) {
	t.Parallel()
	f := newBootTest(t)
	ctx := context.Background()
	f.setAvailable(12, 0)
	f.belowFloor(t, true)
	f.step(t)
	waiter := waiterAgent(t, f)
	done := make(chan error, 1)
	go func() { done <- f.srv.admitBoot(ctx, waiter) }()
	waitFor(t, "the boot to wait for the host", func() bool {
		held := f.srv.memoryHold(waiter.Ref())
		return held != nil && held.Machine == "waiting" && held.Why == api.HoldWhyHost && mustAgent(t, f, waiter).Hold == state.HoldMemory
	})
	f.belowFloor(t, false)
	f.step(t)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the boot still waits with the host over its floor")
	}
	f.srv.admit.mu.Lock()
	why := f.srv.admit.a.Why()
	f.srv.admit.mu.Unlock()
	if why == pressure.WaitHost {
		t.Error("admission still says the host holds boots")
	}
}

func mustAgent(t *testing.T, f *bootTest, a state.Agent) state.Agent {
	t.Helper()
	got, err := f.srv.store.Agent(context.Background(), a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A start request whose boot must wait answers at once, the agent On hold,
// rather than waiting in the request for as long as the host takes: the app
// gives up on a request after minutes.
func TestStartRequestDoesNotWaitForTheHost(t *testing.T) {
	t.Parallel()
	f := newBootTest(t)
	ctx := context.Background()
	f.setAvailable(12, 0)
	f.belowFloor(t, true)
	f.step(t)
	waiter := waiterAgent(t, f)
	// Its machine stopped, beside agent-01's running one.
	script := `#!/bin/sh
case "$1" in
  list) echo '[{"name": "ab-hello-stack-agent-01", "status": "Running"}, {"name": "ab-hello-stack-agent-03", "status": "Stopped"}]' ;;
  query) echo '{}' ;;
esac`
	testutil.WriteExecutable(t, f.srv.cfg.Incus.Bin, []byte(script)) // the daemon forks meanwhile: not os.WriteFile
	if !f.srv.bootWaits() {
		t.Fatal("with the host below its floor a boot should wait")
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
	f := newBootTest(t)
	ctx := context.Background()
	f.setAvailable(12, 0)
	f.belowFloor(t, true)
	f.step(t)
	waiter := waiterAgent(t, f)
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

// bootTest is pressureTest with a second agent, newer than the first, and
// the daemon's log kept.
type bootTest struct {
	*pressureTest
	newer state.Agent
	log   *syncBuffer
}

func newBootTest(t *testing.T) *bootTest {
	t.Helper()
	log := &syncBuffer{}
	p := newPressureTest(t, func(s *Server) { s.cfg.Log = log })
	newer := state.Agent{
		Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02", AI: "none",
		Branch: "agentbox/agent-02", Worktree: "/worktrees/agent-02", Status: state.AgentReady, CreatedAt: time.Now().Add(time.Minute),
	}
	if err := p.srv.store.AddAgent(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	return &bootTest{pressureTest: p, newer: newer, log: log}
}

// step takes a pressure step.
func (f *bootTest) step(t *testing.T) {
	t.Helper()
	f.srv.stepPressure(context.Background())
}
