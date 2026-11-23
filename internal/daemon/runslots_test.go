package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/runslots"
	"agentbox/internal/state"
)

// fakeRunPhase is the agents' machines as the run slots see them: what each
// uses, its containers, and what ending a run phase stopped.
type fakeRunPhase struct {
	mu         sync.Mutex
	use        map[string]int64 // by instance
	containers []string         // running in every machine
	killed     []string         // instance/key
	keep       [][]string       // what StopContainers was told to keep
	stopped    []string         // ref: recording, android, browser
}

func (f *fakeRunPhase) MachineUse(instance string) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.use[instance]
	return n, ok
}

func (f *fakeRunPhase) setUse(instance string, n int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.use == nil {
		f.use = map[string]int64{}
	}
	f.use[instance] = n
}

func (f *fakeRunPhase) Containers(context.Context, state.Agent) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.containers), nil
}

func (f *fakeRunPhase) StopContainers(_ context.Context, _ state.Agent, keep []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keep = append(f.keep, keep)
	var stop []string
	for _, id := range f.containers {
		if !slices.Contains(keep, id) {
			stop = append(stop, id)
		}
	}
	return stop, nil
}

func (f *fakeRunPhase) KillRun(instance, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, instance+"/"+key)
	return nil
}

func (f *fakeRunPhase) note(a state.Agent, what string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, a.Ref()+": "+what)
	return nil
}

func (f *fakeRunPhase) StopRecording(_ context.Context, a state.Agent) error {
	return f.note(a, "recording")
}
func (f *fakeRunPhase) StopAndroid(_ context.Context, a state.Agent) error {
	return f.note(a, "android")
}
func (f *fakeRunPhase) StopBrowser(_ context.Context, a state.Agent) error {
	return f.note(a, "browser")
}

func (f *fakeRunPhase) snapshot() (killed, stopped []string, keep [][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.killed), slices.Clone(f.stopped), slices.Clone(f.keep)
}

const testGiB = int64(1) << 30

// runSlotsTest is a daemon with n agents in one project, in a VM of vm
// bytes, whose machines' use the test sets.
type runSlotsTest struct {
	testDaemon
	ops    *fakeRunPhase
	agents []state.Agent
	vm     int64
}

func newRunSlotsTest(t *testing.T, n int, vm int64) *runSlotsTest {
	t.Helper()
	return newGrowingRunSlotsTest(t, n, vm, vm)
}

// newGrowingRunSlotsTest is newRunSlotsTest for a VM with plugged bytes now
// that may grow to vmCap (0: its cap is unknown).
func newGrowingRunSlotsTest(t *testing.T, n int, plugged, vmCap int64) *runSlotsTest {
	t.Helper()
	rt := &runSlotsTest{ops: &fakeRunPhase{}, vm: plugged}
	rt.testDaemon = startTestDaemon(t, t.TempDir(), oneAgentIncus, testConfig{setup: func(s *Server) {
		s.runPhase = rt.ops
		s.vmMemory = func() int64 { return vmCap }
		s.vmMemoryNow = func() int64 { return plugged }
	}})
	first := addTestAgent(t, rt.testDaemon)
	rt.agents = []state.Agent{first}
	for i := 2; i <= n; i++ {
		a := first
		a.Name = "agent-0" + string(rune('0'+i))
		a.Instance = "ab-hello-stack-" + a.Name
		a.Branch = "agentbox/" + a.Name
		if err := rt.srv.store.AddAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
		rt.agents = append(rt.agents, a)
	}
	for _, a := range rt.agents {
		rt.ops.setUse(a.Instance, testGiB/2)
	}
	rt.step(t, time.Now())
	return rt
}

