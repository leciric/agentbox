package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/runslots"
	"agentbox/internal/state"
)

// Run slots (20 Nov 2026, the user's decision; package runslots): an agent's
// code phase (editing, research, targeted tests and builds) never waits, and
// its run phase (the app, docker compose, Playwright, its desktop, browser or
// emulator, recordings) takes a run slot, waiting in a first-come,
// first-served queue while none is free.
//
//   - Counting: every 2 seconds, with the agents' memory limits (watchMemory),
//     the pool is what the agents together may use before the kernel
//     throttles them (agent.AgentsLimits' memory.high: the VM less what the
//     daemon, Incus and the lead chats keep), what agents holding no slot use
//     now comes off it (memory.current less page cache, agent.MachineUse),
//     and each slot held reserves its project's learned run peak, or what
//     its machine uses if that is more. A run is granted when its project's
//     peak fits in what is left; with no slot held, one always is.
//   - Learning: a run's peak is the most its machine used while it held the
//     slot, sampled every 2 seconds. Not the cgroup's memory.peak: that is
//     since the machine started, the daemon may not reset it (the file is
//     root's and a reset holds only for the file descriptor that wrote it),
//     and it counts page cache, which a run that pulled images or read the
//     repository fills and the kernel gives back, as agentMemory leaves out.
//   - Asking: `agentbox run up` (POST /v1/self/run/up) waits up to 30
//     seconds at a time and asks again, which keeps its place; a shimmed
//     dev server, Playwright or `docker compose up`, and `agentbox heavy`,
//     ask the same way (cli/runphase.go). Starting the browser, the emulator
//     or a recording from inside the agent takes a free slot at once or is
//     refused with ErrorNoRunSlot (needRunSlot), as do the desktop tools.
//   - Ending: `agentbox run down`, a lease that expires (runslots.LeaseTTL
//     after the agent last did anything in its run phase), or the agent
//     stopping or going. The first two stop what the run phase started
//     (endRunPhase): what it ran in its cgroup, the containers that weren't
//     running when it was granted, a recording, the emulator, the browser.
//   - Code phase: an agent holding no slot has a memory.high under its
//     memory.max (agent.CodePhaseHigh), so a dev server left running is
//     squeezed; a holder has none.
//
// Leases are the daemon's memory, not the database's: a restart frees every
// slot (what ran keeps running, squeezed by the code phase's memory.high, and
// the agent runs `agentbox run up` again). The learned peaks are kept.

// runSlotWait is the most one POST /v1/self/run/up waits for a slot before
// answering where the agent stands.
const runSlotWait = 30 * time.Second

// runSlotState is the run slots, under mu.
type runSlotState struct {
	mu   sync.Mutex
	pool *runslots.Pool
	// instance is each holder's and waiter's machine, by agent ref.
	instance map[string]string
	// projects are the projects with agents running, as the last step saw.
	projects []string
	// changed is closed, and replaced, whenever who holds or waits changes:
	// what waits for a slot waits on it.
	changed chan struct{}
	// shown is what the app was last told (runSlotsKey), so a step that
	// changes nothing publishes nothing.
	shown string
	// loaded says the learned peaks were read from the database.
	loaded bool
}

func newRunSlotState() *runSlotState {
	return &runSlotState{pool: runslots.New(), instance: map[string]string{}, changed: make(chan struct{})}
}

// runPhaseOps is what the daemon reads of agents' machines for the run
// slots, and what ending a run phase does in one: agent's functions, or a
// test's.
type runPhaseOps interface {
	MachineUse(instance string) (int64, bool)
	Containers(ctx context.Context, a state.Agent) ([]string, error)
	StopContainers(ctx context.Context, a state.Agent, keep []string) ([]string, error)
	KillRun(instance, key string) error
	StopRecording(ctx context.Context, a state.Agent) error
	StopAndroid(ctx context.Context, a state.Agent) error
	StopBrowser(ctx context.Context, a state.Agent) error
}

type agentRunPhase struct{ s *Server }

