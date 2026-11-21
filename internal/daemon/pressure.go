package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// Memory is the kernel's to protect, as in T3 Code (19 Nov 2026, the user's
// decision): each agent's machine has a hard memory.max (agent.MemoryLimit),
// and every agent's together a memory.high and memory.max under
// agentbox.agents (agent/sharedmemory.go), so a process that outgrows what
// it may have is killed at once, and the agent is told what was killed, at
// what size and for which limit (watchMemory). Nothing waits or is paused
// for memory: an agent's machine boots when asked (admission.go), and a
// heavy command (a test run, a build) starts the moment it asks. Until then
// heavy commands waited while the VM's PSI said memory was short and were
// paused when it stayed short, and PSI counts tasks stalled at those limits
// of their own too: on 19 Nov 2026 the agents throttled at their shared
// limit kept builds waiting and paused, and every boot behind them, for
// twenty minutes and more with the VM's memory free.
//
// A heavy command still asks, through the in-agent API: the shims for go,
// cargo, npm, make… first in every agent's PATH, whatever its AI tool
// (agent/heavyshims.go, cli.heavyShim), or `agentbox heavy -- <cmd>` around
// anything else. Asking is how the daemon knows an agent works (idle
// reclaim, the VM's own idle shrink, the package cache), and how many
// workers it starts with: the VM's cores split between the heavy commands
// running then (agent.HeavyWorkers), whatever the memory. As it starts, its
// shell joins a cgroup of its own under the agent's machine's
// (agent.PlaceRun), which the experimental "pause whole agents"
// (agentfreeze.go) freezes rather than the whole machine when freezing that
// would free nothing.
//
// The loop that reads the VM's memory is what must still work when the VM
// has none to spare: a reading allocates nothing and opens nothing
// (pressure.Sampler), the loop does nothing else (the agents' limits and
// kills are watched on a loop of their own, and the app told aside, as that
// asks Incus), a run's freezer is written directly rather than through sudo
// (agent.PlaceRun hands its cgroup to the daemon's user), and in the VM the
// daemon's own memory is locked in and the OOM killer told to spare it
// (protectDaemon).

const (
	// pressureInterval is how often the VM's pressure is read, and the
	// agents' memory limits and kills looked at.
	pressureInterval = 2 * time.Second
	// oomScoreAdj is the daemon's in the VM: the OOM killer takes a process
	// of the daemon's only once it holds 90% of the VM more than any other.
	oomScoreAdj = -900
	// heavyTTL is how long a heavy command that didn't join a cgroup of its
	// own counts as running unless asked for again or ended; heavyMaxTTL
	// the most a request may ask for.
	heavyTTL    = 15 * time.Minute
	heavyMaxTTL = time.Hour
)

// runCgroups is what the daemon does to the runs' cgroups: agent's
// functions, or a test's.
type runCgroups interface {
	Place(instance, key string, pid int) error
	Freeze(instance, key string, on bool) error
	Populated(instance, key string) bool
	Remove(instance, key string) error
}

type agentRunCgroups struct{}

func (agentRunCgroups) Place(instance, key string, pid int) error {
	return agent.PlaceRun(instance, key, pid)
}
func (agentRunCgroups) Freeze(instance, key string, on bool) error {
	return agent.FreezeRun(instance, key, on)
}
func (agentRunCgroups) Populated(instance, key string) bool {
	return agent.RunPopulated(instance, key)
}
func (agentRunCgroups) Remove(instance, key string) error { return agent.RemoveRun(instance, key) }

// heavyRuns is the heavy commands going and what the daemon keeps beside
// them, under mu.
type heavyRuns struct {
	mu sync.Mutex
	g  *pressure.Runs
	// instance is each run's agent's machine, by run ID.
	instance map[string]string
	// told are the runs whose agents were told the fleet paused them, by
	// run ID.
	told map[string]bool
	// psiErr is the last error reading the VM's pressure, logged once.
	psiErr string
	// refresh asks for the agents to be refreshed (refreshAgents), aside
	// from the loop.
	refresh chan struct{}
	// reading is the loop's last reading of the VM, taken at readAt, with
	// the trend it made (resuming after a restart waits on them).
	reading pressure.Sample
	trend   pressure.Trend
	readAt  time.Time
}

