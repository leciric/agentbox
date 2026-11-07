package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// fakeRunCgroups is the runs' cgroups, as a test sees them: what was placed,
// frozen, reclaimed and removed, and whether each still has processes.
type fakeRunCgroups struct {
	mu        sync.Mutex
	placed    map[string]int  // instance/key: the pid placed
	frozen    map[string]bool // instance/key
	reclaimed []string
	removed   []string
	empty     map[string]bool // instance/key: its processes have all gone
	placeErr  error
}

func runID(instance, key string) string { return instance + "/" + key }

func (f *fakeRunCgroups) Place(instance, key string, pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.placeErr != nil {
		return f.placeErr
	}
	if f.placed == nil {
		f.placed = map[string]int{}
	}
	f.placed[runID(instance, key)] = pid
	return nil
}

func (f *fakeRunCgroups) Freeze(instance, key string, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.frozen == nil {
		f.frozen = map[string]bool{}
	}
	f.frozen[runID(instance, key)] = on
	return nil
}

func (f *fakeRunCgroups) Reclaim(instance, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reclaimed = append(f.reclaimed, runID(instance, key))
	return nil
}

func (f *fakeRunCgroups) Populated(instance, key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, placed := f.placed[runID(instance, key)]
	return placed && !f.empty[runID(instance, key)]
}

func (f *fakeRunCgroups) Remove(instance, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.frozen != nil {
		f.frozen[runID(instance, key)] = false
	}
	f.removed = append(f.removed, runID(instance, key))
	return nil
}

func (f *fakeRunCgroups) isFrozen(instance, key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frozen[runID(instance, key)]
}

// pressureTest is a daemon with one agent whose memory pressure the test sets,
// and a governor that decides at once rather than waiting out its averages.
type pressureTest struct {
	testDaemon
	cg       *fakeRunCgroups
	mu       sync.Mutex
	sample   pressure.Sample
	instance string
	ref      string
}

func newPressureTest(t *testing.T, also ...func(*Server)) *pressureTest {
	t.Helper()
	p := &pressureTest{cg: &fakeRunCgroups{}}
	p.testDaemon = startTestDaemon(t, t.TempDir(), oneAgentIncus, testConfig{setup: func(s *Server) {
		for _, f := range also {
			f(s)
		}
		s.runCgroups = p.cg
		s.readPressure = func() (pressure.Sample, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.sample, nil
		}
		// The default policy, with nothing to wait for between steps.
		pol := pressure.DefaultPolicy
		pol.FreezeAfter, pol.CalmAfter, pol.Step, pol.AdmitStep, pol.HeadroomStep = 0, 0, 0, 0, 0
		pol.Grace = time.Minute
		s.heavy.g.Policy = pol
	}})
	a := addTestAgent(t, p.testDaemon)
	p.instance, p.ref = a.Instance, a.Ref()
	return p
}

func (p *pressureTest) set(some, full float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sample.PSI = pressure.PSI{Some: pressure.Stall{Avg10: some}, Full: pressure.Stall{Avg10: full}}
}

// setAvailable sets the VM at its 16 GiB cap with avail GiB available and
// its 4 GiB of swap swapped GiB full.
func (p *pressureTest) setAvailable(avail, swapped float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	const gib = 1 << 30
	p.sample.Mem = pressure.Mem{Total: 16 * gib, Cap: 16 * gib, Available: int64(avail * gib), SwapTotal: 4 * gib, SwapFree: int64((4 - swapped) * gib)}
}

// start asks for key to start, waiting at most wait.
func (p *pressureTest) start(t *testing.T, key string, wait time.Duration) bool {
	t.Helper()
	out, err := p.srv.askHeavy(context.Background(), p.ref, p.instance, key, "go test ./"+key, time.Hour, wait)
	if err != nil {
		t.Fatal(err)
	}
	return out.Started
}