func (agentRunPhase) MachineUse(instance string) (int64, bool) { return agent.MachineUse(instance) }
func (o agentRunPhase) Containers(ctx context.Context, a state.Agent) ([]string, error) {
	return o.s.manager(o.s.cfg.Log).RunningContainers(ctx, a)
}
func (o agentRunPhase) StopContainers(ctx context.Context, a state.Agent, keep []string) ([]string, error) {
	return o.s.manager(o.s.cfg.Log).StopContainersExcept(ctx, a, keep)
}
func (agentRunPhase) KillRun(instance, key string) error { return agent.KillRun(instance, key) }
func (o agentRunPhase) StopRecording(ctx context.Context, a state.Agent) error {
	m := o.s.manager(o.s.cfg.Log)
	if st, err := m.Recording(ctx, a); err != nil || !st.Recording {
		return errNothingToStop
	}
	_, err := m.StopRecording(ctx, a)
	return err
}
func (o agentRunPhase) StopAndroid(ctx context.Context, a state.Agent) error {
	return o.s.manager(o.s.cfg.Log).StopAndroid(ctx, a)
}
func (o agentRunPhase) StopBrowser(ctx context.Context, a state.Agent) error {
	return o.s.manager(o.s.cfg.Log).StopBrowser(ctx, a)
}

// errNothingToStop is a run phase's recording that had ended already.
var errNothingToStop = errors.New("nothing to stop")

// noRunSlot refuses an in-agent start of what needs a run slot: the agent
// holds none, and none is free now.
type noRunSlot struct {
	what    string
	waiting bool
	ahead   int
}

func (e noRunSlot) Error() string {
	where := "no run slot is free"
	switch {
	case e.waiting:
		where = fmt.Sprintf("you are waiting for a run slot (%d ahead)", e.ahead)
	case e.ahead > 0:
		where = fmt.Sprintf("no run slot is free and %d %s waiting for one", e.ahead, plural(e.ahead, "agent is", "agents are"))
	}
	return fmt.Sprintf("%s: %s is your run phase, which needs a run slot. "+
		"Run `agentbox run up` in the background, keep coding until it returns, then start it again; `agentbox run down` when you are done.", where, e.what)
}

// loadRunPeaks reads the projects' learned run peaks, once.
func (s *Server) loadRunPeaks(ctx context.Context) {
	sl := s.slots
	sl.mu.Lock()
	loaded := sl.loaded
	sl.loaded = true
	sl.mu.Unlock()
	if loaded {
		return
	}
	peaks, err := s.store.RunPeaks(ctx)
	if err != nil {
		s.logf("run slots: reading the projects' run peaks: %v", err)
		return
	}
	sl.mu.Lock()
	for _, p := range peaks {
		sl.pool.SetPeak(p.Project, runslots.Peak{Bytes: p.Bytes, Runs: p.Runs, At: p.At})
	}
	sl.mu.Unlock()
}

// stepRunSlots measures what runs have and steps the slots: leases nobody
// renewed expire and what their run phases started is stopped, places nobody
// asks for go, and the front of the queue gets what fits. agents is every
// agent, total the VM's memory.
func (s *Server) stepRunSlots(ctx context.Context, agents []state.Agent, total int64, now time.Time) {
	ops := s.runPhase
	use := map[string]int64{}
	byRef := map[string]state.Agent{}
	var projects []string
	for _, a := range agents {
		if a.IsLead() || a.Instance == "" {
			continue
		}
		byRef[a.Ref()] = a
		if a.Status != state.AgentReady {
			continue
		}
		if !slices.Contains(projects, a.Project) {
			projects = append(projects, a.Project)
		}
		if n, ok := ops.MachineUse(a.Instance); ok {
			use[a.Ref()] = n
		}
	}
	slices.Sort(projects)
	pool := s.runPool(total, now)

	sl := s.slots
	sl.mu.Lock()
	var code int64
	for ref, n := range use {
		if sl.pool.Holds(ref) {
			sl.pool.Observe(ref, n)
		} else {
			code += n
		}
	}
	sl.pool.Measure(runslots.Budget{Pool: pool, CodeUse: code})
	sl.projects = projects
	// An agent that went holds nothing.
	var gone []runslots.Lease
	for _, l := range sl.pool.Leases() {
		if _, ok := byRef[l.Agent]; !ok {
			gone = append(gone, *sl.pool.Down(l.Agent))
			delete(sl.instance, l.Agent)
		}
	}
	for _, w := range sl.pool.Queue() {
		if _, ok := byRef[w.Agent]; !ok {
			sl.pool.Down(w.Agent)
			delete(sl.instance, w.Agent)
		}
	}
	expired, _, dropped := sl.pool.Step(now)
	for _, w := range dropped {
		delete(sl.instance, w.Agent)
	}
	type ending struct {
		lease runslots.Lease
		agent state.Agent
	}
	var ends []ending
	for _, l := range expired {
		delete(sl.instance, l.Agent)
		ends = append(ends, ending{*l, byRef[l.Agent]})
	}
	sl.mu.Unlock()

	for _, l := range gone {
		s.learnRunPeak(ctx, l)
	}
	for _, e := range ends {
		go func(e ending) {
			ctx := context.WithoutCancel(ctx)
			stopped, _ := s.endRunPhase(ctx, e.agent, e.lease)
			s.tellRunSlotExpired(ctx, e.agent, stopped)
		}(e)
	}
	s.runSlotsChanged(false)
}

