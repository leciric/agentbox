package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A driver is one way of running AgentBox's agents: today's containers on the
// host, the Cloud Hypervisor VM through agentbox's front end, or the
// prototype that drives Cloud Hypervisor directly. The plan (runMode) is the
// same for every driver, so what differs in the table is the mode and nothing
// else.
type driver interface {
	name() string
	// describe is the mode's configuration, for the JSON: versions, sizes,
	// the VM's command line.
	describe() map[string]any
	// preflight checks what the mode needs and that nothing of an earlier run
	// is left, before anything is changed.
	preflight(ctx context.Context) error
	// setup installs the mode from nothing, as a new user would, timing each
	// step with m.step; with the project the agents build registered.
	setup(ctx context.Context, m *modeRun) error
	vm() vmOps // nil when the mode has no VM
	create(ctx context.Context, agent string) error
	action(ctx context.Context, agent, action string) error // start, stop, pause or resume
	// exec runs a bash script in the agent, in its copy of the repository.
	exec(ctx context.Context, agent, script string) error
	// held is the memory the host holds for the mode's agents, read cheaply
	// once a second: the agents' cgroups, or the VM's process.
	held() int64
	// cleanup removes everything the harness made in this mode, and nothing
	// else. It is safe to run again, and after a run that was killed.
	cleanup(ctx context.Context) error
}

type vmOps interface {
	boot(ctx context.Context) error // start the VM and wait until agents can be made
	shutdown(ctx context.Context) error
	pause(ctx context.Context) error
	resume(ctx context.Context) error
}

type step struct {
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
	Error   string  `json:"error,omitempty"`
	Note    string  `json:"note,omitempty"`
	At      float64 `json:"at"` // seconds since the mode's sampler started
}

type memPoint struct {
	Name        string  `json:"name"`
	HostUsedGiB float64 `json:"hostUsedGiB"` // MemTotal - MemAvailable
	DeltaGiB    float64 `json:"deltaGiB"`    // over the mode's baseline, taken before it set anything up
	HeldGiB     float64 `json:"heldGiB"`     // what the driver holds: agents' cgroups, or the VM process
}

type modeResult struct {
	Mode     string             `json:"mode"`
	Config   map[string]any     `json:"config"`
	Steps    []step             `json:"steps"`
	Metrics  map[string]float64 `json:"metrics"`
	Notes    map[string]string  `json:"notes,omitempty"`
	Memory   []memPoint         `json:"memory"`
	Windows  map[string]*window `json:"windows"`
	Errors   []string           `json:"errors,omitempty"`
	Samples  map[string]any     `json:"samples"`
	Started  time.Time          `json:"started"`
	Finished time.Time          `json:"finished"`
}

type modeRun struct {
	opts    *options
	d       driver
	r       *runner
	s       *sampler
	res     *modeResult
	mu      sync.Mutex
	baseGiB float64
}

func (m *modeRun) say(format string, args ...any) {
	fmt.Printf("[%s] "+format+"\n", append([]any{m.d.name()}, args...)...)
	m.r.logf("### "+format, args...)
}

// step times fn, records it, and says how it went.
func (m *modeRun) step(ctx context.Context, key, name string, fn func(context.Context) error) (float64, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	at := m.s.since()
	t0 := time.Now()
	err := fn(ctx)
	secs := time.Since(t0).Seconds()
	st := step{Key: key, Name: name, Seconds: secs, At: at}
	if err != nil {
		st.Error = err.Error()
		m.say("%s: failed after %s: %v", name, fmtSecs(secs), err)
	} else {
		m.say("%s: %s", name, fmtSecs(secs))
	}
	m.mu.Lock()
	m.res.Steps = append(m.res.Steps, st)
	m.mu.Unlock()
	return secs, err
}

func (m *modeRun) metric(key string, v float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.res.Metrics[key] = v
}

func (m *modeRun) note(key, note string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.res.Notes[key] != "" {
		note = m.res.Notes[key] + "; " + note
	}
	m.res.Notes[key] = note
}

func (m *modeRun) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.res.Errors = append(m.res.Errors, err.Error())
}

// timed runs a step and keeps its time as a metric; a failed step keeps no
// number, and says why in the notes.
func (m *modeRun) timed(ctx context.Context, key, name string, fn func(context.Context) error) error {
	secs, err := m.step(ctx, key, name, fn)
	if err != nil {
		if ctx.Err() == nil {
			m.note(key, "failed: "+firstLine(err.Error()))
			m.fail(fmt.Errorf("%s: %w", name, err))
		}
		return err
	}
	m.metric(key, secs)
	return nil
}