// step steps the run slots as the memory loop does, at now.
func (rt *runSlotsTest) step(t *testing.T, now time.Time) {
	t.Helper()
	agents, err := rt.srv.store.Agents(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	rt.srv.stepRunSlots(context.Background(), agents, now)
}

// call makes a request on agent i's own socket.
func (rt *runSlotsTest) call(t *testing.T, i int, method, path, body string) (int, []byte) {
	t.Helper()
	w := httptest.NewRecorder()
	rt.srv.inAgentRoutes(rt.agents[i].Instance).ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w.Code, w.Body.Bytes()
}

// up asks for agent i's run slot, waiting up to wait seconds.
func (rt *runSlotsTest) up(t *testing.T, i, wait int) api.RunPhase {
	t.Helper()
	code, body := rt.call(t, i, http.MethodPost, "/v1/self/run/up", `{"waitSeconds":`+strconv.Itoa(wait)+`}`)
	if code != http.StatusOK {
		t.Fatalf("run up for agent %d: %d %s", i, code, body)
	}
	var ph api.RunPhase
	if err := json.Unmarshal(body, &ph); err != nil {
		t.Fatal(err)
	}
	return ph
}

func (rt *runSlotsTest) down(t *testing.T, i int) api.RunPhase {
	t.Helper()
	code, body := rt.call(t, i, http.MethodDelete, "/v1/self/run", "")
	if code != http.StatusOK {
		t.Fatalf("run down for agent %d: %d %s", i, code, body)
	}
	var ph api.RunPhase
	if err := json.Unmarshal(body, &ph); err != nil {
		t.Fatal(err)
	}
	return ph
}

// The slots are the agents' share of the VM, less what agents coding use,
// in runs of the project's peak: a 16 GiB VM leaves the agents 13 GiB; two
// agents coding at half a GiB each leave 12, six runs of the default 2 GiB.
// A holder's use is its reservation's, not the code phase's.
func TestRunSlotsAreCountedFromTheVMLessTheCodePhase(t *testing.T) {
	t.Parallel()
	rt := newRunSlotsTest(t, 2, 16*testGiB)
	ctx := context.Background()
	slots, err := rt.client.RunSlots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slots.Pool != 13*testGiB || slots.CodeUse != testGiB || slots.Total != 6 || slots.Free != 6 || slots.InUse != 0 {
		t.Fatalf("GET /v1/run-slots = %+v, want 6 of 6 free from 13 GiB less 1 coding", slots)
	}
	if ph := rt.up(t, 0, 0); ph.State != api.RunSlotHolding || !ph.Granted || ph.Key == "" {
		t.Fatalf("run up = %+v, want it granted", ph)
	}
	rt.step(t, time.Now())
	slots, _ = rt.client.RunSlots(ctx)
	// 13 less agent-02's half GiB less agent-01's 2 GiB reserved: 10.5,
	// five more.
	if slots.CodeUse != testGiB/2 || slots.InUse != 1 || slots.Free != 5 || slots.Total != 6 {
		t.Fatalf("with one held: %+v, want 5 of 6 free", slots)
	}
	if len(slots.Holders) != 1 || slots.Holders[0].Agent != rt.agents[0].Ref() || slots.Holders[0].Reserved != runslots.DefaultPeak {
		t.Fatalf("holders = %+v", slots.Holders)
	}
	if len(slots.Projects) != 1 || slots.Projects[0].Project != "hello-stack" || slots.Projects[0].Learned || slots.Projects[0].InUse != 1 {
		t.Fatalf("projects = %+v", slots.Projects)
	}
	// Agents coding more leave fewer runs.
	rt.ops.setUse(rt.agents[1].Instance, 9*testGiB)
	rt.step(t, time.Now())
	if slots, _ = rt.client.RunSlots(ctx); slots.Free != 1 || slots.Total != 2 {
		t.Fatalf("with 9 GiB coding: %+v, want 1 of 2 free", slots)
	}
	// The agent's own status says it holds one.
	if rs := rt.srv.runSlotOf(rt.agents[0].Ref()); rs == nil || rs.State != api.RunSlotHolding || rs.Expires == nil {
		t.Fatalf("agent-01's run slot = %+v", rs)
	}
	if rs := rt.srv.runSlotOf(rt.agents[1].Ref()); rs != nil {
		t.Fatalf("agent-02, coding, has run slot %+v", rs)
	}
}

// The pool counts what the VM can grow to, not only what it has plugged in
// now: a VM plugged at 8 GiB that may reach 16 has 13 GiB for its agents, not
// 5, so a second 4 GiB run starts instead of waiting for memory the host
// would give. The host's ceiling holds it back while the host can't spare
// that much, but never to less than what is plugged, and with the cap unknown
// the pool is what is plugged (what it was before the cap counted).
func TestRunSlotsCountWhatTheVMCanReach(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string // short: it is part of the daemon's socket path
		vmCap    int64
		ceiling  int64 // the host's report, 0 for none
		wantPool int64
		granted  bool // whether a second 4 GiB run starts
	}{
		{"cap16", 16 * testGiB, 0, 13 * testGiB, true},
		{"capunknown", 0, 0, 5 * testGiB, false},
		{"ceil10", 16 * testGiB, 10 * testGiB, 7 * testGiB, false},
		{"ceil12", 16 * testGiB, 12 * testGiB, 9 * testGiB, true},
		{"ceilunder", 16 * testGiB, 6 * testGiB, 5 * testGiB, false},
		{"ceilover", 16 * testGiB, 20 * testGiB, 13 * testGiB, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rt := newGrowingRunSlotsTest(t, 2, 8*testGiB, c.vmCap)
			sl := rt.srv.slots
			sl.mu.Lock()
			sl.pool.SetPeak("hello-stack", runslots.Peak{Bytes: 4 * testGiB, Runs: 3, At: time.Now()})
			sl.mu.Unlock()
			if c.ceiling > 0 {
				h := &rt.srv.hostLoad
				h.mu.Lock()
				h.last, h.got = api.HostLoad{VMCeiling: c.ceiling}, time.Now()
				h.mu.Unlock()
			}
			rt.step(t, time.Now())
			// 4 GiB for the first run, half a GiB for the agent still coding.
			if ph := rt.up(t, 0, 0); ph.State != api.RunSlotHolding {
				t.Fatalf("the first run = %+v, want it granted", ph)
			}
			rt.step(t, time.Now())
			slots, err := rt.client.RunSlots(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if slots.Pool != c.wantPool {
				t.Errorf("the pool is %d GiB, want %d GiB", slots.Pool/testGiB, c.wantPool/testGiB)
			}
			ph := rt.up(t, 1, 0)
			if got := ph.State == api.RunSlotHolding; got != c.granted {
				t.Errorf("the second 4 GiB run: %+v, granted %v, want %v (pool %d GiB, free %d MiB)", ph, got, c.granted, slots.Pool/testGiB, slots.FreeMemory>>20)
			}
			if !c.granted && ph.State != api.RunSlotWaiting {
				t.Errorf("the refused run = %+v, want it waiting", ph)
			}
		})
	}
}