func newHeavyRuns() *heavyRuns {
	return &heavyRuns{
		g:        pressure.NewRuns(pressure.DefaultPolicy),
		instance: map[string]string{},
		told:     map[string]bool{},
		refresh:  make(chan struct{}, 1),
	}
}

// readSample reads the VM's memory through a pressure.Sampler, opened at the
// first reading. Its cap is what the front end said the VM may grow to.
func readSample() func() (pressure.Sample, error) {
	var (
		mu sync.Mutex
		s  *pressure.Sampler
	)
	vmCap, _ := strconv.ParseInt(os.Getenv(agent.VMMemoryCapEnv), 10, 64)
	return func() (pressure.Sample, error) {
		mu.Lock()
		if s == nil {
			var err error
			if s, err = pressure.OpenSampler(pressure.File, pressure.MemInfo, pressure.VMStat, vmCap); err != nil {
				mu.Unlock()
				return pressure.Sample{}, err
			}
			s.ReadSwaps(pressure.Swaps)
		}
		mu.Unlock()
		return s.Read()
	}
}

// runPressure is the pressure loop, and the loops beside it that may wait on
// SQLite, Incus or sudo: refreshing the agents as what holds them changes,
// and watching their memory limits and kills.
func (s *Server) runPressure(ctx context.Context) {
	s.loadFleet(ctx)
	s.loadRunPeaks(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.heavy.refresh:
				s.refreshAgents(ctx)
			}
		}
	}()
	if s.pressureEvery <= 0 {
		return // a test steps it itself
	}
	s.protectDaemon()
	go func() {
		ticker := time.NewTicker(s.pressureEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.watchMemory(ctx)
			}
		}
	}()
	ticker := time.NewTicker(s.pressureEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.stepPressure(ctx)
		}
	}
}

// protectDaemon keeps the daemon going when the VM runs out of memory: its
// code locked in, so the pressure loop never waits on the binary being read
// back from disk, and the OOM killer told to take others first. That is the
// VM's daemon only: on a machine of the user's own, its memory is theirs.
//
// The lock is on top of the memory.min of the scope it runs in
// (vmprotect.go), which a daemon that couldn't move there lacks. Only the
// executable's mappings (lockMemory): a locked heap can never be given back.
func (s *Server) protectDaemon() {
	if !hostos.InVM() {
		return
	}
	pid := strconv.Itoa(os.Getpid())
	if err := agent.Sudo("prlimit", "", "--pid", pid, "--memlock=unlimited:unlimited"); err != nil {
		s.logf("memory pressure: the daemon may not lock its memory in: %v", err)
	} else if err := lockMemory(); err != nil {
		s.logf("memory pressure: locking the daemon's memory in: %v", err)
	}
	if err := agent.Sudo("tee", strconv.Itoa(oomScoreAdj), "/proc/"+pid+"/oom_score_adj"); err != nil {
		s.logf("memory pressure: sparing the daemon from the OOM killer: %v", err)
	}
}

// refreshSoon has the agents refreshed aside, at once, so the app shows what
// changed without the loop waiting on Incus.
func (s *Server) refreshSoon() {
	select {
	case s.heavy.refresh <- struct{}{}:
	default:
	}
}