// runPool is what every agent's machine may use together: the agents'
// shared memory.high in a VM of total bytes, as far as the VM can grow now
// (reachableMemory), or as much of what the VM has outside one.
func (s *Server) runPool(total int64, now time.Time) int64 {
	reach := total
	if reach > 0 {
		reach = s.reachableMemory(total, now)
	}
	if reach <= 0 {
		reach = s.lastMem().Total
	}
	return agent.AgentsLimits(reach, 0).High
}

// runSlotsChanged wakes what waits for a slot and tells the app, when the
// slots changed since it was last told (or always, with force), and has the
// agents' states refreshed.
func (s *Server) runSlotsChanged(force bool) {
	view := s.runSlotsView()
	key := runSlotsKey(view)
	sl := s.slots
	sl.mu.Lock()
	same := sl.shown == key
	sl.shown = key
	if !same || force {
		close(sl.changed)
		sl.changed = make(chan struct{})
	}
	sl.mu.Unlock()
	if same && !force {
		return
	}
	if s.events != nil {
		s.events.publish(api.EventRunSlots, view)
	}
	s.refreshSoon()
}

// runSlotsKey is what of the slots the app shows changing: not what the
// holders use, which moves every step.
func runSlotsKey(v api.RunSlots) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d/%d/%d/%d/%d", v.Total, v.Free, v.InUse, v.Queued, v.SlotSize)
	for _, h := range v.Holders {
		fmt.Fprintf(&b, " h:%s:%d", h.Agent, h.Expires.Unix())
	}
	for _, w := range v.Queue {
		fmt.Fprintf(&b, " q:%s", w.Agent)
	}
	for _, p := range v.Projects {
		fmt.Fprintf(&b, " p:%s:%d:%d", p.Project, p.Peak, p.Free)
	}
	return b.String()
}

// runSlotsView is GET /v1/run-slots' answer.
func (s *Server) runSlotsView() api.RunSlots {
	sl := s.slots
	sl.mu.Lock()
	defer sl.mu.Unlock()
	p := sl.pool
	c := p.Counts(sl.projects)
	b := p.Budget()
	out := api.RunSlots{
		Total: c.Total, Free: c.Free, InUse: c.InUse, Queued: c.Queued, SlotSize: c.Size,
		Pool: b.Pool, CodeUse: b.CodeUse, FreeMemory: max(p.Free(), 0),
		Holders: []api.RunSlotHolder{}, Queue: []api.RunSlotWaiter{}, Projects: []api.ProjectRunSlots{},
	}
	inUse, queued := map[string]int{}, map[string]int{}
	for _, l := range p.Leases() {
		inUse[l.Project]++
		out.Holders = append(out.Holders, api.RunSlotHolder{Agent: l.Agent, Project: l.Project, Since: l.Granted, Expires: l.Until, Reserved: l.Reserved, Use: l.Use, Peak: l.Peak})
	}
	for i, w := range p.Queue() {
		queued[w.Project]++
		out.Queue = append(out.Queue, api.RunSlotWaiter{Agent: w.Agent, Project: w.Project, Ahead: i, Since: w.Since})
	}
	peaks := p.Peaks()
	projects := slices.Clone(sl.projects)
	for name := range peaks {
		if !slices.Contains(projects, name) {
			projects = append(projects, name)
		}
	}
	for name := range inUse {
		if !slices.Contains(projects, name) {
			projects = append(projects, name)
		}
	}
	for name := range queued {
		if !slices.Contains(projects, name) {
			projects = append(projects, name)
		}
	}
	slices.Sort(projects)
	for _, name := range projects {
		pk, learned := peaks[name]
		out.Projects = append(out.Projects, api.ProjectRunSlots{
			Project: name, Peak: p.PeakOf(name), Learned: learned, Runs: pk.Runs,
			Free: p.FreeFor(name), InUse: inUse[name], Queued: queued[name],
		})
	}
	return out
}

