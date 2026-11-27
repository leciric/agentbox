package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

const testGiB = int64(1) << 30

// With "Fewer build workers when memory is short" on, as it is unless turned
// off, a heavy command gets its share of the cores, no more than the memory
// it can start in holds at what one worker of its tool takes (go test's
// default, 1 GiB, until a run has finished), and at least one; the other
// heavy commands running keep what they will likely still take; and its
// agent's own limit, when that leaves less, is what counts.
func TestHeavyWorkersCountMemory(t *testing.T) {
	t.Parallel()
	room := int64(-1)
	p := newPressureTest(t, func(s *Server) {
		s.vmCores = func() int { return 12 }
		s.agentRoom = func(string) (int64, bool) { return room, room >= 0 }
	})
	ctx := context.Background()
	if settings, err := p.client.Settings(ctx); err != nil || !settings.FewerWorkersForMemory {
		t.Fatalf("settings = %v, %v: want Fewer build workers when memory is short on", settings.FewerWorkersForMemory, err)
	}

	// Unread memory: the cores alone.
	if got := p.ask(t, p.instance, "unread"); got.Workers != 12 || got.Count.Memory {
		t.Errorf("before the VM's memory is read: %+v, want 12 workers from the cores", got.Count)
	}
	p.srv.endRuns(func(g *pressure.Runs) []*pressure.Run { return []*pressure.Run{g.End(p.ref, "unread")} }, false)

	// 4 GiB available, and 4 more the VM can still grow to: 8 workers of
	// 1 GiB, under the cores' 12.
	p.mu.Lock()
	p.sample.Mem.Total, p.sample.Mem.Cap, p.sample.Mem.Available = 12*testGiB, 16*testGiB, 4*testGiB
	p.mu.Unlock()
	p.srv.stepPressure(ctx)
	first := p.ask(t, p.instance, "first")
	if c := first.Count; first.Workers != 8 || !c.Memory || c.Limit != agent.LimitMemory || c.Tool != "go test" ||
		c.PerWorker != testGiB || c.Learned || c.Free != 8*testGiB || c.CPU != 12 {
		t.Errorf("first: %d workers, %+v; want 8 of 1 GiB in 8 GiB free", first.Workers, c)
	}
	// Asking again answers what it started with.
	if again := p.ask(t, p.instance, "first"); again.Workers != 8 {
		t.Errorf("first, asked again: %d workers, want 8", again.Workers)
	}

	// The first holds 2 GiB of the 8 it will likely take: 6 are kept for it,
	// and the second has 2 GiB, though its share of the cores is 6.
	p.cg.setMemory(p.instance, "first", 2*testGiB, 2*testGiB)
	p.place(t, "first")
	second := p.ask(t, p.instance, "second")
	if c := second.Count; second.Workers != 2 || c.Claimed != 6*testGiB || c.Free != 2*testGiB || c.CPU != 6 {
		t.Errorf("second: %d workers, %+v; want 2 in 2 GiB free, 6 claimed", second.Workers, c)
	}

	// Nothing left: one worker.
	p.mu.Lock()
	p.sample.Mem.Available, p.sample.Mem.Cap = testGiB/2, 0
	p.mu.Unlock()
	p.srv.stepPressure(ctx)
	if third := p.ask(t, p.instance, "third"); third.Workers != 1 || third.Count.Limit != agent.LimitLeast {
		t.Errorf("third: %d workers, %+v; want 1, the least", third.Workers, third.Count)
	}

	// The agent's own limit leaves less than the VM.
	p.srv.endRuns(func(g *pressure.Runs) []*pressure.Run {
		return []*pressure.Run{g.End(p.ref, "second"), g.End(p.ref, "third")}
	}, false)
	p.mu.Lock()
	p.sample.Mem.Available = 64 * testGiB
	p.mu.Unlock()
	p.srv.stepPressure(ctx)
	room = 9 * testGiB // 6 of it the first's to take
	if fourth := p.ask(t, p.instance, "fourth"); fourth.Workers != 3 || fourth.Count.Limit != agent.LimitAgent {
		t.Errorf("fourth: %d workers, %+v; want 3, what the agent's limit leaves", fourth.Workers, fourth.Count)
	}

	// The Running view's tree whose processes are in the first's cgroup
	// shows its count; the pressure report does too.
	if w := p.srv.runWorkers(p.instance, []string{"init.scope", agent.RunCgroupName("first") + "/child"}); w == nil || w.Workers != 8 {
		t.Errorf("the first's tree: %+v, want its 8 workers", w)
	}
	if w := p.srv.runWorkers(p.instance, []string{"init.scope"}); w != nil {
		t.Errorf("a tree in no run: %+v", w)
	}
	if ps, err := p.client.Pressure(ctx); err != nil || len(ps.Runs) == 0 || ps.Runs[0].Workers == nil || ps.Runs[0].Workers.Workers != 8 {
		t.Errorf("GET /v1/pressure = %+v, %v; want the first with its 8 workers", ps.Runs, err)
	}
}