// stepPressure reads the VM's memory: for the fleet, with pausing whole
// agents on, which decides from it; for how short it is, which GET
// /v1/pressure shows; and for what a restart carries on to wait to settle.
// It also lets the next machine in line boot (admission.go), and forgets the
// runs that ended.
func (s *Server) stepPressure(ctx context.Context) {
	sample, err := s.readPressure()
	h := s.heavy
	h.mu.Lock()
	if err != nil {
		// No PSI (not Linux, or a kernel without it): memory is never short
		// to the loop, which is what memory pressure of 0 makes of it.
		if msg := err.Error(); msg != h.psiErr {
			h.psiErr = msg
			s.logf("memory pressure: %v; taken as none", err)
		}
		sample = pressure.Sample{}
	}
	now := time.Now()
	sample.HostBusy = s.hostLoad.busy(now)
	sample.Mem = s.hostLoad.withCeiling(sample.Mem, now)
	s.watchPSI(now, sample.PSI)
	gone := h.g.Expire(func(r *pressure.Run) bool {
		if r.Placed {
			return s.runCgroups.Populated(h.instance[r.ID()], r.Key)
		}
		return now.Before(r.Until)
	})
	h.g.Observe(now, sample)
	trend, why := h.g.Trend(), h.g.Why()
	h.reading, h.trend, h.readAt = sample, trend, now
	type ended struct {
		run      pressure.Run
		instance string
	}
	var ends []ended
	for _, r := range gone {
		ends = append(ends, ended{*r, h.instance[r.ID()]})
		h.forget(r)
	}
	h.mu.Unlock()

	// Removing a cgroup may take sudo: aside.
	for _, c := range ends {
		if c.run.Placed {
			go func(c ended) {
				if err := s.runCgroups.Remove(c.instance, c.run.Key); err != nil {
					s.logf("memory pressure: %s: removing its cgroup: %v", c.run.ID(), err)
				}
			}(c)
		}
	}
	if len(ends) > 0 {
		s.refreshSoon()
	}
	if s.fleetOn() {
		s.stepFleet(ctx, now, sample)
		why = s.fleetWhyNow()
	} else {
		s.stepAdmission(now)
	}
	s.notePressure(now, sample, err == nil, why)
}

// forget drops what the daemon kept beside a run that went. Under mu.
func (h *heavyRuns) forget(r *pressure.Run) {
	id := r.ID()
	delete(h.instance, id)
	delete(h.told, id)
}

// tellPaused tells an agent, once a run, that pausing whole agents
// (agentfreeze.go) paused one of its commands for memory.
func (s *Server) tellPaused(ctx context.Context, r pressure.Run) {
	s.heavy.mu.Lock()
	already := s.heavy.told[r.ID()]
	s.heavy.told[r.ID()] = true
	s.heavy.mu.Unlock()
	if already {
		return
	}
	a, err := s.agentByRef(ctx, r.Agent)
	if err != nil {
		return
	}
	what := "A command of yours"
	if c := strings.TrimSpace(r.Command); c != "" {
		what = fmt.Sprintf("Your command `%s`", headWords(c, 120))
	}
	text := what + " was paused for memory: the VM's memory is under pressure from several agents' tests and builds at once. " +
		"It resumes by itself once that eases, and loses nothing. If it timed out meanwhile, run it again. " +
		"Running fewer packages or workers at once (go test -p 2, vitest --maxWorkers=2, make -j2) gets it through sooner."
	if _, err := s.tellUnlessRetired(ctx, a, text); err != nil {
		s.logf("memory pressure: telling %s its command was paused: %v", r.Agent, err)
	}
	s.captureEvent(ctx, a.Project, a.Name, "memory_paused", map[string]any{"command": r.Command}, "")
}

// agentByRef is the agent a ref names.
func (s *Server) agentByRef(ctx context.Context, ref string) (state.Agent, error) {
	project, name, ok := strings.Cut(ref, "/")
	if !ok {
		return state.Agent{}, fmt.Errorf("%q isn't project/agent", ref)
	}
	return s.store.Agent(ctx, project, name)
}

