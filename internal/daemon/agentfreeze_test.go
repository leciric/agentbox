package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// fakeMachineCgroups is the agents' machines' cgroups as a test sees them:
// every one it is told of runs, and freezes and thaws as it is told.
type fakeMachineCgroups struct {
	mu        sync.Mutex
	frozen    map[string]bool
	reclaimed []string
}

func (f *fakeMachineCgroups) Running(instance string) (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return true, f.frozen[instance]
}
func (f *fakeMachineCgroups) Own(string) error { return nil }
func (f *fakeMachineCgroups) Freeze(instance string, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.frozen == nil {
		f.frozen = map[string]bool{}
	}
	f.frozen[instance] = on
	return nil
}
func (f *fakeMachineCgroups) Reclaim(instance string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reclaimed = append(f.reclaimed, instance)
	return nil
}
func (f *fakeMachineCgroups) Memory(string) int64 { return 1 << 30 }
func (f *fakeMachineCgroups) isFrozen(instance string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frozen[instance]
}

// fleetTest is pressureTest with a second agent, newer than the first, and
// whole machines' cgroups faked.
type fleetTest struct {
	*pressureTest
	mc    *fakeMachineCgroups
	newer state.Agent
}

func newFleetTest(t *testing.T, on bool) *fleetTest {
	t.Helper()
	mc := &fakeMachineCgroups{}
	p := newPressureTest(t)
	p.srv.machineCgroups = mc
	pol := p.srv.heavy.g.Policy
	p.srv.frozen.f.Policy = pol
	ctx := context.Background()
	newer := state.Agent{
		Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02", AI: "none",
		Branch: "agentbox/agent-02", Worktree: "/worktrees/agent-02", Status: state.AgentReady, CreatedAt: time.Now().Add(time.Minute),
	}
	if err := p.srv.store.AddAgent(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if on {
		if _, err := p.client.UpdateSettings(ctx, api.UpdateSettingsRequest{AgentFreeze: new(true)}); err != nil {
			t.Fatal(err)
		}
	}
	return &fleetTest{pressureTest: p, mc: mc, newer: newer}
}

// step syncs the fleet with the agents, as the memory watch does, and takes
// a pressure step.
func (f *fleetTest) step(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	agents, err := f.srv.store.Agents(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	f.srv.syncFleet(ctx, agents)
	f.srv.stepPressure(ctx)
}

func TestFleetSettingOffByDefault(t *testing.T) {
	t.Parallel()
	f := newFleetTest(t, false)
	ctx := context.Background()
	got, err := f.client.Settings(ctx)
	if err != nil || got.AgentFreeze {
		t.Fatalf("settings %v, %v: want agentFreeze off", got.AgentFreeze, err)
	}
	got, err = f.client.UpdateSettings(ctx, api.UpdateSettingsRequest{AgentFreeze: new(true)})
	if err != nil || !got.AgentFreeze || !f.srv.fleetOn() {
		t.Fatalf("turned on: %v, %v", got.AgentFreeze, err)
	}
}

func TestFleetOffFreezesNothing(t *testing.T) {
	t.Parallel()
	f := newFleetTest(t, false)
	f.setAvailable(0.5, 4) // out of memory
	f.set(90, 60)
	for range 3 {
		f.step(t)
	}
	if f.mc.isFrozen(f.newer.Instance) || f.mc.isFrozen(f.instance) {
		t.Fatal("a machine was frozen with the setting off")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.srv.admitMachine(ctx, f.newer); err != nil {
		t.Fatalf("a boot waited with the setting off: %v", err)
	}
}

func TestFleetFreezesNewerThawsOnCalm(t *testing.T) {
	t.Parallel()
	f := newFleetTest(t, true)
	ctx := context.Background()
	// The older agent works: a heavy command of its runs.
	if !f.start(t, "build", time.Second) {
		t.Fatal("the build didn't start")
	}
	f.setAvailable(0.5, 4)
	f.step(t)
	if !f.mc.isFrozen(f.newer.Instance) {
		t.Fatal("the newer machine wasn't frozen with the VM out of memory")
	}
	if f.mc.isFrozen(f.instance) {
		t.Fatal("the oldest working machine was frozen")
	}
	waitFor(t, "the frozen machine's memory to be pushed out", func() bool {
		f.mc.mu.Lock()
		defer f.mc.mu.Unlock()
		return len(f.mc.reclaimed) == 1
	})
	held := f.srv.memoryHold(f.newer.Ref())
	if held == nil || held.Machine != "paused" || held.Ahead != 0 || held.Why != string(pressure.Low) {
		t.Fatalf("the frozen agent's hold = %+v", held)
	}
	// A message for it waits in its chat rather than resuming it.
	if !f.srv.frozenForMemory(f.newer.Ref()) {
		t.Fatal("not frozen for memory")
	}
	waitFor(t, "the frozen agents to be remembered", func() bool {
		v, _ := f.srv.store.Setting(ctx, state.SettingAgentFreezeFrozen)
		return v == `["hello-stack/agent-02"]`
	})

	f.setAvailable(12, 1)
	f.set(0, 0)
	f.step(t)
	if f.mc.isFrozen(f.newer.Instance) {
		t.Fatal("the machine didn't thaw once calm")
	}
	if held := f.srv.memoryHold(f.newer.Ref()); held != nil {
		t.Fatalf("thawed, the hold = %+v", held)
	}
}

func TestFleetStartsHeavyAtOnce(t *testing.T) {
	t.Parallel()
	f := newFleetTest(t, true)
	if !f.start(t, "first", time.Second) {
		t.Fatal("first didn't start")
	}
	f.set(50, 0)
	if !f.start(t, "second", 100*time.Millisecond) {
		t.Fatal("a heavy command waited with whole agents paused instead")
	}
}

func TestFleetBootWaitsForMemory(t *testing.T) {
	t.Parallel()
	f := newFleetTest(t, true)
	if !f.start(t, "build", time.Second) { // the older agent works
		t.Fatal("the build didn't start")
	}
	f.setAvailable(2, 4) // 2 GiB: a 1.25 GiB boot over a 1.6 GiB floor doesn't fit
	f.step(t)
	waiter := state.Agent{Project: "hello-stack", Name: "agent-03", Instance: "ab-hello-stack-agent-03"}
	done := make(chan error, 1)
	go func() { done <- f.srv.admitMachine(context.Background(), waiter) }()
	waitFor(t, "the boot to wait", func() bool {
		held := f.srv.machineHold(waiter.Ref())
		return held != nil && held.Machine == "waiting" && held.Ahead == 0
	})
	select {
	case err := <-done:
		t.Fatalf("booted with 2 GiB available: %v", err)
	case <-time.After(100 * time.Millisecond):
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
}

func TestFleetOffThaws(t *testing.T) {
	t.Parallel()
	f := newFleetTest(t, true)
	if !f.start(t, "build", time.Second) {
		t.Fatal("the build didn't start")
	}
	f.setAvailable(0.5, 4)
	f.step(t)
	if !f.mc.isFrozen(f.newer.Instance) {
		t.Fatal("not frozen")
	}
	if _, err := f.client.UpdateSettings(context.Background(), api.UpdateSettingsRequest{AgentFreeze: new(false)}); err != nil {
		t.Fatal(err)
	}
	if f.mc.isFrozen(f.newer.Instance) {
		t.Fatal("still frozen with the setting off")
	}
}