func TestHeavyStartsOrWaits(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	if !p.start(t, "first", time.Second) {
		t.Fatal("a command didn't start in a calm VM")
	}
	p.set(50, 0)
	out, err := p.srv.askHeavy(context.Background(), p.ref, p.instance, "second", "go test ./...", time.Hour, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if out.Started || !strings.Contains(out.Why, "under pressure (50%") {
		t.Fatalf("under pressure, with one going: %+v", out)
	}
	// Its place is kept for the asker to ask again, and shows on the agent.
	if h := p.srv.memoryHold(p.ref); h == nil || h.Waiting != 1 || h.Paused != 0 {
		t.Fatalf("the agent's hold = %+v, want one waiting", h)
	}
	p.set(1, 0)
	if !p.start(t, "second", time.Second) {
		t.Fatal("the waiting command didn't start once pressure eased")
	}
	if h := p.srv.memoryHold(p.ref); h != nil {
		t.Errorf("with both going, the hold = %+v", h)
	}
}

func TestPressurePausesNewest(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	ctx := context.Background()
	for _, key := range []string{"old", "new"} {
		if !p.start(t, key, time.Second) {
			t.Fatalf("%s didn't start", key)
		}
		if err := p.srv.runCgroups.Place(p.instance, key, 100); err != nil {
			t.Fatal(err)
		}
		p.srv.heavy.mu.Lock()
		p.srv.heavy.g.Place(p.ref, key)
		p.srv.heavy.mu.Unlock()
	}
	p.set(90, 60)
	p.srv.stepPressure(ctx)
	p.srv.stepPressure(ctx)
	if !p.cg.isFrozen(p.instance, "new") || p.cg.isFrozen(p.instance, "old") {
		t.Fatalf("frozen = %v, want only the newer run", p.cg.frozen)
	}
	if h := p.srv.memoryHold(p.ref); h == nil || h.Paused != 1 {
		t.Fatalf("the agent's hold = %+v, want one paused", h)
	}
	waitFor(t, "the paused run's memory to be pushed out", func() bool {
		p.cg.mu.Lock()
		defer p.cg.mu.Unlock()
		return len(p.cg.reclaimed) == 1
	})
	waitFor(t, "the agent to be told", func() bool {
		events, err := p.srv.memory().Events(ctx, "hello-stack", memory.EventFilter{Types: []string{"memory_paused"}})
		return err == nil && len(events) == 1
	})

	p.set(1, 0)
	p.srv.stepPressure(ctx)
	if p.cg.isFrozen(p.instance, "new") {
		t.Fatal("the paused run didn't resume once calm")
	}
	// The old run ends: its cgroup goes.
	p.srv.endRuns(ctx, func(g *pressure.Governor) []*pressure.Run { return []*pressure.Run{g.End(p.ref, "old")} })
	if len(p.cg.removed) != 1 || p.cg.removed[0] != runID(p.instance, "old") {
		t.Errorf("removed %v", p.cg.removed)
	}
	// The new one's processes all go: the next step ends it.
	p.cg.mu.Lock()
	p.cg.empty = map[string]bool{runID(p.instance, "new"): true}
	p.cg.mu.Unlock()
	p.srv.stepPressure(ctx)
	if runs := p.srv.heavy.g.Running(); len(runs) != 0 {
		t.Errorf("still running: %d", len(runs))
	}
}

func TestRunningOutPausesBeforePressureShows(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	ctx := context.Background()
	for _, key := range []string{"old", "mid", "new"} {
		if !p.start(t, key, time.Second) {
			t.Fatalf("%s didn't start", key)
		}
		if err := p.srv.runCgroups.Place(p.instance, key, 100); err != nil {
			t.Fatal(err)
		}
		p.srv.heavy.mu.Lock()
		p.srv.heavy.g.Place(p.ref, key)
		p.srv.heavy.mu.Unlock()
	}
	// No pressure to speak of yet, but 1 GiB left and swap full.
	p.set(5, 5)
	p.setAvailable(1, 4)
	p.srv.stepPressure(ctx)
	if !p.cg.isFrozen(p.instance, "new") || !p.cg.isFrozen(p.instance, "mid") || p.cg.isFrozen(p.instance, "old") {
		t.Fatalf("frozen = %v, want all but the oldest", p.cg.frozen)
	}
	out, err := p.srv.askHeavy(ctx, p.ref, p.instance, "later", "go test ./...", time.Hour, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if out.Started || !strings.Contains(out.Why, "nearly out of memory (1.0 GiB of 16.0 GiB available, swap full") {
		t.Fatalf("out of memory, a new command: %+v", out)
	}
}

func TestHeavyDropWakes(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	if !p.start(t, "going", time.Second) {
		t.Fatal("didn't start")
	}
	p.set(80, 0)
	done := make(chan bool)
	go func() {
		out, _ := p.srv.askHeavy(context.Background(), p.ref, p.instance, "waiting", "", time.Hour, time.Minute)
		done <- out.Started
	}()
	waitFor(t, "the second command to wait", func() bool { return p.srv.memoryHold(p.ref) != nil })
	p.srv.dropHeavy(context.Background(), p.ref)
	select {
	case started := <-done:
		if started {
			t.Error("a dropped waiter was told to start")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dropping the agent left its waiter waiting")
	}
}

// A run's shell joins it once it has started, and a join that fails leaves
// it unplaced, which is never paused.
func TestHeavyJoin(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	join := func(key string, pid int) error {
		r := httptest.NewRequest(http.MethodPost, "/v1/self/heavy/"+key+"/join", strings.NewReader(`{"pid":`+strconv.Itoa(pid)+`}`))
		r.SetPathValue("key", key)
		return p.srv.joinHeavy(p.instance)(httptest.NewRecorder(), r)
	}
	if err := join("never-asked", 7); err == nil {
		t.Error("a run that never started was joined")
	}
	if !p.start(t, "k", time.Second) {
		t.Fatal("didn't start")
	}
	if err := join("k", 7); err != nil {
		t.Fatal(err)
	}
	if p.cg.placed[runID(p.instance, "k")] != 7 || !p.srv.heavy.g.Get(p.ref, "k").Placed {
		t.Fatalf("placed %v", p.cg.placed)
	}
	p.cg.placeErr = errors.New("no process 8 in the machine")
	if !p.start(t, "other", time.Second) {
		t.Fatal("didn't start")
	}
	if err := join("other", 8); err == nil || p.srv.heavy.g.Get(p.ref, "other").Placed {
		t.Error("a run whose join failed counts as placed")
	}
}

// A process killed for an agent's memory.max is news for that agent, once,
// with what was killed and at what size; what was killed before the daemon
// looked isn't.
func TestOOMKillTold(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	ctx := context.Background()
	var mu sync.Mutex
	counts := agent.MemoryCounts{Kills: 3, OwnLimit: 1, Limit: 14 << 30}
	var limits []int64
	p.srv.vmMemory = func() int64 { return 16 << 30 }
	p.srv.setMemoryLimit = func(instance string, limit int64) error {
		mu.Lock()
		defer mu.Unlock()
		limits = append(limits, limit)
		return nil
	}
	p.srv.memoryEvents = func(string) (agent.MemoryCounts, bool) {
		mu.Lock()
		defer mu.Unlock()
		return counts, true
	}
	p.srv.oomVictim = func(string) (agent.OOMVictim, bool) {
		return agent.OOMVictim{Name: "go", PID: 4242, RSS: 9 << 30}, true
	}
	oomEvents := func() []memory.Event {
		events, err := p.srv.memory().Events(ctx, "hello-stack", memory.EventFilter{Types: []string{"oom_kill"}})
		if err != nil {
			t.Fatal(err)
		}
		return events
	}
	p.srv.watchMemory(ctx)
	time.Sleep(50 * time.Millisecond)
	if len(oomEvents()) != 0 {
		t.Fatal("kills from before the daemon looked were reported")
	}
	if len(limits) != 1 || limits[0] != agent.MemoryLimit(16<<30, 0) {
		t.Fatalf("memory.max set to %v, want %d", limits, agent.MemoryLimit(16<<30, 0))
	}
	// Its own limit: it reached it since.
	mu.Lock()
	counts.Kills, counts.OwnLimit = 4, 2
	mu.Unlock()
	p.srv.watchMemory(ctx)
	p.srv.watchMemory(ctx)
	waitFor(t, "the kill to be told", func() bool { return len(oomEvents()) > 0 })
	time.Sleep(50 * time.Millisecond)
	events := oomEvents()
	if len(events) != 1 {
		t.Fatalf("%d oom_kill events, want 1", len(events))
	}
	for _, want := range []string{`"process":"go"`, `"pid":4242`, `"limit":15032385536`, `"own_limit":true`} {
		if !strings.Contains(string(events[0].Payload), want) {
			t.Errorf("the event %s lacks %s", events[0].Payload, want)
		}
	}
	// The VM's: killed, without its own limit reached.
	mu.Lock()
	counts.Kills = 5
	mu.Unlock()
	p.srv.watchMemory(ctx)
	waitFor(t, "the second kill to be told", func() bool { return len(oomEvents()) > 1 })
	if events := oomEvents(); !strings.Contains(string(events[0].Payload), `"own_limit":false`) {
		t.Errorf("the VM's kill reads %s", events[0].Payload)
	}
}

// An agent's memory.max is its share of the VM's cap, half unless the user
// chose, and a new share reaches running agents; each also gets the io.weight
// that has its disk use yield to the daemon's and Incus's.
func TestAgentMemoryShare(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	limits := map[string]int64{}
	weights := map[string]int{}
	p := newPressureTest(t, func(s *Server) {
		s.vmMemory = func() int64 { return 18 << 30 }
		s.setMemoryLimit = func(instance string, limit int64) error {
			mu.Lock()
			defer mu.Unlock()
			limits[instance] = limit
			return nil
		}
		s.setIOWeight = func(instance string, weight int) error {
			mu.Lock()
			defer mu.Unlock()
			weights[instance] = weight
			return nil
		}
	})
	ctx := context.Background()
	limit := func() int64 {
		mu.Lock()
		defer mu.Unlock()
		return limits[p.instance]
	}

	p.srv.watchMemory(ctx)
	if got := limit(); got != 18<<30/100*50 {
		t.Errorf("memory.max = %d MiB, want half of 18 GiB", got>>20)
	}
	if weights[p.instance] != agent.AgentIOWeight {
		t.Errorf("io.weight = %d, want %d", weights[p.instance], agent.AgentIOWeight)
	}
	settings, err := p.client.Settings(ctx)
	if err != nil || settings.AgentMemoryShare != 50 || settings.AgentMemoryLimit != 18<<30/100*50 || settings.VMMemory != 18<<30 {
		t.Fatalf("Settings = share %d limit %d vm %d, %v", settings.AgentMemoryShare, settings.AgentMemoryLimit, settings.VMMemory, err)
	}

	for _, bad := range []int{5, 95, -1} {
		if _, err := p.client.UpdateSettings(ctx, api.UpdateSettingsRequest{AgentMemoryShare: &bad}); err == nil {
			t.Errorf("a share of %d%% was taken", bad)
		}
	}
	share := 30
	if settings, err := p.client.UpdateSettings(ctx, api.UpdateSettingsRequest{AgentMemoryShare: &share}); err != nil || settings.AgentMemoryShare != 30 {
		t.Fatalf("UpdateSettings(30%%) = %d, %v", settings.AgentMemoryShare, err)
	}
	p.srv.watchMemory(ctx)
	if got := limit(); got != 18<<30/100*30 {
		t.Errorf("after 30%%: memory.max = %d MiB", got>>20)
	}
	zero := 0
	if settings, err := p.client.UpdateSettings(ctx, api.UpdateSettingsRequest{AgentMemoryShare: &zero}); err != nil || settings.AgentMemoryShare != agent.DefaultAgentMemoryShare {
		t.Errorf("back to the default: %d, %v", settings.AgentMemoryShare, err)
	}
}

// A busy host holds a heavy command back, says so, and drops the VM's
// caches when asked, at most once a while; a report that goes stale holds
// nothing.
func TestHostLoadHoldsHeavyCommands(t *testing.T) {
	t.Parallel()
	var drops atomic.Int32
	p := newPressureTest(t, func(s *Server) {
		old := dropVMCaches
		dropVMCaches = func() error { drops.Add(1); return nil }
		t.Cleanup(func() { dropVMCaches = old })
	})
	ctx := context.Background()
	if !p.start(t, "first", time.Second) {
		t.Fatal("a command didn't start in a calm VM")
	}
	busy := api.HostLoad{OS: "linux", Memory: 30, Busy: true, Why: "memory", Reclaim: true, At: time.Now()}
	for range 2 {
		if err := p.client.PutHostLoad(ctx, busy); err != nil {
			t.Fatal(err)
		}
	}
	out, err := p.srv.askHeavy(ctx, p.ref, p.instance, "second", "go test ./...", time.Hour, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if out.Started || !strings.Contains(out.Why, "computer is busy") {
		t.Fatalf("host busy: %+v", out)
	}
	if ps, err := p.client.Pressure(ctx); err != nil || ps.Host == nil || !ps.Host.Busy || ps.Why != string(pressure.HostBusy) {
		t.Fatalf("GET /v1/pressure = %+v, %v", ps, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for drops.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := drops.Load(); n != 1 {
		t.Fatalf("caches dropped %d times for two reports, want once", n)
	}
	// Stale: as good as calm.
	p.srv.hostLoad.mu.Lock()
	p.srv.hostLoad.got = time.Now().Add(-api.HostLoadTTL - time.Second)
	p.srv.hostLoad.mu.Unlock()
	if !p.start(t, "second", time.Second) {
		t.Fatal("a stale busy report still held the command")
	}
}

// An agent being made already runs its machine, and gets its memory.max then,
// not only once it is ready: several made at once had none.
func TestAgentBeingMadeGetsItsMemoryLimit(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	ctx := context.Background()
	making := state.Agent{
		Project: "hello-stack", Name: "agent-05", Instance: "ab-hello-stack-agent-05", AI: "none",
		Branch: "agentbox/agent-05", Worktree: "/worktrees/agent-05", Status: state.AgentCreating, CreatedAt: time.Now(),
	}
	if err := p.srv.store.AddAgent(ctx, making); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	limits := map[string]int64{}
	p.srv.vmMemory = func() int64 { return 16 << 30 }
	p.srv.setMemoryLimit = func(instance string, limit int64) error {
		mu.Lock()
		defer mu.Unlock()
		limits[instance] = limit
		return nil
	}
	caps := map[string]agent.Caps{}
	p.srv.vmSwap = func() int64 { return 8 << 30 }
	p.srv.setCaps = func(instance string, c agent.Caps) error {
		mu.Lock()
		defer mu.Unlock()
		caps[instance] = c
		return nil
	}
	p.srv.watchMemory(ctx)
	mu.Lock()
	defer mu.Unlock()
	if got, want := limits[making.Instance], agent.MemoryLimit(16<<30, 0); got != want {
		t.Errorf("memory.max of the agent being made = %d, want %d (all: %v)", got, want, limits)
	}
	// Its swap, CPU weight and task count from the same moment: a fork bomb
	// or a build in its setup is as bounded as one in a Ready agent.
	want := agent.Caps{SwapMax: agent.SwapLimit(agent.MemoryLimit(16<<30, 0), 8<<30, 0), CPUWeight: agent.AgentCPUWeight, PidsMax: agent.AgentPidsMax}
	if got := caps[making.Instance]; got != want {
		t.Errorf("caps of the agent being made = %+v, want %+v", got, want)
	}
}

// TestAgentCapsFollowTheShareAndTheVMsSwap: an agent's memory.swap.max is a
// quarter of its memory.max, at most its share of the VM's swap, and follows
// both.
func TestAgentCapsFollowTheShareAndTheVMsSwap(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var swap int64 = 30 << 30
	caps := map[string]agent.Caps{}
	p := newPressureTest(t, func(s *Server) {
		s.vmMemory = func() int64 { return 18 << 30 }
		s.vmSwap = func() int64 { mu.Lock(); defer mu.Unlock(); return swap }
		s.setCaps = func(instance string, c agent.Caps) error {
			mu.Lock()
			defer mu.Unlock()
			caps[instance] = c
			return nil
		}
	})
	ctx := context.Background()
	got := func() agent.Caps { mu.Lock(); defer mu.Unlock(); return caps[p.instance] }

	p.srv.watchMemory(ctx)
	if c := got(); c.SwapMax != 18<<30/100*50/agent.SwapDivisor || c.CPUWeight != agent.AgentCPUWeight || c.PidsMax != agent.AgentPidsMax {
		t.Errorf("caps = %+v, want a quarter of half of 18 GiB, weight %d, %d tasks", c, agent.AgentCPUWeight, agent.AgentPidsMax)
	}
	share := 30
	if _, err := p.client.UpdateSettings(ctx, api.UpdateSettingsRequest{AgentMemoryShare: &share}); err != nil {
		t.Fatal(err)
	}
	p.srv.watchMemory(ctx)
	if c := got(); c.SwapMax != 18<<30/100*30/agent.SwapDivisor {
		t.Errorf("after a 30%% share: swap.max = %d MiB", c.SwapMax>>20)
	}
	// Little swap: no more than the agent's share of it.
	mu.Lock()
	swap = 2 << 30
	mu.Unlock()
	p.srv.watchMemory(ctx)
	if c := got(); c.SwapMax != 2<<30/100*30 {
		t.Errorf("with 2 GiB of swap: swap.max = %d MiB", c.SwapMax>>20)
	}
	// Swap that can't be read leaves the limit as it is.
	mu.Lock()
	swap = -1
	mu.Unlock()
	p.srv.watchMemory(ctx)
	if c := got(); c.SwapMax != 0 || c.PidsMax != agent.AgentPidsMax {
		t.Errorf("unreadable swap: caps = %+v, want swap untouched", c)
	}
}
