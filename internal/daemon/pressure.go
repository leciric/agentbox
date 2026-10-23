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

// Memory goes by what the VM measures, not by guesses. Every agent starts at
// once. Each has a hard memory.max (agent.MemoryLimit), so a process that
// outgrows what one agent can have alone is killed at once, and the agent is
// told what was killed and at what size (watchMemory). What memory pressure
// holds back is heavy commands (test runs, builds): they ask the daemon
// before they start, wait while the VM's PSI says memory is short, and the
// newest of those already going is paused when it stays short, then resumed
// in order (package pressure decides; this applies it).
//
// Inside an agent a heavy command asks through the in-agent API: Claude
// Code's hook around its Bash tool (cli/heavy.go, agent.withHeavyHooks), or
// `agentbox heavy -- <cmd>` around anything else. As it starts, its shell
// joins a cgroup of its own under the agent's machine's (agent.PlaceRun), so
// pausing it freezes that command and what it started, not the agent, and
// its memory is pushed out to swap while it is paused.
//
// The loop that reads the VM's memory and pauses runs is what must still
// work when the VM has none to spare: a reading allocates nothing and opens
// nothing (pressure.Sampler), the loop does nothing else (the agents' limits
// and kills are watched on a loop of their own, and the app told aside, as
// that asks Incus), a run's freezer is written directly rather than through
// sudo (agent.PlaceRun hands its cgroup to the daemon's user), and in the VM
// the daemon's own memory is locked in and the OOM killer told to spare it
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
	// heavyMaxWait is the longest one request waits to start.
	heavyMaxWait = 10 * time.Minute
)

// runCgroups is what applying the governor's decisions does to the runs'
// cgroups: agent's functions, or a test's.
type runCgroups interface {
	Place(instance, key string, pid int) error
	Freeze(instance, key string, on bool) error
	Reclaim(instance, key string) error
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
func (agentRunCgroups) Reclaim(instance, key string) error { return agent.ReclaimRun(instance, key) }
func (agentRunCgroups) Populated(instance, key string) bool {
	return agent.RunPopulated(instance, key)
}
func (agentRunCgroups) Remove(instance, key string) error { return agent.RemoveRun(instance, key) }

// heavyRuns is the governor and what the daemon keeps beside it, under mu.
type heavyRuns struct {
	mu sync.Mutex
	g  *pressure.Governor
	// started is closed as a waiting run starts, by run ID.
	started map[string]chan struct{}
	// instance is each run's agent's machine, by run ID.
	instance map[string]string
	// told are the runs whose agents were told they were paused, by run ID.
	told map[string]bool
	// psiErr is the last error reading the VM's pressure, logged once.
	psiErr string
	// refresh asks for the agents to be refreshed (refreshAgents), aside
	// from the loop.
	refresh chan struct{}
}

func newHeavyRuns() *heavyRuns {
	return &heavyRuns{
		g:        pressure.NewGovernor(pressure.DefaultPolicy),
		started:  map[string]chan struct{}{},
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
		}
		mu.Unlock()
		return s.Read()
	}
}

// giveZram gives AgentBox's VM its compressed swap in memory as the daemon
// starts (agent.EnsureZram): without swap the kernel kills before memory
// pressure shows, and a paused run's memory has nowhere to go. Outside the
// VM (an earlier release's host install) the machine's swap is its owner's.
func (s *Server) giveZram() {
	if !hostos.InVM() {
		return
	}
	did, err := agent.EnsureZram(s.vmMemory() / agent.ZramShare)
	if err != nil {
		s.logf("zram swap: %v; memory pressure shows only once the VM has swap (agentbox vm swap on)", err)
		return
	}
	s.logf("zram swap: %s", did)
}