// getRunSlots is GET /v1/run-slots.
func (s *Server) getRunSlots(w http.ResponseWriter, _ *http.Request) error {
	return writeJSON(w, http.StatusOK, s.runSlotsView())
}

// runSlotOf is where agent ref stands for a run slot (Agent.RunSlot); nil
// for neither holding one nor waiting.
func (s *Server) runSlotOf(ref string) *api.RunSlot {
	sl := s.slots
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return toAPIRunSlot(sl.pool.Status(ref))
}

func toAPIRunSlot(st runslots.Status) *api.RunSlot {
	switch st.State {
	case runslots.Holding:
		until := st.Lease.Until
		return &api.RunSlot{State: api.RunSlotHolding, Since: st.Lease.Granted, Expires: &until}
	case runslots.Waiting:
		return &api.RunSlot{State: api.RunSlotWaiting, Ahead: st.Ahead, Since: st.Waiter.Since}
	}
	return nil
}

// askRunSlot asks for a's run slot (Pool.Up, or Pool.Try with try) and says
// where it stands, with the channel that is closed when that may change. A
// slot newly held learns which containers were running before its run phase,
// before the agent is told it holds it.
func (s *Server) askRunSlot(ctx context.Context, a state.Agent, try bool) (api.RunPhase, <-chan struct{}) {
	sl := s.slots
	ref := a.Ref()
	sl.mu.Lock()
	before := sl.pool.Status(ref).State
	var st runslots.Status
	var granted bool
	if try {
		st, granted = sl.pool.Try(ref, a.Project, time.Now())
	} else {
		st, granted = sl.pool.Up(ref, a.Project, time.Now())
	}
	if st.State != "" {
		sl.instance[ref] = a.Instance
	}
	changed := sl.changed
	sl.mu.Unlock()
	if st.State == runslots.Holding && !st.Lease.ContainersKnown {
		s.noteContainers(ctx, a, st.Lease.Key)
	}
	if st.State != before {
		s.runSlotsChanged(true)
	}
	ph := s.runPhaseOf(ref)
	ph.Granted = granted
	if st.State == "" {
		ph.Ahead = st.Ahead
	}
	return ph, changed
}

// noteContainers records, on a's lease key, the containers running before
// its run phase, which ending it leaves alone. When they can't be read none
// are known, and none are stopped.
func (s *Server) noteContainers(ctx context.Context, a state.Agent, key string) {
	ids, err := s.runPhase.Containers(ctx, a)
	if err != nil {
		s.logf("run slots: %s: reading its containers: %v", a.Ref(), err)
		return
	}
	s.markRunPhase(a.Ref(), key, func(l *runslots.Lease) { l.Containers, l.ContainersKnown = ids, true })
}

// markRunPhase records on agent ref's lease what its run phase started,
// when it still holds the lease key ("" for any).
func (s *Server) markRunPhase(ref, key string, f func(*runslots.Lease)) {
	sl := s.slots
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.pool.Mark(ref, func(l *runslots.Lease) {
		if key == "" || l.Key == key {
			f(l)
		}
	})
}