// With no slot free, agents wait in line, first come first served, each
// told how many are ahead; a slot given back goes to the front at once, to
// a request waiting for it.
func TestRunSlotsQueueFirstComeFirstServed(t *testing.T) {
	t.Parallel()
	// 6 GiB: the agents may have 3, one run of 2 GiB with them coding.
	rt := newRunSlotsTest(t, 3, 6*testGiB)
	if ph := rt.up(t, 0, 0); ph.State != api.RunSlotHolding {
		t.Fatalf("agent-01: %+v", ph)
	}
	if ph := rt.up(t, 1, 0); ph.State != api.RunSlotWaiting || ph.Ahead != 0 {
		t.Fatalf("agent-02: %+v, want waiting at the front", ph)
	}
	if ph := rt.up(t, 2, 0); ph.State != api.RunSlotWaiting || ph.Ahead != 1 {
		t.Fatalf("agent-03: %+v, want waiting with 1 ahead", ph)
	}
	if rs := rt.srv.runSlotOf(rt.agents[2].Ref()); rs == nil || rs.State != api.RunSlotWaiting || rs.Ahead != 1 {
		t.Fatalf("agent-03's status = %+v", rs)
	}
	slots, _ := rt.client.RunSlots(context.Background())
	if slots.Queued != 2 || slots.Free != 0 || len(slots.Queue) != 2 || slots.Queue[1].Agent != rt.agents[2].Ref() || slots.Queue[1].Ahead != 1 {
		t.Fatalf("GET /v1/run-slots = %+v", slots)
	}
	// agent-02 waits for it; agent-01 gives it back.
	got := make(chan api.RunPhase, 1)
	go func() { got <- rt.up(t, 1, 20) }()
	time.Sleep(100 * time.Millisecond)
	rt.down(t, 0)
	select {
	case ph := <-got:
		if ph.State != api.RunSlotHolding {
			t.Fatalf("agent-02 after agent-01's down: %+v", ph)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("agent-02 still waits after agent-01 gave its slot back")
	}
	if ph := rt.up(t, 2, 0); ph.State != api.RunSlotWaiting || ph.Ahead != 0 {
		t.Fatalf("agent-03: %+v, want at the front now", ph)
	}
	// agent-01 asking again goes to the back.
	if ph := rt.up(t, 0, 0); ph.State != api.RunSlotWaiting || ph.Ahead != 1 {
		t.Fatalf("agent-01 again: %+v, want behind agent-03", ph)
	}
}

// A lease nobody renews expires: what its run phase started is stopped —
// what ran in its cgroup, the containers started since it was granted, the
// recording, the emulator, the browser — the run's peak is learned for its
// project, and the next in line has the slot.
func TestRunSlotLeaseExpiresAndStopsWhatItStarted(t *testing.T) {
	t.Parallel()
	rt := newRunSlotsTest(t, 2, 6*testGiB)
	rt.ops.containers = []string{"db0000000000"} // running before
	ph := rt.up(t, 0, 0)
	if ph.State != api.RunSlotHolding {
		t.Fatalf("agent-01: %+v", ph)
	}
	if ph := rt.up(t, 1, 0); ph.State != api.RunSlotWaiting {
		t.Fatalf("agent-02: %+v", ph)
	}
	// The run phase starts a compose stack, the browser, the emulator and a
	// recording, and its machine peaks at 3 GiB.
	rt.ops.mu.Lock()
	rt.ops.containers = append(rt.ops.containers, "web000000000")
	rt.ops.mu.Unlock()
	ref := rt.agents[0].Ref()
	rt.srv.markRunPhase(ref, "", func(l *runslots.Lease) { l.Browser, l.Android, l.Recording = true, true, true })
	rt.ops.setUse(rt.agents[0].Instance, 3*testGiB)
	rt.step(t, time.Now())
	rt.ops.setUse(rt.agents[0].Instance, testGiB)
	// agent-02 keeps asking; agent-01 does nothing for the lease's time.
	later := time.Now().Add(runslots.LeaseTTL + time.Second)
	rt.srv.slots.mu.Lock()
	rt.srv.slots.pool.Up(rt.agents[1].Ref(), "hello-stack", later)
	rt.srv.slots.mu.Unlock()
	rt.step(t, later)

	deadline := time.Now().Add(10 * time.Second)
	for {
		killed, stopped, keep := rt.ops.snapshot()
		if len(stopped) == 3 {
			if len(killed) != 1 || killed[0] != rt.agents[0].Instance+"/"+ph.Key {
				t.Errorf("killed %v, want the run phase's cgroup %s", killed, ph.Key)
			}
			if len(keep) != 1 || !slices.Equal(keep[0], []string{"db0000000000"}) {
				t.Errorf("containers kept %v, want only the one running before", keep)
			}
			want := []string{ref + ": recording", ref + ": android", ref + ": browser"}
			if !slices.Equal(stopped, want) {
				t.Errorf("stopped %v, want %v", stopped, want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the expired run phase stopped only %v", stopped)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rs := rt.srv.runSlotOf(rt.agents[1].Ref()); rs == nil || rs.State != api.RunSlotHolding {
		t.Fatalf("agent-02 after agent-01's lease expired: %+v", rs)
	}
	// The peak was learned and kept.
	for {
		peaks, err := rt.srv.store.RunPeaks(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(peaks) == 1 {
			if peaks[0].Project != "hello-stack" || peaks[0].Bytes != 3*testGiB || peaks[0].Runs != 1 {
				t.Fatalf("run peaks = %+v, want hello-stack's at 3 GiB", peaks)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run's peak was never kept")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// `agentbox run down` stops what the run phase started and says so, and
// learns its peak, which the next of the project's runs reserves.
func TestRunDownStopsWhatTheRunPhaseStarted(t *testing.T) {
	t.Parallel()
	rt := newRunSlotsTest(t, 1, 16*testGiB)
	ph := rt.up(t, 0, 0)
	rt.ops.mu.Lock()
	rt.ops.containers = []string{"web000000000"}
	rt.ops.mu.Unlock()
	rt.srv.markRunPhase(rt.agents[0].Ref(), ph.Key, func(l *runslots.Lease) { l.Browser = true })
	rt.ops.setUse(rt.agents[0].Instance, 5*testGiB)
	rt.step(t, time.Now())
	out := rt.down(t, 0)
	if out.State != "" || out.Peak != 5*testGiB {
		t.Fatalf("run down = %+v, want it ended at a 5 GiB peak", out)
	}
	if len(out.Stopped) != 2 || !strings.Contains(out.Stopped[0], "1 container") || out.Stopped[1] != "the browser" {
		t.Fatalf("stopped %q", out.Stopped)
	}
	if killed, _, _ := rt.ops.snapshot(); len(killed) != 1 {
		t.Fatalf("killed %v", killed)
	}
	// The project's next run reserves what this one peaked at.
	if ph := rt.up(t, 0, 0); ph.State != api.RunSlotHolding {
		t.Fatal(ph)
	}
	slots, _ := rt.client.RunSlots(context.Background())
	if len(slots.Holders) != 1 || slots.Holders[0].Reserved != 5*testGiB || !slots.Projects[0].Learned || slots.Projects[0].Runs != 1 {
		t.Fatalf("after a learned run: %+v", slots)
	}
	// Down with nothing held is nothing.
	rt.down(t, 0)
	if out := rt.down(t, 0); out.State != "" || len(out.Stopped) != 0 {
		t.Fatalf("a second down: %+v", out)
	}
}

// An agent that stops or goes frees its slot, and its place in the queue.
func TestStoppedAgentFreesItsRunSlot(t *testing.T) {
	t.Parallel()
	rt := newRunSlotsTest(t, 3, 6*testGiB)
	rt.up(t, 0, 0)
	rt.up(t, 1, 0)
	rt.up(t, 2, 0)
	rt.srv.dropRunSlot(rt.agents[0].Ref())
	if rs := rt.srv.runSlotOf(rt.agents[1].Ref()); rs == nil || rs.State != api.RunSlotHolding {
		t.Fatalf("agent-02 after agent-01 stopped: %+v", rs)
	}
	// One that went from the database, without a word, too.
	if err := rt.srv.store.RemoveAgent(context.Background(), rt.agents[1].Project, rt.agents[1].Name); err != nil {
		t.Fatal(err)
	}
	rt.step(t, time.Now())
	if rs := rt.srv.runSlotOf(rt.agents[2].Ref()); rs == nil || rs.State != api.RunSlotHolding {
		t.Fatalf("agent-03 after agent-02 went: %+v", rs)
	}
}

// Starting the browser, the emulator or a recording from inside an agent is
// its run phase: with no slot free it is refused, saying what to do, and
// with one free it takes it.
func TestInAgentStartsNeedARunSlot(t *testing.T) {
	t.Parallel()
	rt := newRunSlotsTest(t, 2, 6*testGiB)
	rt.up(t, 0, 0)
	for _, path := range []string{"/v1/self/browser/start", "/v1/self/android/start", "/v1/self/media/record/start"} {
		code, body := rt.call(t, 1, http.MethodPost, path, "{}")
		var e api.Error
		_ = json.Unmarshal(body, &e)
		if code != http.StatusConflict || e.Code != api.ErrorNoRunSlot || !strings.Contains(e.Error, "agentbox run up") {
			t.Fatalf("%s without a slot: %d %s", path, code, body)
		}
	}
	// The desktop's tools ask without queueing.
	code, body := rt.call(t, 1, http.MethodPost, "/v1/self/run/up", `{"try":true,"browser":true}`)
	var ph api.RunPhase
	_ = json.Unmarshal(body, &ph)
	if code != http.StatusOK || ph.State != "" || ph.Ahead != 0 {
		t.Fatalf("try without a slot: %d %s", code, body)
	}
	if rs := rt.srv.runSlotOf(rt.agents[1].Ref()); rs != nil {
		t.Fatalf("a try queued agent-02: %+v", rs)
	}
	// Joining a run phase it doesn't have is refused too.
	if code, body := rt.call(t, 1, http.MethodPost, "/v1/self/run/join", `{"pid":42}`); code != http.StatusConflict {
		t.Fatalf("join without a slot: %d %s", code, body)
	}
	rt.down(t, 0)
	code, body = rt.call(t, 1, http.MethodPost, "/v1/self/run/up", `{"try":true,"browser":true}`)
	_ = json.Unmarshal(body, &ph)
	if code != http.StatusOK || ph.State != api.RunSlotHolding {
		t.Fatalf("try with a slot free: %d %s", code, body)
	}
	st := rt.srv.slots
	st.mu.Lock()
	l := st.pool.Status(rt.agents[1].Ref()).Lease
	st.mu.Unlock()
	if !l.Browser || !l.ContainersKnown {
		t.Fatalf("the desktop's lease %+v, want the browser marked and the containers read", l)
	}
	if code, body := rt.call(t, 1, http.MethodPost, "/v1/self/run/join", `{"pid":42}`); code != http.StatusNoContent {
		t.Fatalf("join with a slot: %d %s", code, body)
	}
}

// An agent coding has a memory.high under its memory.max, so a dev server it
// left running is squeezed; one in its run phase has none.
func TestCodePhaseIsSqueezedAndTheRunPhaseIsNot(t *testing.T) {
	t.Parallel()
	rt := newRunSlotsTest(t, 2, 16*testGiB)
	var mu sync.Mutex
	high := map[string]int64{}
	rt.srv.setMemoryHigh = func(instance string, n int64) error {
		mu.Lock()
		defer mu.Unlock()
		high[instance] = n
		return nil
	}
	rt.up(t, 0, 0)
	rt.srv.watchMemory(context.Background())
	limit := agent.MemoryLimit(16*testGiB, agent.DefaultAgentMemoryShare)
	mu.Lock()
	defer mu.Unlock()
	if high[rt.agents[0].Instance] != 0 {
		t.Errorf("the run phase's memory.high = %d, want none", high[rt.agents[0].Instance])
	}
	if want := agent.CodePhaseHigh(limit); want <= 0 || high[rt.agents[1].Instance] != want {
		t.Errorf("the code phase's memory.high = %d, want %d", high[rt.agents[1].Instance], want)
	}
}