// pairs runs n rounds of down then up (stop and start, pause and resume),
// so that each is timed from the same state, and keeps the median of each.
// It says whether every round got back up.
func (m *modeRun) pairs(ctx context.Context, n int, downKey, downName string, down func(context.Context) error, upKey, upName string, up func(context.Context) error) bool {
	var downs, ups []float64
	ok := true
	for i := range n {
		s1, err := m.step(ctx, downKey, fmt.Sprintf("%s (%d/%d)", downName, i+1, n), down)
		if err != nil {
			m.failed(ctx, downKey, downName, err)
			break
		}
		s2, err := m.step(ctx, upKey, fmt.Sprintf("%s (%d/%d)", upName, i+1, n), up)
		if err != nil {
			m.failed(ctx, upKey, upName, err)
			ok = false
			break
		}
		downs, ups = append(downs, s1), append(ups, s2)
	}
	if len(ups) > 0 {
		m.metric(downKey, median(downs))
		m.metric(upKey, median(ups))
	}
	return ok && ctx.Err() == nil
}

func (m *modeRun) failed(ctx context.Context, key, name string, err error) {
	if ctx.Err() != nil {
		return
	}
	m.note(key, "failed: "+firstLine(err.Error()))
	m.fail(fmt.Errorf("%s: %w", name, err))
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func (m *modeRun) memPoint(name string) {
	mp := memPoint{Name: name, HeldGiB: gib(m.d.held())}
	if mi, err := readMeminfo(); err == nil {
		mp.HostUsedGiB = gib(mi["MemTotal"] - mi["MemAvailable"])
	}
	if name == "baseline" {
		m.baseGiB = mp.HostUsedGiB
	}
	mp.DeltaGiB = mp.HostUsedGiB - m.baseGiB
	m.mu.Lock()
	m.res.Memory = append(m.res.Memory, mp)
	m.res.Metrics["mem."+name+".delta"] = mp.DeltaGiB
	m.res.Metrics["mem."+name+".held"] = mp.HeldGiB
	m.mu.Unlock()
	m.say("memory %s: host +%.2f GiB over baseline, agents/VM hold %.2f GiB", name, mp.DeltaGiB, mp.HeldGiB)
}

// window samples the host while fn runs, then for tail more, and keeps the
// summary under name.
func (m *modeRun) window(ctx context.Context, name string, tail time.Duration, fn func(context.Context) error) error {
	from := m.s.since()
	err := fn(ctx)
	if ctx.Err() == nil {
		_ = sleepCtx(ctx, tail)
	}
	w := m.s.window(name, from, m.s.since())
	m.mu.Lock()
	m.res.Windows[name] = w
	m.mu.Unlock()
	m.say("host during %s: fsync p50 %.1f ms, p95 %.1f ms, max %.0f ms; io some %.0f%% (max %.0f%%), full %.0f%% (max %.0f%%); btrfs commit max %.0f ms",
		name, w.Fsync.P50, w.Fsync.P95, w.Fsync.Max, w.IOSomeAvg, w.IOSomeMax, w.IOFullAvg, w.IOFullMax, w.CommitMaxMs)
	return err
}

func agentName(i int) string { return fmt.Sprintf("bench-%d", i) }

// runMode is the plan every mode runs, in order. A step that fails is
// recorded and the plan carries on with what doesn't depend on it; setup or
// the first agent failing ends the mode.
func runMode(ctx context.Context, opts *options, d driver) *modeResult {
	res := &modeResult{Mode: d.name(), Config: d.describe(), Metrics: map[string]float64{}, Notes: map[string]string{},
		Windows: map[string]*window{}, Samples: map[string]any{}, Started: time.Now()}
	logf, err := os.Create(filepath.Join(opts.out, "logs", d.name()+".log"))
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	defer func() { _ = logf.Close() }()
	m := &modeRun{opts: opts, d: d, res: res, r: &runner{log: logf}}
	if lr, ok := d.(interface{ setRunner(*runner) }); ok {
		lr.setRunner(m.r)
	}
	s, err := newSampler(opts.probeDir, d.held)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	m.s = s
	sctx, stopSampler := context.WithCancel(context.Background())
	var sampling sync.WaitGroup
	sampling.Go(func() { s.run(sctx) })

	m.say("checking prerequisites")
	if err := d.preflight(ctx); err != nil {
		m.say("can't run: %v", err)
		m.fail(err)
	} else {
		m.memPoint("baseline")
		m.plan(ctx)
		if ctx.Err() != nil {
			m.say("interrupted: cleaning up")
			m.fail(errors.New("interrupted"))
		}
		if opts.keep {
			m.say("--keep: leaving the agents and the VM as they are; clean up with %s --cleanup --modes %s", os.Args[0], d.name())
		} else {
			cctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			cctx, stop := cleanupSignals(cctx)
			if _, err := m.step(cctx, "cleanup", "clean up", d.cleanup); err != nil {
				m.fail(fmt.Errorf("cleanup: %w: run %s --cleanup --modes %s", err, os.Args[0], d.name()))
			}
			stop()
			cancel()
		}
	}
	stopSampler()
	sampling.Wait()
	fs, hs, errs := s.snapshot()
	res.Samples["fsync"] = fs
	res.Samples["host"] = hs
	res.Samples["btrfsCommitStats"] = s.btrfs
	res.Samples["disks"] = s.disks
	for _, e := range errs {
		res.Errors = append(res.Errors, "sampler: "+e)
	}
	res.Finished = time.Now()
	return res
}

func (m *modeRun) plan(ctx context.Context) {
	o := m.opts
	d := m.d
	if err := d.setup(ctx, m); err != nil {
		m.failed(ctx, "setup.total", "setup", err)
		return
	}
	for _, st := range m.res.Steps {
		if st.Key == "setup.step" && st.Error == "" {
			m.res.Metrics["setup.total"] += st.Seconds
			m.res.Metrics["setup.steps"]++
		}
	}
	if vm := d.vm(); vm != nil && ctx.Err() == nil {
		m.pairs(ctx, o.reps, "vm.pause", "VM pause", vm.pause, "vm.resume", "VM resume", vm.resume)
		if !m.pairs(ctx, o.bootReps, "vm.shutdown", "VM shut down", vm.shutdown, "vm.boot", "VM boot", vm.boot) {
			return
		}
	}

	// Idle: installed, and a VM freshly booted, with no agents.
	_ = sleepCtx(ctx, o.settle)
	m.memPoint("idle")
	_ = m.window(ctx, "idle", 0, func(ctx context.Context) error { return sleepCtx(ctx, o.idleWindow) })

	// One agent, and what it takes to make, stop, start, pause and resume it.
	var creates []float64
	create := func(i int) error {
		secs, err := m.step(ctx, "agent.create", "create "+agentName(i), func(ctx context.Context) error { return d.create(ctx, agentName(i)) })
		if err != nil {
			m.failed(ctx, "agent.create", "create "+agentName(i), err)
			return err
		}
		creates = append(creates, secs)
		m.metric("agent.create", median(creates))
		return nil
	}
	if create(1) != nil {
		return
	}
	_ = sleepCtx(ctx, o.settle)
	m.memPoint("agents1")
	a1 := agentName(1)
	act := func(action string) func(context.Context) error {
		return func(ctx context.Context) error { return d.action(ctx, a1, action) }
	}
	m.pairs(ctx, o.reps, "agent.stop", "stop "+a1, act("stop"), "agent.start", "start "+a1, act("start"))
	m.pairs(ctx, o.reps, "agent.pause", "pause "+a1, act("pause"), "agent.resume", "resume "+a1, act("resume"))
	if ctx.Err() != nil {
		return
	}

	// Memory with 3 and 5 agents, idle: made and running, doing nothing.
	made := 1
	for _, want := range []int{3, 5} {
		for made < want && ctx.Err() == nil {
			if create(made+1) != nil {
				break
			}
			made++
		}
		if made == want {
			_ = sleepCtx(ctx, o.settle)
			m.memPoint(fmt.Sprintf("agents%d", want))
		}
	}

	// One agent's build, cold then warm.
	if !o.skip["build"] && ctx.Err() == nil {
		_ = m.step0(ctx, "clear caches in "+a1, func(ctx context.Context) error { return d.exec(ctx, a1, coldScript) })
		_ = m.window(ctx, "build1", 0, func(ctx context.Context) error {
			m.build(ctx, a1, "build.go.cold", "go build, cold", o.goCold)
			m.build(ctx, a1, "build.go.warm", "go build, warm", o.goWarm)
			m.build(ctx, a1, "build.npm.cold", "desktop build, cold", o.npm)
			m.build(ctx, a1, "build.npm.warm", "desktop build, warm", o.npm)
			return nil
		})
	}

	// Three agents building at once, cold, while the host is sampled.
	if !o.skip["build3"] && made >= 4 && ctx.Err() == nil {
		script := coldScript + "\n" + o.goCold
		if o.npm != "" {
			script += "\n" + o.npm
		}
		_ = m.window(ctx, "build3", o.settle, func(ctx context.Context) error {
			return m.timed(ctx, "build3.wall", "3 agents building at once", func(ctx context.Context) error {
				var wg sync.WaitGroup
				errs := make([]error, 3)
				for i := range 3 {
					a := agentName(i + 2)
					wg.Go(func() {
						_, errs[i] = m.step(ctx, "build3.agent", a+" build", func(ctx context.Context) error { return d.exec(ctx, a, script) })
					})
				}
				wg.Wait()
				return errors.Join(errs...)
			})
		})
		if w := m.res.Windows["build3"]; w != nil {
			m.metric("mem.build3.heldMax", w.HeldMaxGiB)
		}
	}

	// A sustained write in one agent, while the host is sampled, and for a
	// while after: the freezes came with the writeback, after the writes.
	if !o.skip["write"] && made >= 5 && ctx.Err() == nil {
		a5 := agentName(5)
		mib := int(o.writeGiB * 1024)
		script := fmt.Sprintf("dd if=/dev/urandom of=.bench-write.bin bs=1M count=%d conv=fsync status=none\nrm -f .bench-write.bin", mib)
		_ = m.window(ctx, "write", o.writeTail, func(ctx context.Context) error {
			return m.timed(ctx, "write.seconds", fmt.Sprintf("write %.0f GiB in %s", o.writeGiB, a5), func(ctx context.Context) error {
				return d.exec(ctx, a5, script)
			})
		})
		if secs, ok := m.res.Metrics["write.seconds"]; ok && secs > 0 {
			m.metric("write.mibs", float64(mib)/secs)
		}
	}

	// Stop every agent: does the memory come back?
	if ctx.Err() == nil {
		for i := 1; i <= made; i++ {
			a := agentName(i)
			_ = m.step0(ctx, "stop "+a, func(ctx context.Context) error { return d.action(ctx, a, "stop") })
		}
		_ = sleepCtx(ctx, o.settle)
		m.memPoint("stopped")
		_ = sleepCtx(ctx, o.reclaimWait)
		if ctx.Err() == nil {
			m.memPoint("stopped_later")
		}
		// A VM keeps what its guest cached for the agents: free page reporting
		// only gives back pages the guest frees. Dropping the guest's caches is
		// what a VM mode could do when its agents stop.
		if r, ok := d.(interface {
			dropCaches(context.Context) error
		}); ok && ctx.Err() == nil {
			if m.step0(ctx, "drop the guest's caches", r.dropCaches) == nil {
				_ = sleepCtx(ctx, o.settle)
				m.memPoint("caches_dropped")
			}
		}
	}
	if ov, ok := d.(interface{ ownsVM() bool }); ok && ov.ownsVM() && ctx.Err() == nil {
		if _, err := m.step(ctx, "vm.shutdown", "VM shut down", d.vm().shutdown); err == nil {
			_ = sleepCtx(ctx, o.settle)
			m.memPoint("vm_off")
		}
	}
}

// setupStep times one step of installing the mode.
func (m *modeRun) setupStep(ctx context.Context, name string, fn func(context.Context) error) (float64, error) {
	return m.step(ctx, "setup.step", name, fn)
}

// recordStep records a step the harness didn't run itself, with the time it
// took from elsewhere (or none) and why.
func (m *modeRun) recordStep(key, name string, secs float64, note string) {
	m.say("%s: %s (%s)", name, fmtSecs(secs), note)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.res.Steps = append(m.res.Steps, step{Key: key, Name: name, Seconds: secs, Note: note, At: m.s.since()})
}

// step0 is a step that isn't a metric: its time is in the steps, and its
// failure in the errors.
func (m *modeRun) step0(ctx context.Context, name string, fn func(context.Context) error) error {
	_, err := m.step(ctx, "other", name, fn)
	if err != nil && ctx.Err() == nil {
		m.fail(fmt.Errorf("%s: %w", name, err))
	}
	return err
}

// build runs one build command in an agent and keeps its time, even when it
// fails: a test failing in an agent still took that long to compile and run,
// and the note says it failed.
func (m *modeRun) build(ctx context.Context, agent, key, name, script string) {
	if script == "" || ctx.Err() != nil {
		return
	}
	secs, err := m.step(ctx, key, name+" in "+agent, func(ctx context.Context) error { return m.d.exec(ctx, agent, script) })
	if ctx.Err() != nil {
		return
	}
	m.metric(key, secs)
	if err != nil {
		m.note(key, "exited non-zero: "+firstLine(err.Error()))
	}
}

// coldScript empties the agent's Go and npm caches, and the desktop app's
// node_modules, so a build after it is cold whatever the base image came
// with (agentbox image build --dev-caches fills them).
const coldScript = `if command -v go >/dev/null 2>&1; then
  mc=$(go env GOMODCACHE); gc=$(go env GOCACHE)
  [ -d "$mc" ] && chmod -R u+w "$mc"
  rm -rf "$mc" "$gc"
fi
rm -rf "$HOME/.npm" desktop/node_modules
true`

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func fmtSecs(s float64) string {
	switch {
	case s < 1:
		return fmt.Sprintf("%.0f ms", s*1000)
	case s < 120:
		return fmt.Sprintf("%.1f s", s)
	default:
		return fmt.Sprintf("%dm%02ds", int(s)/60, int(s)%60)
	}
}