// runPressure is the pressure loop, and the loops beside it that may wait on
// SQLite, Incus or sudo: refreshing the agents as runs change, and watching
// their memory limits and kills.
func (s *Server) runPressure(ctx context.Context) {
	s.loadFleet(ctx)
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
// memory locked in, so the pressure loop never waits on a page the kernel
// pushed out, and the OOM killer told to take others first. That is the
// VM's daemon only: on a machine of the user's own, its memory is theirs.
//
// mlockall, not a memory.min: the daemon runs in its ssh session's cgroup,
// and a memory.min there means nothing unless every cgroup above it has one,
// while moving the daemon into one of its own would take the lead chats it
// starts with it, which a lock doesn't (children don't inherit it). Locked
// as the pages are first touched (MCL_ONFAULT), so what Go reserves and
// never uses costs nothing, after locking all that is mapped now: the
// binary's code, the pause included, never has to be read back from disk.
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

// stepPressure reads the VM's pressure and applies what the governor makes
// of it: runs that ended go, waiting ones start, going ones pause or resume.
func (s *Server) stepPressure(ctx context.Context) {
	sample, err := s.readPressure()
	h := s.heavy
	h.mu.Lock()
	if err != nil {
		// No PSI (not Linux, or a kernel without it): nothing is ever held,
		// which is what memory pressure of 0 makes of it.
		if msg := err.Error(); msg != h.psiErr {
			h.psiErr = msg
			s.logf("memory pressure: %v; heavy commands run without waiting", err)
		}
		sample = pressure.Sample{}
	}
	now := time.Now()
	s.watchPSI(now, sample.PSI)
	gone := h.g.Expire(now, func(r *pressure.Run) bool {
		if r.Placed {
			return s.runCgroups.Populated(h.instance[r.ID()], r.Key)
		}
		return now.Before(r.Until)
	})
	// Pausing whole agents (agentfreeze.go) holds their machines instead:
	// every run starts at once, and none is paused on its own.
	fleetOn := s.fleetOn()
	var acts pressure.Actions
	if fleetOn {
		acts = h.g.StartAll(now)
	} else {
		acts = h.g.Step(now, sample)
	}
	trend := h.g.Trend()
	type change struct {
		run      pressure.Run
		instance string
	}
	var ended, paused, resumed []change
	for _, r := range gone {
		ended = append(ended, change{*r, h.instance[r.ID()]})
		h.forget(r)
	}
	for _, r := range acts.Start {
		if ch := h.started[r.ID()]; ch != nil {
			close(ch)
			delete(h.started, r.ID())
		}
	}
	for _, r := range acts.Pause {
		paused = append(paused, change{*r, h.instance[r.ID()]})
	}
	for _, r := range acts.Resume {
		resumed = append(resumed, change{*r, h.instance[r.ID()]})
	}
	h.mu.Unlock()

	// Removing a cgroup may take sudo: aside.
	for _, c := range ended {
		if c.run.Placed {
			go func(c change) {
				if err := s.runCgroups.Remove(c.instance, c.run.Key); err != nil {
					s.logf("memory pressure: %s: removing its cgroup: %v", c.run.ID(), err)
				}
			}(c)
		}
	}
	for _, c := range resumed {
		s.logf("memory pressure: resuming %s (%s)", c.run.ID(), c.run.Command)
		if err := s.runCgroups.Freeze(c.instance, c.run.Key, false); err != nil {
			s.logf("memory pressure: resuming %s: %v", c.run.ID(), err)
		}
	}
	for _, c := range paused {
		s.logf("memory pressure: pausing %s (%s): %s: %s", c.run.ID(), c.run.Command, reasonWords(c.run.Why, sample, trend), sample)
		if err := s.runCgroups.Freeze(c.instance, c.run.Key, true); err != nil {
			s.logf("memory pressure: pausing %s: %v", c.run.ID(), err)
			continue
		}
		// Out to swap, so the paused run doesn't hold the VM's memory. It
		// takes a while for gigabytes; nothing waits for it.
		go func(c change) {
			if err := s.runCgroups.Reclaim(c.instance, c.run.Key); err != nil {
				s.logf("memory pressure: pushing %s's memory out: %v", c.run.ID(), err)
			}
		}(c)
		// Told aside, so a chat slow to take it doesn't hold up the loop.
		go s.tellPaused(context.WithoutCancel(ctx), c.run)
	}
	if len(ended)+len(paused)+len(resumed)+len(acts.Start) > 0 {
		s.refreshSoon()
	}
	if fleetOn {
		s.stepFleet(ctx, now, sample)
	}
}

// forget drops what the daemon kept beside a run that went. Under mu.
func (h *heavyRuns) forget(r *pressure.Run) {
	id := r.ID()
	if ch := h.started[id]; ch != nil {
		close(ch) // a waiter still asking hears it went; it reads the governor again
		delete(h.started, id)
	}
	delete(h.instance, id)
	delete(h.told, id)
}

// tellPaused tells an agent, once a run, that one of its commands was paused
// for memory.
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
		"It resumes by itself, in turn, once that eases, and loses nothing. If it timed out meanwhile, run it again: it waits its turn. " +
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
// asking to start, answered once it may, or when the request's wait runs out.
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
		wait := heavyMaxWait
		if req.WaitSeconds > 0 {
			wait = min(time.Duration(req.WaitSeconds)*time.Second, heavyMaxWait)
		}
		ctx := r.Context()
		a, err := s.store.AgentByInstance(ctx, instance)
		if err != nil {
			return err
		}
		s.wakeIdle(a.Ref())
		out, err := s.askHeavy(ctx, a.Ref(), instance, key, req.Command, ttl, wait)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) askHeavy(ctx context.Context, ref, instance, key, command string, ttl, wait time.Duration) (api.HeavyStart, error) {
	h := s.heavy
	h.mu.Lock()
	run, started := h.g.Ask(ref, key, command, ttl, time.Now())
	id := run.ID()
	h.instance[id] = instance
	if !started && s.fleetOn() {
		// Its machine is what pauses, not the run: it starts now.
		h.g.StartAll(time.Now())
		started = true
	}
	if started {
		h.mu.Unlock()
		return api.HeavyStart{Started: true}, nil
	}
	ch := h.started[id]
	asked := ch == nil
	if asked {
		ch = make(chan struct{})
		h.started[id] = ch
	}
	h.mu.Unlock()
	// Calm and nothing ahead of it: this step starts it now.
	s.stepPressure(ctx)
	if asked {
		// Still waiting, it shows on the agent ("Waiting for memory").
		select {
		case <-ch:
		default:
			s.refreshAgents(ctx)
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	keep := time.NewTicker(pressure.DefaultPolicy.Grace / 3)
	defer keep.Stop()
	for {
		select {
		case <-ch:
			h.mu.Lock()
			r := h.g.Get(ref, key)
			h.mu.Unlock()
			if r == nil || r.Started.IsZero() {
				return api.HeavyStart{Why: "it was dropped while it waited: its agent stopped"}, nil
			}
			return api.HeavyStart{Started: true}, nil
		case <-keep.C:
			h.mu.Lock()
			h.g.Keep(ref, key, ttl, time.Now())
			h.mu.Unlock()
		case <-timer.C:
			return api.HeavyStart{Why: s.heavyWhy(ref, key)}, nil
		case <-ctx.Done():
			return api.HeavyStart{}, ctx.Err()
		}
	}
}

// heavyWhy says why a run waits: a clause its asker words its line around.
func (s *Server) heavyWhy(ref, key string) string {
	h := s.heavy
	h.mu.Lock()
	defer h.mu.Unlock()
	ahead, paused := 0, 0
	for _, r := range h.g.Waiting() {
		if r.Agent == ref && r.Key == key {
			break
		}
		ahead++
	}
	for _, r := range h.g.Running() {
		if r.Paused {
			paused++
		}
	}
	why := reasonWords(h.g.Why(), h.g.Last(), h.g.Trend())
	if why == "" {
		why = "other commands started just before it"
	}
	if paused > 0 && h.g.Why() != pressure.Easing {
		why += fmt.Sprintf(", %d paused %s resume first", paused, plural(paused, "command is to", "commands are to"))
	}
	if ahead > 0 {
		why += fmt.Sprintf(", and %d %s ahead of it", ahead, plural(ahead, "command is", "commands are"))
	}
	return why
}

// reasonWords says why runs are held, a clause for the agents and the log;
// "" for nothing.
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
		started := run != nil && !run.Started.IsZero()
		h.mu.Unlock()
		if !started {
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

// endHeavy is DELETE /v1/self/heavy/{key}.
func (s *Server) endHeavy(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		s.endRuns(r.Context(), func(g *pressure.Governor) []*pressure.Run {
			if run := g.End(a.Ref(), r.PathValue("key")); run != nil {
				return []*pressure.Run{run}
			}
			return nil
		})
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

// dropHeavy ends every run of an agent, as it stops or goes.
func (s *Server) dropHeavy(ctx context.Context, ref string) {
	s.endRuns(ctx, func(g *pressure.Governor) []*pressure.Run { return g.Drop(ref) })
}

// endRuns ends the runs end takes out of the governor: their cgroups resumed
// and removed, and the next step taken at once, which resumes or starts what
// they leave room for.
func (s *Server) endRuns(ctx context.Context, end func(*pressure.Governor) []*pressure.Run) {
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
		s.stepPressure(ctx)
	}
}

// memoryHold is what the daemon holds of an agent's heavy commands, nil for
// nothing.
func (s *Server) memoryHold(ref string) *api.MemoryHold {
	if held := s.machineHold(ref); held != nil {
		return held
	}
	h := s.heavy
	h.mu.Lock()
	defer h.mu.Unlock()
	var out api.MemoryHold
	note := func(at time.Time) {
		if out.Since.IsZero() || at.Before(out.Since) {
			out.Since = at
		}
	}
	for _, r := range h.g.Running() {
		if r.Agent == ref && r.Paused {
			out.Paused++
			note(r.PausedAt)
		}
	}
	for _, r := range h.g.Waiting() {
		if r.Agent == ref {
			out.Waiting++
			note(r.Asked)
		}
	}
	if out.Paused+out.Waiting == 0 {
		return nil
	}
	last, tr := h.g.Last(), h.g.Trend()
	out.Why = string(h.g.Why())
	out.Available, out.Total = last.Mem.Available, last.Mem.Total
	switch h.g.Why() {
	case pressure.Stalling:
		out.Stalled = last.Some.Avg10
	case pressure.Falling:
		out.Rate = int64(tr.Fall)
	case pressure.Churning:
		out.Rate = int64(tr.Churn)
	}
	return &out
}

// getPressure is GET /v1/pressure.
func (s *Server) getPressure(w http.ResponseWriter, r *http.Request) error {
	h := s.heavy
	h.mu.Lock()
	psi := h.g.Last()
	out := api.PressureStatus{Some: psi.Some.Avg10, Full: psi.Full.Avg10, Why: string(h.g.Why()), Runs: []api.HeavyRun{}}
	for _, run := range h.g.Running() {
		st, since := "running", run.Started
		if run.Paused {
			st, since = "paused", run.PausedAt
		}
		out.Runs = append(out.Runs, api.HeavyRun{Agent: run.Agent, Key: run.Key, Command: run.Command, State: st, Since: since, Placed: run.Placed})
	}
	for _, run := range h.g.Waiting() {
		out.Runs = append(out.Runs, api.HeavyRun{Agent: run.Agent, Key: run.Key, Command: run.Command, State: "waiting", Since: run.Asked})
	}
	h.mu.Unlock()
	return writeJSON(w, http.StatusOK, out)
}

// watchMemory keeps every running agent's memory.max where the VM's size
// puts it, and tells an agent at once when a process of its was killed for
// it.
func (s *Server) watchMemory(ctx context.Context) {
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		return
	}
	s.syncFleet(ctx, agents)
	limit := agent.MemoryLimit(s.vmMemory())
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady || a.Instance == "" {
			continue
		}
		if err := s.setMemoryLimit(a.Instance, limit); err != nil {
			s.logOnce("memory.max "+a.Instance, fmt.Sprintf("memory.max: %s: %v", a.Ref(), err))
		}
		now, ok := s.memoryEvents(a.Instance)
		if !ok {
			continue
		}
		s.mu.Lock()
		seen, known := s.oomSeen[a.Instance]
		s.oomSeen[a.Instance] = now
		s.mu.Unlock()
		// The first look at a machine counts what was killed before this
		// daemon was there to tell anyone.
		if known && now.Kills > seen.Kills {
			// Its own limit, when it reached it since; else the VM ran out.
			own := now.OwnLimit > seen.OwnLimit
			go s.tellKilled(context.WithoutCancel(ctx), a, now.Kills-seen.Kills, now.Limit, own)
		}
	}
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

// tellKilled tells an agent that n of its processes were killed for memory,
// for its own memory.max, limit, or for the VM's, and records it.
func (s *Server) tellKilled(ctx context.Context, a state.Agent, n, limit int64, own bool) {
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
	why := fmt.Sprintf("your machine reached its limit of %s, all one agent can have in this VM", agent.HumanBytes(limit))
	if !own {
		why = "the VM ran out of memory, with every agent's work together"
	}
	text := fmt.Sprintf("%s killed for memory: %s. "+
		"Run fewer packages or workers at once (go test -p 2 ./..., vitest --maxWorkers=2, make -j2, one test file at a time) and try again.",
		what, why)
	s.logf("memory: %s: %s", a.Ref(), text)
	if _, err := s.tellUnlessRetired(ctx, a, text); err != nil {
		s.logf("memory: telling %s a process was killed: %v", a.Ref(), err)
	}
	payload := map[string]any{"kills": n, "limit": limit, "own_limit": own}
	if found {
		payload["process"], payload["pid"], payload["rss"] = v.Name, v.PID, v.RSS
	}
	s.captureEvent(ctx, a.Project, a.Name, "oom_kill", payload, "")
}