// runPhaseOf is where agent ref's run phase stands, for the agent.
func (s *Server) runPhaseOf(ref string) api.RunPhase {
	sl := s.slots
	sl.mu.Lock()
	defer sl.mu.Unlock()
	st := sl.pool.Status(ref)
	c := sl.pool.Counts(sl.projects)
	ph := api.RunPhase{State: st.State, Ahead: st.Ahead, Free: c.Free, Total: c.Total}
	if st.Lease != nil {
		until := st.Lease.Until
		ph.Key, ph.Expires = st.Lease.Key, &until
	}
	return ph
}

// runUp is POST /v1/self/run/up: a run slot for the calling agent, waiting
// up to WaitSeconds for one.
func (s *Server) runUp(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req api.RunUpRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		wait := min(time.Duration(max(req.WaitSeconds, 0))*time.Second, runSlotWait)
		deadline := time.Now().Add(wait)
		for {
			ph, changed := s.askRunSlot(r.Context(), a, req.Try)
			if ph.State == api.RunSlotHolding && req.Browser {
				s.markRunPhase(a.Ref(), ph.Key, func(l *runslots.Lease) { l.Browser = true })
			}
			left := time.Until(deadline)
			if ph.State == api.RunSlotHolding || req.Try || left <= 0 {
				return writeJSON(w, http.StatusOK, ph)
			}
			timer := time.NewTimer(left)
			select {
			case <-changed:
			case <-timer.C:
			case <-r.Context().Done():
				timer.Stop()
				return r.Context().Err()
			}
			timer.Stop()
		}
	}
}

// runStatus is GET /v1/self/run.
func (s *Server) runStatus(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, s.runPhaseOf(a.Ref()))
	}
}