// startHeavy is POST /v1/self/heavy: a heavy command of the calling agent's
// starting, or asking again while it runs. It starts at once: nothing waits
// for memory (api.HeavyRequest.WaitSeconds is a daemon before this one's),
// with as many workers as its share of the VM's cores among the heavy
// commands running now, in every agent (agent.HeavyWorkers).
func (s *Server) startHeavy(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req api.HeavyRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		key := strings.TrimSpace(req.Key)
		if key == "" {
			return fmt.Errorf("a heavy command needs a key naming it")
		}
		ttl := heavyTTL
		if req.TTLSeconds > 0 {
			ttl = min(time.Duration(req.TTLSeconds)*time.Second, heavyMaxTTL)
		}
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		s.wakeIdle(a.Ref())
		running := s.startRun(a.Ref(), instance, key, req.Command, ttl)
		workers := agent.HeavyWorkers(s.vmCores(), running, s.hostLoad.busy(time.Now()))
		return writeJSON(w, http.StatusOK, api.HeavyStart{Started: true, Workers: workers})
	}
}

// startRun has the heavy command key of agent ref, in instance, start now,
// or renews it for ttl, and reports how many heavy commands run now, in
// every agent, this one counted: one paused by pausing whole agents
// (agentfreeze.go) uses no CPU, and isn't.
func (s *Server) startRun(ref, instance, key, command string, ttl time.Duration) (running int) {
	h := s.heavy
	h.mu.Lock()
	run := h.g.Ask(ref, key, command, ttl, time.Now())
	fresh := h.instance[run.ID()] == ""
	h.instance[run.ID()] = instance
	for _, r := range h.g.Running() {
		if !r.Paused {
			running++
		}
	}
	h.mu.Unlock()
	if fresh {
		s.refreshSoon()
	}
	return running
}

// reasonWords says why the fleet holds machines or runs, a clause for the
// agents and the log; "" for nothing.
func reasonWords(why pressure.Reason, s pressure.Sample, tr pressure.Trend) string {
	avail := fmt.Sprintf("%s of %s available", agent.HumanBytes(s.Mem.Available), agent.HumanBytes(s.Mem.Total))
	switch why {
	case pressure.Stalling:
		return fmt.Sprintf("the VM's memory is under pressure (%.0f%% of the last 10 seconds stalled on it)", s.Some.Avg10)
	case pressure.Falling:
		return fmt.Sprintf("the VM's memory is running out fast (%s, falling %s a second, swap full)", avail, agent.HumanBytes(int64(tr.Fall)))
	case pressure.Churning:
		return fmt.Sprintf("the VM's memory doesn't hold what is being used (%s a second coming back from swap, %s)", agent.HumanBytes(int64(tr.Churn)), avail)
	case pressure.Low:
		return fmt.Sprintf("the VM is nearly out of memory (%s, swap full)", avail)
	case pressure.Easing:
		return "the VM's memory has eased, and commands paused for it resume first, one at a time"
	case pressure.HostBusy:
		return "the user's computer, outside the VM, is busy, and its own programs come first"
	}
	return ""
}

