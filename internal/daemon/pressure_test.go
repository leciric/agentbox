package daemon

import (
	"context"
	"encoding/json"
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
// frozen and removed, and whether each still has processes.
type fakeRunCgroups struct {
	mu       sync.Mutex
	placed   map[string]int  // instance/key: the pid placed
	frozen   map[string]bool // instance/key
	removed  []string
	empty    map[string]bool // instance/key: its processes have all gone
	placeErr error
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
// start asks for a heavy command of the agent's as its shim does, POST
// /v1/self/heavy, and reports whether the daemon said it started.
func (p *pressureTest) start(t *testing.T, key string) bool {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/self/heavy", strings.NewReader(`{"key":"`+key+`","command":"go test ./`+key+`","waitSeconds":600}`))
	w := httptest.NewRecorder()
	if err := p.srv.startHeavy(p.instance)(w, r); err != nil {
		t.Fatal(err)
	}
	var out api.HeavyStart
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Started
}

// A heavy command starts the moment it asks, whatever the VM's memory says
// (19 Nov 2026, the user's decision): under pressure, nearly out of memory,
// the agents throttled at their shared limit. What the reading says is how
// short memory is, which builds run fewer jobs for, and nothing holds it.
func TestHeavyStartsAtOnceWhateverTheMemory(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	ctx := context.Background()
	for i, reading := range []func(){
		func() { p.set(1, 0) },
		func() { p.set(62, 8) },                      // 19 Nov 2026: agents stalled at their shared limit
		func() { p.set(5, 5); p.setAvailable(1, 4) }, // nearly out, swap full
	} {
		reading()
		p.srv.stepPressure(ctx)
		key := "k" + strconv.Itoa(i)
		if !p.start(t, key) {
			t.Fatalf("%s waits", key)
		}
	}
	if why := p.srv.heavy.g.Why(); why != pressure.Low {
		t.Errorf("memory short for %q, want %q", why, pressure.Low)
	}
	if h := p.srv.memoryHold(p.ref); h != nil {
		t.Errorf("the agent's hold = %+v, with nothing waiting or paused", h)
	}
	if ps, err := p.client.Pressure(ctx); err != nil || len(ps.Runs) != 3 || ps.Runs[0].State != "running" {
		t.Fatalf("GET /v1/pressure = %+v, %v", ps, err)
	}
}

// Nothing pauses a run for memory, however long pressure stays high or
// however little is left; a run that ends has its cgroup removed, and one
// whose processes all went ends at the next step.
func TestPressureNeverPausesARun(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	ctx := context.Background()
	for _, key := range []string{"old", "new"} {
		if !p.start(t, key) {
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
	p.setAvailable(0.5, 4)
	for range 5 {
		p.srv.stepPressure(ctx)
	}
	if p.cg.isFrozen(p.instance, "new") || p.cg.isFrozen(p.instance, "old") {
		t.Fatalf("frozen = %v, want none", p.cg.frozen)
	}
	if h := p.srv.memoryHold(p.ref); h != nil {
		t.Fatalf("the agent's hold = %+v", h)
	}
	// The old run ends: its cgroup goes.
	p.srv.endRuns(func(g *pressure.Runs) []*pressure.Run { return []*pressure.Run{g.End(p.ref, "old")} })
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

// Dropping an agent's runs, as it stops or goes, ends every one of them.
func TestHeavyDropEndsAnAgentsRuns(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	for _, key := range []string{"a", "b"} {
		if !p.start(t, key) {
			t.Fatal("didn't start")
		}
	}
	p.srv.dropHeavy(p.ref)
	if runs := p.srv.heavy.g.Running(); len(runs) != 0 {
		t.Errorf("still running: %d", len(runs))
	}
	if p.srv.heavyHeld(p.ref) {
		t.Error("an agent whose runs were dropped still works")
	}
}

// A run's shell joins it once it has started, and a join that fails leaves
// it unplaced, which even pausing whole agents never pauses.
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
	if !p.start(t, "k") {
		t.Fatal("didn't start")
	}
	if err := join("k", 7); err != nil {
		t.Fatal(err)
	}
	if p.cg.placed[runID(p.instance, "k")] != 7 || !p.srv.heavy.g.Get(p.ref, "k").Placed {
		t.Fatalf("placed %v", p.cg.placed)
	}
	p.cg.placeErr = errors.New("no process 8 in the machine")
	if !p.start(t, "other") {
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
	for _, want := range []string{`"process":"go"`, `"pid":4242`, `"limit":15032385536`, `"own_limit":true`, `"cause":"own"`} {
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
	if events := oomEvents(); !strings.Contains(string(events[0].Payload), `"own_limit":false`) || !strings.Contains(string(events[0].Payload), `"cause":"vm"`) {
		t.Errorf("the VM's kill reads %s", events[0].Payload)
	}
	// A cgroup nested in the machine: it reached its own smaller limit.
	mu.Lock()
	counts.Kills = 6
	counts.Nested = map[string]agent.NestedLimit{"docker/abc": {Limit: 512 << 20, Reached: 1}}
	mu.Unlock()
	p.srv.watchMemory(ctx) // the first look at it: a kill before this isn't news
	mu.Lock()
	counts.Kills = 7
	counts.Nested = map[string]agent.NestedLimit{"docker/abc": {Limit: 512 << 20, Reached: 2}}
	mu.Unlock()
	p.srv.watchMemory(ctx)
	waitFor(t, "the nested kill to be told", func() bool { return len(oomEvents()) > 2 })
	events = oomEvents()
	for _, want := range []string{`"cause":"nested"`, `"cgroup":"docker/abc"`, `"cgroup_limit":536870912`, `"own_limit":false`} {
		if !strings.Contains(string(events[0].Payload), want) {
			t.Errorf("the nested kill reads %s, lacks %s", events[0].Payload, want)
		}
	}
}

// TestThrashingAgentLosesItsLargestProcess: an agent that has spent its
// memory and swap, stalled on it, for thrashFor loses the process holding
// the most memory, and is told; one spent but hardly stalled (a build that
// fits, just), or stalled but not spent (the VM short), keeps everything.
func TestThrashingAgentLosesItsLargestProcess(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	ctx := context.Background()
	var mu sync.Mutex
	sample := agent.ThrashSample{At: time.Unix(1000, 0)}
	var killed []string
	p.srv.vmMemory = func() int64 { return 16 << 30 }
	p.srv.thrash = func(string) (agent.ThrashSample, bool) {
		mu.Lock()
		defer mu.Unlock()
		return sample, true
	}
	p.srv.killLargest = func(instance string) (agent.OOMVictim, error) {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, instance)
		return agent.OOMVictim{Name: "node", PID: 777, RSS: 7 << 30}, nil
	}
	// Two seconds between looks, full percent of them stalled.
	step := func(full float64, spent bool) {
		mu.Lock()
		sample.At = sample.At.Add(2 * time.Second)
		sample.Stalled += time.Duration(full / 100 * float64(2*time.Second))
		sample.Spent = spent
		mu.Unlock()
		p.srv.watchMemory(ctx)
	}
	kills := func() int { mu.Lock(); defer mu.Unlock(); return len(killed) }

	for range 5 {
		step(90, false) // stalled, not spent: the VM is short
	}
	for range 5 {
		step(10, true) // spent, hardly stalled
	}
	if kills() != 0 {
		t.Fatalf("killed %v without thrashing", killed)
	}
	step(100, false)
	step(30, true) // spent from here, stalled from 20% to 100%
	step(100, true)
	step(20, true)
	if kills() != 0 {
		t.Fatalf("killed %v before thrashFor", killed)
	}
	step(25, true)
	if kills() != 1 || killed[0] != p.instance {
		t.Fatalf("killed %v after thrashFor, want %s once", killed, p.instance)
	}
	step(80, true) // the count starts again
	if kills() != 1 {
		t.Errorf("killed %v again at once", killed)
	}
	events := func() []memory.Event {
		events, err := p.srv.memory().Events(ctx, "hello-stack", memory.EventFilter{Types: []string{"oom_kill"}})
		if err != nil {
			t.Fatal(err)
		}
		return events
	}
	waitFor(t, "the kill to be told", func() bool { return len(events()) > 0 })
	for _, want := range []string{`"process":"node"`, `"pid":777`, `"thrashing":true`, `"own_limit":true`} {
		if !strings.Contains(string(events()[0].Payload), want) {
			t.Errorf("the event %s lacks %s", events()[0].Payload, want)
		}
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

// A busy host holds no heavy command back: builds run fewer jobs (Why
// "host"), and the VM's caches are dropped when asked, at most once a while.
func TestHostLoadHoldsNoHeavyCommand(t *testing.T) {
	t.Parallel()
	var drops atomic.Int32
	p := newPressureTest(t, func(s *Server) {
		old := dropVMCaches
		dropVMCaches = func() error { drops.Add(1); return nil }
		t.Cleanup(func() { dropVMCaches = old })
	})
	ctx := context.Background()
	busy := api.HostLoad{OS: "linux", Memory: 30, Busy: true, Why: "memory", Reclaim: true, At: time.Now()}
	for range 2 {
		if err := p.client.PutHostLoad(ctx, busy); err != nil {
			t.Fatal(err)
		}
	}
	p.srv.stepPressure(ctx)
	if !p.start(t, "busy") {
		t.Fatal("a command waited for a busy host")
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
