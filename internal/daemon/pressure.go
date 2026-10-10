package daemon

import (
	"context"
	"fmt"
	"net/http"
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

const (
	// pressureInterval is how often the VM's pressure is read, and the
	// agents' memory limits and kills looked at.
	pressureInterval = 2 * time.Second
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
}

func newHeavyRuns() *heavyRuns {
	return &heavyRuns{
		g:        pressure.NewGovernor(pressure.DefaultPolicy),
		started:  map[string]chan struct{}{},
		instance: map[string]string{},
		told:     map[string]bool{},
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

// runPressure is the pressure loop.
func (s *Server) runPressure(ctx context.Context) {
	if s.pressureEvery <= 0 {
		return // a test steps it itself
	}
	ticker := time.NewTicker(s.pressureEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.stepPressure(ctx)
			s.watchMemory(ctx)
		}
	}
}

// stepPressure reads the VM's pressure and applies what the governor makes
// of it: runs that ended go, waiting ones start, going ones pause or resume.
func (s *Server) stepPressure(ctx context.Context) {
	psi, err := s.readPressure()
	h := s.heavy
	h.mu.Lock()
	if err != nil {
		// No PSI (not Linux, or a kernel without it): nothing is ever held,
		// which is what memory pressure of 0 makes of it.
		if msg := err.Error(); msg != h.psiErr {
			h.psiErr = msg
			s.logf("memory pressure: %v; heavy commands run without waiting", err)
		}
		psi = pressure.PSI{}
	}
	now := time.Now()
	gone := h.g.Expire(now, func(r *pressure.Run) bool {
		if r.Placed {
			return s.runCgroups.Populated(h.instance[r.ID()], r.Key)
		}
		return now.Before(r.Until)
	})
	acts := h.g.Step(now, psi)
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

	for _, c := range ended {
		if c.run.Placed {
			if err := s.runCgroups.Remove(c.instance, c.run.Key); err != nil {
				s.logf("memory pressure: %s: removing its cgroup: %v", c.run.ID(), err)
			}
		}
	}
	for _, c := range resumed {
		s.logf("memory pressure: resuming %s (%s)", c.run.ID(), c.run.Command)
		if err := s.runCgroups.Freeze(c.instance, c.run.Key, false); err != nil {
			s.logf("memory pressure: resuming %s: %v", c.run.ID(), err)
		}
	}
	for _, c := range paused {
		s.logf("memory pressure: pausing %s (%s): full pressure %.0f%%", c.run.ID(), c.run.Command, psi.Full.Avg10)
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
		s.refreshAgents(ctx)
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
	if _, err := s.tellAgent(ctx, a, text); err != nil {
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
	psi := h.g.Last()
	why := fmt.Sprintf("the VM's memory is under pressure (%.0f%% of the last 10 seconds stalled on it)", psi.Some.Avg10)
	if paused > 0 {
		why += fmt.Sprintf(", %d paused %s resume first", paused, plural(paused, "command is to", "commands are to"))
	}
	if ahead > 0 {
		why += fmt.Sprintf(", and %d %s ahead of it", ahead, plural(ahead, "command is", "commands are"))
	}
	return why
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
	return &out
}

// getPressure is GET /v1/pressure.
func (s *Server) getPressure(w http.ResponseWriter, r *http.Request) error {
	h := s.heavy
	h.mu.Lock()
	psi := h.g.Last()
	out := api.PressureStatus{Some: psi.Some.Avg10, Full: psi.Full.Avg10, Runs: []api.HeavyRun{}}
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
	if _, err := s.tellAgent(ctx, a, text); err != nil {
		s.logf("memory: telling %s a process was killed: %v", a.Ref(), err)
	}
	payload := map[string]any{"kills": n, "limit": limit, "own_limit": own}
	if found {
		payload["process"], payload["pid"], payload["rss"] = v.Name, v.PID, v.RSS
	}
	s.captureEvent(ctx, a.Project, a.Name, "oom_kill", payload, "")
}