// joinHeavy is POST /v1/self/heavy/{key}/join: a process of the agent's, and
// what it starts after, into a started run's cgroup.
func (s *Server) joinHeavy(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req api.HeavyJoin
		if err := readJSON(r, &req); err != nil {
			return err
		}
		if req.PID <= 0 {
			return fmt.Errorf("say which process joins it")
		}
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		key := r.PathValue("key")
		h := s.heavy
		h.mu.Lock()
		run := h.g.Get(a.Ref(), key)
		h.mu.Unlock()
		if run == nil {
			return fmt.Errorf("%s hasn't started", key)
		}
		if err := s.runCgroups.Place(instance, key, req.PID); err != nil {
			s.logf("memory pressure: %s/%s: joining its cgroup: %v", a.Ref(), key, err)
			return err
		}
		h.mu.Lock()
		h.g.Place(a.Ref(), key)
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

// endHeavy is DELETE /v1/self/heavy/{key}, with how the command ended
// (api.HeavyEnd), which is kept as its timing (timing.go).
func (s *Server) endHeavy(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var end api.HeavyEnd
		if err := readJSON(r, &end); err != nil {
			return err
		}
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		var ended *pressure.Run
		s.endRuns(func(g *pressure.Runs) []*pressure.Run {
			if run := g.End(a.Ref(), r.PathValue("key")); run != nil {
				ended = run
				return []*pressure.Run{run}
			}
			return nil
		})
		s.recordCommand(a, end, ended, time.Now())
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

// dropHeavy ends every run of an agent, as it stops or goes.
func (s *Server) dropHeavy(ref string) {
	s.endRuns(func(g *pressure.Runs) []*pressure.Run { return g.Drop(ref) })
}

// endRuns ends the runs end takes out: their cgroups removed.
func (s *Server) endRuns(end func(*pressure.Runs) []*pressure.Run) {
	h := s.heavy
	h.mu.Lock()
	gone := end(h.g)
	instances := make([]string, len(gone))
	for i, r := range gone {
		instances[i] = h.instance[r.ID()]
		h.forget(r)
	}
	h.mu.Unlock()
	for i, r := range gone {
		if r.Placed {
			if err := s.runCgroups.Remove(instances[i], r.Key); err != nil {
				s.logf("memory pressure: %s: removing its cgroup: %v", r.ID(), err)
			}
		}
	}
	if len(gone) > 0 {
		s.refreshSoon()
	}
}

// memoryHold is what memory holds of an agent, nil for nothing: what
// admission holds of its machine's boot (admission.go), or, with pausing
// whole agents on, what the fleet pauses of it, its machine or its heavy
// commands. Nothing else waits or is paused for memory.
func (s *Server) memoryHold(ref string) *api.MemoryHold {
	if held := s.machineHold(ref); held != nil {
		return held
	}
	h := s.heavy
	h.mu.Lock()
	defer h.mu.Unlock()
	var out api.MemoryHold
	for _, r := range h.g.Running() {
		if r.Agent == ref && r.Paused {
			out.Paused++
			if out.Since.IsZero() || r.PausedAt.Before(out.Since) {
				out.Since = r.PausedAt
			}
		}
	}
	if out.Paused == 0 {
		return nil
	}
	// Only the fleet pauses a run: what holds it is what the fleet saw.
	s.fleetWhy(&out)
	return &out
}

// getPressure is GET /v1/pressure.
func (s *Server) getPressure(w http.ResponseWriter, r *http.Request) error {
	h := s.heavy
	h.mu.Lock()
	psi := h.g.Last()
	out := api.PressureStatus{Some: psi.Some.Avg10, Full: psi.Full.Avg10, Why: string(h.g.Why()), Runs: []api.HeavyRun{}}
	if load, ok := s.hostLoad.current(time.Now()); ok {
		out.Host = &load
	}
	for _, run := range h.g.Running() {
		st, since := "running", run.Started
		if run.Paused {
			st, since = "paused", run.PausedAt
		}
		out.Runs = append(out.Runs, api.HeavyRun{Agent: run.Agent, Key: run.Key, Command: run.Command, State: st, Since: since, Placed: run.Placed})
	}
	h.mu.Unlock()
	if use, ok := s.agentsUse(); ok {
		out.Agents = &use
	}
	return writeJSON(w, http.StatusOK, out)
}

// watchMemory keeps every running agent's memory.max where the VM's size
// puts it, with its io.weight and its other caps (agent.Caps), and tells an agent at once when a process of its was killed for
// it.
func (s *Server) watchMemory(ctx context.Context) {
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		return
	}
	s.syncFleet(ctx, agents)
	share := s.agentMemoryShare(ctx)
	total := s.vmMemory()
	agentsHit, agentsLimit := s.watchShared(s.vmMemoryNow(), s.reachableMemory(total, time.Now()))
	limit := agent.MemoryLimit(total, share)
	caps := agent.AgentCaps(limit, s.vmSwap(), share)
	// The run slots first: who holds one decides each machine's memory.high.
	s.stepRunSlots(ctx, agents, time.Now())
	for _, a := range agents {
		if a.IsLead() || a.Instance == "" {
			continue
		}
		// One being made runs its machine too — the old home pushed in,
		// caches filled, Docker set up — and several made at once, with no
		// limit, could take the whole VM: limited as soon as its cgroup is
		// there (it isn't before its machine starts, and nothing is written).
		if a.Status != state.AgentReady && a.Status != state.AgentCreating {
			continue
		}
		if err := s.setMemoryLimit(a.Instance, limit); err != nil {
			s.logOnce("memory.max "+a.Instance, fmt.Sprintf("memory.max: %s: %v", a.Ref(), err))
		}
		// Squeezed in its code phase, not in its run phase (runslots.go).
		if err := s.setMemoryHigh(a.Instance, s.codePhaseHigh(a.Ref(), limit)); err != nil {
			s.logOnce("memory.high "+a.Instance, fmt.Sprintf("memory.high: %s: %v", a.Ref(), err))
		}
		if err := s.setIOWeight(a.Instance, agent.AgentIOWeight); err != nil {
			s.logOnce("io.weight "+a.Instance, fmt.Sprintf("io.weight: %s: %v", a.Ref(), err))
		}
		// Its swap, CPU weight and task count, at the same moment
		// (agent/caps.go).
		if err := s.setCaps(a.Instance, caps); err != nil {
			s.logOnce("caps "+a.Instance, fmt.Sprintf("swap, cpu and pids caps: %s: %v", a.Ref(), err))
		}
		if a.Status != state.AgentReady {
			continue
		}
		s.raiseOOMScore(a.Ref(), a.Instance)
		s.watchThrash(ctx, a, limit)
		now, ok := s.memoryEvents(a.Instance)
		if !ok {
			continue
		}
		s.mu.Lock()
		seen, known := s.oomSeen[a.Instance]
		s.oomSeen[a.Instance] = now
		if known && now.Kills > seen.Kills {
			s.oomAt[a.Instance] = time.Now()
		}
		s.mu.Unlock()
		// The first look at a machine counts what was killed before this
		// daemon was there to tell anyone.
		if known && now.Kills > seen.Kills {
			// A limit nested in the machine, when one reached it since; else
			// its own, when it did; else the VM ran out.
			k := killCause{limit: now.Limit, own: now.OwnLimit > seen.OwnLimit, shared: agentsHit, sharedLimit: agentsLimit, total: total, atCap: s.vmAtCap()}
			for path, n := range now.Nested {
				if n.Reached > seen.Nested[path].Reached && (k.nestedPath == "" || path < k.nestedPath) {
					k.nestedPath, k.nestedLimit = path, n.Limit
				}
			}
			s.countOOMKills(now.Kills - seen.Kills)
			go s.tellKilled(context.WithoutCancel(ctx), a, now.Kills-seen.Kills, k)
		}
	}
}

// vmAtCap is the VM's memory cap when the VM has grown all the way to it,
// by the pressure loop's last reading: raising it (agentbox vm resize
// --memory-cap) is then what gives every agent more. 0 short of it, where
// the host grows the VM or can't spare more (api.HostLoad.VMCeiling), and
// for a VM that doesn't grow.
func (s *Server) vmAtCap() int64 {
	vmCap := s.vmCap()
	if vmCap <= 0 || !(pressure.Mem{Total: s.lastMem().Total, Cap: vmCap}).AtCap() {
		return 0
	}
	return vmCap
}

// configuredCap is the VM's memory cap as its front end set it
// (agent.VMMemoryCapEnv): 0 for a VM that doesn't grow, or outside one.
func configuredCap() int64 {
	n, _ := strconv.ParseInt(os.Getenv(agent.VMMemoryCapEnv), 10, 64)
	return max(n, 0)
}

// lastMem is the VM's memory as the pressure loop last read it.
func (s *Server) lastMem() pressure.Mem {
	h := s.heavy
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.g.Last().Mem
}

// thrashFor is how long an agent stays spent — its memory and swap — and
// stalled before its largest process is killed (agent/thrash.go): three
// looks, so a build's moment at its peak isn't thrashing. In the load lab
// every second of it was the VM's disk reading 200 MB/s and the daemon's
// requests waiting seconds on it.
const thrashFor = 6 * time.Second

// thrashState is what watchThrash knows of one machine: the look it was
// first seen spent at, zero when it isn't.
type thrashState struct {
	since agent.ThrashSample
}

// watchThrash kills the largest process of an agent that has been thrashing
// at its own limit for thrashFor, and tells it, as the kernel's OOM killer
// would have, sooner.
func (s *Server) watchThrash(ctx context.Context, a state.Agent, limit int64) {
	now, ok := s.thrash(a.Instance)
	s.mu.Lock()
	if !ok {
		delete(s.thrashing, a.Instance)
		s.mu.Unlock()
		return
	}
	st := s.thrashing[a.Instance]
	switch {
	case !now.Spent:
		st.since = agent.ThrashSample{}
	case st.since.At.IsZero():
		st.since = now
	}
	kill := now.Spent && now.At.Sub(st.since.At) >= thrashFor && now.StalledShare(st.since) >= agent.ThrashShare
	if kill {
		st.since = agent.ThrashSample{}
	}
	s.thrashing[a.Instance] = st
	s.mu.Unlock()
	if !kill {
		return
	}
	v, err := s.killLargest(a.Instance)
	if err != nil {
		s.logOnce("thrash "+a.Instance, fmt.Sprintf("memory: %s is thrashing at its limit: %v", a.Ref(), err))
		return
	}
	s.countOOMKills(1)
	text := fmt.Sprintf("`%s` (pid %d, using %s) was killed for memory: your machine had used all its memory (%s) for %s, "+
		"and was reading its programs back from the disk more than running them. "+
		"Run fewer packages or workers at once (go test -p 2 ./..., vitest --maxWorkers=2, make -j2, one test file at a time) and try again.",
		v.Name, v.PID, agent.HumanBytes(v.RSS), agent.HumanBytes(limit), thrashFor)
	s.logf("memory: %s: %s", a.Ref(), text)
	go func() {
		ctx := context.WithoutCancel(ctx)
		if _, err := s.tellUnlessRetired(ctx, a, text); err != nil {
			s.logf("memory: telling %s a process was killed: %v", a.Ref(), err)
		}
		s.captureEvent(ctx, a.Project, a.Name, "oom_kill", map[string]any{
			"kills": 1, "limit": limit, "own_limit": true, "thrashing": true,
			"process": v.Name, "pid": v.PID, "rss": v.RSS,
		}, "")
	}()
}

// agentMemoryShare is the share of the VM, in percent, one agent may have
// (agent.MemoryLimit): what the settings chose, or agent.DefaultAgentMemoryShare.
func (s *Server) agentMemoryShare(ctx context.Context) int {
	if v, err := s.store.Setting(ctx, state.SettingAgentMemoryShare); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return min(max(n, agent.MinAgentMemoryShare), agent.MaxAgentMemoryShare)
		}
	}
	return agent.DefaultAgentMemoryShare
}

