package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/memory"
	"agentbox/internal/pressure"
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
	psi      pressure.PSI
	instance string
	ref      string
}

func newPressureTest(t *testing.T) *pressureTest {
	t.Helper()
	p := &pressureTest{cg: &fakeRunCgroups{}}
	p.testDaemon = startTestDaemon(t, t.TempDir(), oneAgentIncus, testConfig{setup: func(s *Server) {
		s.runCgroups = p.cg
		s.readPressure = func() (pressure.PSI, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.psi, nil
		}
		s.heavy.g.Policy = pressure.Policy{Admit: 10, Freeze: 20, Calm: 5, Grace: time.Minute}
	}})
	a := addTestAgent(t, p.testDaemon)
	p.instance, p.ref = a.Instance, a.Ref()
	return p
}

func (p *pressureTest) set(some, full float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.psi = pressure.PSI{Some: pressure.Stall{Avg10: some}, Full: pressure.Stall{Avg10: full}}
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
	if len(limits) != 1 || limits[0] != agent.MemoryLimit(16<<30) {
		t.Fatalf("memory.max set to %v, want %d", limits, agent.MemoryLimit(16<<30))
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