// runJoin is POST /v1/self/run/join: a process of the agent's into its run
// phase's cgroup, which ending the run phase kills.
func (s *Server) runJoin(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req api.RunJoin
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
		ph := s.runPhaseOf(a.Ref())
		if ph.State != api.RunSlotHolding {
			return noRunSlot{what: "what you run", waiting: ph.State == api.RunSlotWaiting, ahead: ph.Ahead}
		}
		s.slots.mu.Lock()
		s.slots.pool.Beat(a.Ref(), time.Now())
		s.slots.mu.Unlock()
		if err := s.runCgroups.Place(instance, ph.Key, req.PID); err != nil {
			return fmt.Errorf("joining the run phase: %w", err)
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

// runDown is DELETE /v1/self/run: the calling agent's run phase ends, what
// it started is stopped, and its slot goes to the next in line.
func (s *Server) runDown(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		sl := s.slots
		sl.mu.Lock()
		l := sl.pool.Down(a.Ref())
		delete(sl.instance, a.Ref())
		sl.mu.Unlock()
		var out api.RunPhase
		if l != nil {
			out.Stopped, out.Peak = s.endRunPhase(r.Context(), a, *l)
		} else {
			// A place in the queue given up.
			s.runSlotsChanged(true)
		}
		ph := s.runPhaseOf(a.Ref())
		out.Free, out.Total = ph.Free, ph.Total
		return writeJSON(w, http.StatusOK, out)
	}
}

// endRunPhase stops what a's run phase started, which held l: a recording,
// what ran in its cgroup, the containers started since, the emulator and the
// browser. It learns the run's peak, gives the slot to the next in line, and
// answers what it stopped and the peak.
func (s *Server) endRunPhase(ctx context.Context, a state.Agent, l runslots.Lease) (stopped []string, peak int64) {
	ops := s.runPhase
	instance := a.Instance
	if l.Recording {
		switch err := ops.StopRecording(ctx, a); {
		case err == nil:
			stopped = append(stopped, "the recording (kept in your media)")
		case !errors.Is(err, errNothingToStop):
			s.logf("run slots: %s: stopping its recording: %v", l.Agent, err)
		}
	}
	if instance != "" {
		if err := ops.KillRun(instance, l.Key); err != nil {
			s.logf("run slots: %s: stopping what its run phase ran: %v", l.Agent, err)
		}
	}
	if l.ContainersKnown {
		switch ids, err := ops.StopContainers(ctx, a, l.Containers); {
		case err != nil:
			s.logf("run slots: %s: %v", l.Agent, err)
		case len(ids) > 0:
			stopped = append(stopped, fmt.Sprintf("%d %s started since `agentbox run up`", len(ids), plural(len(ids), "container", "containers")))
		}
	}
	if l.Android {
		if err := ops.StopAndroid(ctx, a); err != nil {
			s.logf("run slots: %s: stopping its emulator: %v", l.Agent, err)
		} else {
			stopped = append(stopped, "the Android emulator")
		}
	}
	if l.Browser {
		if err := ops.StopBrowser(ctx, a); err != nil {
			s.logf("run slots: %s: stopping its browser: %v", l.Agent, err)
		} else {
			stopped = append(stopped, "the browser")
		}
	}
	s.learnRunPeak(ctx, l)
	// Its memory is free now: the next in line has the slot.
	s.slots.mu.Lock()
	s.slots.pool.Grant(time.Now())
	s.slots.mu.Unlock()
	s.runSlotsChanged(true)
	return stopped, l.Peak
}

// learnRunPeak takes the peak of a run that held l into its project's
// learned run peak, and keeps it.
func (s *Server) learnRunPeak(ctx context.Context, l runslots.Lease) {
	if l.Peak <= 0 {
		return
	}
	sl := s.slots
	sl.mu.Lock()
	pk := sl.pool.Learn(l.Project, l.Peak, time.Now())
	sl.mu.Unlock()
	if err := s.store.SetRunPeak(ctx, state.RunPeak{Project: l.Project, Bytes: pk.Bytes, Runs: pk.Runs, At: pk.At}); err != nil {
		s.logf("run slots: keeping %s's run peak: %v", l.Project, err)
	}
}

// tellRunSlotExpired tells an agent its run slot expired, and what that
// stopped.
func (s *Server) tellRunSlotExpired(ctx context.Context, a state.Agent, stopped []string) {
	if a.Name == "" {
		return
	}
	what := "what your run phase started was stopped"
	if len(stopped) > 0 {
		what = "AgentBox stopped what your run phase started: " + strings.Join(stopped, ", ") + ", and what you ran through it"
	}
	text := fmt.Sprintf("Your run slot expired: you hadn't used your run phase for %s, so %s. "+
		"Run `agentbox run up` again before you start the app or use the desktop, and `agentbox run down` when you are done.",
		runslots.LeaseTTL, what)
	if _, err := s.tellUnlessRetired(ctx, a, text); err != nil {
		s.logf("run slots: telling %s its slot expired: %v", a.Ref(), err)
	}
	s.captureEvent(ctx, a.Project, a.Name, "run_slot_expired", map[string]any{"stopped": stopped}, "")
}

// dropRunSlot frees an agent's run slot, or its place in the queue, as it
// stops or goes: its machine stops what ran, and its run's peak is learned.
func (s *Server) dropRunSlot(ref string) {
	sl := s.slots
	sl.mu.Lock()
	l := sl.pool.Down(ref)
	delete(sl.instance, ref)
	sl.pool.Grant(time.Now())
	sl.mu.Unlock()
	if l != nil {
		s.learnRunPeak(s.background(), *l)
	}
	s.runSlotsChanged(true)
}

// needRunSlot is an in-agent start of what the run phase runs (what: its
// browser, emulator, a recording): the agent's slot is renewed, or a free one
// taken at once, and mark records on it what the start started; with none,
// the start is refused (noRunSlot), and the agent told to run `agentbox run
// up`.
func (s *Server) needRunSlot(instance, what string, mark func(*runslots.Lease), next func(http.ResponseWriter, *http.Request) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		ph, _ := s.askRunSlot(r.Context(), a, true)
		if ph.State != api.RunSlotHolding {
			return noRunSlot{what: what, waiting: ph.State == api.RunSlotWaiting, ahead: ph.Ahead}
		}
		if err := next(w, r); err != nil {
			return err
		}
		if mark != nil {
			s.markRunPhase(a.Ref(), ph.Key, mark)
		}
		return nil
	}
}

// codePhaseHigh is the memory.high of agent ref's machine, whose memory.max
// is limit: none while it holds a run slot, agent.CodePhaseHigh otherwise.
func (s *Server) codePhaseHigh(ref string, limit int64) int64 {
	sl := s.slots
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.pool.Holds(ref) {
		return 0
	}
	return agent.CodePhaseHigh(limit)
}