// setAgentMemoryShare stores the share a settings request chooses, 0 going
// back to the default, and has running agents' memory.max follow at once:
// lowering it is how a runaway agent is stopped from taking the VM.
func (s *Server) setAgentMemoryShare(ctx context.Context, share int) error {
	value := ""
	if share != 0 {
		if share < agent.MinAgentMemoryShare || share > agent.MaxAgentMemoryShare {
			return fmt.Errorf("an agent's share of the VM's memory is from %d%% to %d%%; %d%% isn't",
				agent.MinAgentMemoryShare, agent.MaxAgentMemoryShare, share)
		}
		value = strconv.Itoa(share)
	}
	if err := s.store.SetSetting(ctx, state.SettingAgentMemoryShare, value); err != nil {
		return err
	}
	if s.pressureEvery > 0 {
		go s.watchMemory(s.background())
	}
	return nil
}

// logOnce logs msg under key, unless it was the last thing logged under it.
func (s *Server) logOnce(key, msg string) {
	s.mu.Lock()
	same := s.loggedOnce[key] == msg
	s.loggedOnce[key] = msg
	s.mu.Unlock()
	if !same {
		s.logf("%s", msg)
	}
}

// killCause is whose memory limit a kill was for: a cgroup nested in the
// machine (nestedPath, from the machine's cgroup, with nestedLimit), the
// machine's own memory.max (own, limit), every agent's together (shared,
// sharedLimit, of a VM of total), or none of them, the VM's.
type killCause struct {
	limit       int64
	own         bool
	nestedPath  string
	nestedLimit int64
	shared      bool
	sharedLimit int64
	total       int64
	// atCap is the VM's memory cap when it had grown all the way to it
	// (vmAtCap), 0 otherwise.
	atCap int64
}