// What one worker takes is learned from the runs that end, per project and
// tool: a run that peaked at 4 GiB with 8 workers brings go test's 1 GiB a
// quarter of the way down to 512 MiB; a failed one that took less teaches
// nothing; one that took more raises it at once, failed or not; and the next
// command counts with what was learned, which outlives the daemon.
func TestHeavyWorkersLearnFromFinishedRuns(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t, func(s *Server) {
		s.vmCores = func() int { return 8 }
		s.agentRoom = func(string) (int64, bool) { return 0, false }
	})
	ctx := context.Background()
	p.setAvailable(64, 0)
	p.srv.stepPressure(ctx)
	run := func(key string, peak int64, exit int) {
		t.Helper()
		if got := p.ask(t, p.instance, key); got.Workers != 8 {
			t.Fatalf("%s: %d workers, want 8", key, got.Workers)
		}
		p.place(t, key)
		p.cg.setMemory(p.instance, key, 0, peak)
		body := `{"command":"go test ./` + key + `","jobs":8,"exitCode":` + strconv.Itoa(exit) + `,"durationMs":1000}`
		r := httptest.NewRequest(http.MethodDelete, "/v1/self/heavy/"+key, strings.NewReader(body))
		r.SetPathValue("key", key)
		if err := p.srv.endHeavy(p.instance)(httptest.NewRecorder(), r); err != nil {
			t.Fatal(err)
		}
	}
	learned := func(want int64) {
		t.Helper()
		w, ok, err := p.srv.store.WorkerMemory(ctx, "hello-stack", "go test")
		if err != nil || !ok || w.PerWorker != want {
			t.Fatalf("go test's worker in hello-stack: %+v, %v, %v; want %d", w, ok, err, want)
		}
	}
	run("a", 4*testGiB, 0)
	learned(testGiB - testGiB/8) // 1 GiB less a quarter of the 512 MiB between
	run("b", testGiB, 1)         // failed, and took less: nothing
	learned(testGiB - testGiB/8)
	run("c", 16*testGiB, 1) // failed, and took 2 GiB a worker: at once
	learned(2 * testGiB)

	// The next go test counts 2 GiB a worker: 8 of 8 cores in 64 GiB, so
	// with 10 GiB free, 5.
	p.setAvailable(10, 0)
	p.srv.stepPressure(ctx)
	if got := p.ask(t, p.instance, "d"); got.Workers != 5 || !got.Count.Learned || got.Count.PerWorker != 2*testGiB {
		t.Errorf("d: %d workers, %+v; want 5 of a learned 2 GiB", got.Workers, got.Count)
	}

	// A daemon starting afresh reads it from the store.
	p.srv.workerMemory = newWorkerMemory()
	if per, ok := p.srv.perWorker(ctx, "hello-stack", "go test"); !ok || per != 2*testGiB {
		t.Errorf("after a restart: %d, %v; want the learned 2 GiB", per, ok)
	}
	if per, ok := p.srv.perWorker(ctx, "other-project", "go test"); ok || per != agent.WorkerDefault("go test") {
		t.Errorf("another project: %d, %v; want go test's default", per, ok)
	}
}

// A command that started before the daemon did says, as it asks again, the
// count it started with: that is what the daemon keeps of it, not a new one.
func TestHeavyWorkersKeptAcrossARestart(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t, func(s *Server) { s.vmCores = func() int { return 12 } })
	r := httptest.NewRequest(http.MethodPost, "/v1/self/heavy", strings.NewReader(`{"key":"old","command":"go test ./...","workers":3}`))
	w := httptest.NewRecorder()
	if err := p.srv.startHeavy(p.instance)(w, r); err != nil {
		t.Fatal(err)
	}
	if c, ok := p.srv.countOf(p.ref + "/old"); !ok || c.Workers != 3 || c.Limit != "" {
		t.Errorf("a run seen first asking again: %+v, %v; want the 3 it said it started with", c, ok)
	}
}

// The setting is on until turned off, and turning it off sticks.
func TestFewerWorkersForMemorySetting(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	ctx := context.Background()
	off := false
	got, err := p.client.UpdateSettings(ctx, api.UpdateSettingsRequest{FewerWorkersForMemory: &off})
	if err != nil || got.FewerWorkersForMemory {
		t.Fatalf("turned off: %v, %v", got.FewerWorkersForMemory, err)
	}
	if on, err := p.srv.store.FlagOn(ctx, state.SettingFewerWorkersForMemory); err != nil || on {
		t.Errorf("stored: %v, %v; want off", on, err)
	}
	on := true
	if got, err := p.client.UpdateSettings(ctx, api.UpdateSettingsRequest{FewerWorkersForMemory: &on}); err != nil || !got.FewerWorkersForMemory {
		t.Errorf("turned on again: %v, %v", got.FewerWorkersForMemory, err)
	}
}

// place puts a started run of the agent's in its cgroup, as its shim does
// (POST /v1/self/heavy/{key}/join).
func (p *pressureTest) place(t *testing.T, key string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/self/heavy/"+key+"/join", strings.NewReader(`{"pid":100}`))
	r.SetPathValue("key", key)
	if err := p.srv.joinHeavy(p.instance)(httptest.NewRecorder(), r); err != nil {
		t.Fatal(err)
	}
}