// tellKilled tells an agent that n of its processes were killed for memory,
// for a nested cgroup's limit, its own memory.max, or the VM's, and records it.
func (s *Server) tellKilled(ctx context.Context, a state.Agent, n int64, k killCause) {
	v, found := s.oomVictim(a.Instance)
	what := "A process in your machine was"
	if n > 1 {
		what = fmt.Sprintf("%d processes in your machine were", n)
	}
	if found {
		what = fmt.Sprintf("`%s` (pid %d, using %s) was", v.Name, v.PID, agent.HumanBytes(v.RSS))
		if n > 1 {
			what = fmt.Sprintf("%d processes in your machine were, the last `%s` (pid %d, using %s),", n, v.Name, v.PID, agent.HumanBytes(v.RSS))
		}
	}
	var why string
	switch {
	case k.nestedPath != "":
		why = fmt.Sprintf("it was in a cgroup inside your machine, /%s, whose memory limit is %s (a container started with --memory, or a systemd slice); your machine and the VM had memory to spare, so raise that limit or run it without one",
			k.nestedPath, agent.HumanBytes(k.nestedLimit))
	case k.own:
		why = fmt.Sprintf("your machine reached its limit of %s, all one agent can have in this VM", agent.HumanBytes(k.limit))
	case k.shared:
		why = fmt.Sprintf("every agent's machine together reached the limit they share, %s", agent.HumanBytes(k.sharedLimit))
		if k.total > 0 {
			why += fmt.Sprintf(" of this VM's %s", agent.HumanBytes(k.total))
		}
		why += ", which keeps the rest for AgentBox and the project chats; your machine was under its own"
	default:
		why = "the VM ran out of memory, with every agent's work together"
	}
	if k.atCap > 0 && k.nestedPath == "" && !k.own {
		// More for every agent comes from a bigger VM.
		why += fmt.Sprintf("; the VM is at its %s memory cap, which the user can raise with `agentbox vm resize --memory-cap` on their computer", agent.HumanBytes(k.atCap))
	}
	text := fmt.Sprintf("%s killed for memory: %s. "+
		"Run fewer packages or workers at once (go test -p 2 ./..., vitest --maxWorkers=2, make -j2, one test file at a time) and try again.",
		what, why)
	s.logf("memory: %s: %s", a.Ref(), text)
	if _, err := s.tellUnlessRetired(ctx, a, text); err != nil {
		s.logf("memory: telling %s a process was killed: %v", a.Ref(), err)
	}
	cause := "vm"
	switch {
	case k.nestedPath != "":
		cause = "nested"
	case k.own:
		cause = "own"
	case k.shared:
		cause = "agents"
	}
	payload := map[string]any{"kills": n, "limit": k.limit, "own_limit": k.own && k.nestedPath == "", "cause": cause}
	if k.nestedPath != "" {
		payload["cgroup"], payload["cgroup_limit"] = k.nestedPath, k.nestedLimit
	}
	if cause == "agents" {
		payload["shared_limit"] = k.sharedLimit
	}
	if found {
		payload["process"], payload["pid"], payload["rss"] = v.Name, v.PID, v.RSS
	}
	s.captureEvent(ctx, a.Project, a.Name, "oom_kill", payload, "")
}
