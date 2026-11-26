package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// What agents still have running (agent/processes.go reads and kills it):
// GET /v1/processes for every agent, GET .../processes for one, and POST
// .../processes/{pid}/kill. Nothing runs in the background for it. The
// daemon remembers each machine's last reading for a while, so the next one
// says how much CPU each tree used in between and since when it has been
// idle; a reading with none recent enough to measure from takes a second
// one a moment later.

const (
	// processRecent is how old a machine's last reading may be and still
	// measure CPU from: older, and a second reading is taken.
	processRecent = 2 * time.Minute
	// processForget is how long a machine nobody asked about is remembered.
	processForget = time.Hour
	// processMinGap is how far apart two readings have to be to measure CPU
	// between them: closer, and a few clock ticks make a percentage.
	processMinGap = 400 * time.Millisecond
	// processBusy is how much of a core a tree has to use to count as busy.
	processBusy = 2.0
	// processQuiet is how much of a core a tree first seen has to have used
	// on average since it started to count as busy then: under it, it has
	// been idle as long as it has run, near enough.
	processQuiet = 1.0
)

type processSampler struct {
	now func() time.Time
	// wait is how long apart the two readings are when a machine has no
	// recent one.
	wait time.Duration
	read func(instance string) ([]agent.ProcessTree, error)
	// pss adds the proportional memory of every machine read at once.
	pss  func(machines ...[]agent.ProcessTree)
	kill func(ctx context.Context, instance string, pid int, grace time.Duration) (agent.Killed, error)
	// workers is the count of the heavy command a tree runs, by the cgroups
	// its processes are in (Server.runWorkers); nil when unset.
	workers func(instance string, cgroups []string) *api.HeavyWorkers

	mu       sync.Mutex
	machines map[string]*processHistory // by instance
}

type processHistory struct {
	at    time.Time
	ticks map[processKey]uint64
	// busy is when each tree, by its root, was last seen busy, and cpu
	// what it used since the reading before.
	busy map[processKey]time.Time
	cpu  map[processKey]float64
}

// processKey is a process, its number and its start telling it from a later
// one given the same number.
type processKey struct {
	pid   int
	start uint64
}

func keyOf(p agent.MachineProcess) processKey { return processKey{p.PID, p.StartTicks} }

func newProcessSampler() *processSampler {
	return &processSampler{
		now:      time.Now,
		wait:     600 * time.Millisecond,
		read:     agent.MachineProcesses,
		pss:      agent.SetPSS,
		kill:     agent.KillMachineProcess,
		machines: map[string]*processHistory{},
	}
}

// sample reads every machine in instances, and gives each its trees as the
// API has them, or why it couldn't. Machines with no recent reading are read
// twice, wait apart, all of them in the same wait. Proportional memory is read
// once, for all of them, on the last reading.
func (ps *processSampler) sample(instances []string) (map[string][]api.ProcessTree, map[string]error) {
	ps.mu.Lock()
	now := ps.now()
	var stale []string
	for _, inst := range instances {
		if h := ps.machines[inst]; h == nil || now.Sub(h.at) > processRecent {
			stale = append(stale, inst)
		}
	}
	ps.mu.Unlock()
	if len(stale) > 0 && ps.wait > 0 {
		for _, inst := range stale {
			if trees, err := ps.read(inst); err == nil {
				ps.record(inst, trees, ps.now())
			}
		}
		time.Sleep(ps.wait)
	}
	read := map[string][]agent.ProcessTree{}
	errs := map[string]error{}
	var all [][]agent.ProcessTree
	for _, inst := range instances {
		trees, err := ps.read(inst)
		if err != nil {
			errs[inst] = err
			ps.forget(inst)
			continue
		}
		read[inst] = trees
		all = append(all, trees)
	}
	if ps.pss != nil {
		ps.pss(all...)
	}
	out := map[string][]api.ProcessTree{}
	at := ps.now()
	for inst, trees := range read {
		out[inst] = ps.record(inst, trees, at)
	}
	ps.mu.Lock()
	for inst, h := range ps.machines {
		if now.Sub(h.at) > processForget {
			delete(ps.machines, inst)
		}
	}
	ps.mu.Unlock()
	return out, errs
}

func (ps *processSampler) forget(instance string) {
	ps.mu.Lock()
	delete(ps.machines, instance)
	ps.mu.Unlock()
}

// record keeps a machine's reading, and gives its trees with what they used
// since the last one.
func (ps *processSampler) record(instance string, trees []agent.ProcessTree, now time.Time) []api.ProcessTree {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	h := ps.machines[instance]
	if h == nil {
		h = &processHistory{busy: map[processKey]time.Time{}}
		ps.machines[instance] = h
	}
	since := now.Sub(h.at)
	measured := !h.at.IsZero() && since > 0 && since <= processRecent
	// Two clients reading the same machine a moment apart (the Running
	// view and the agent's panel) would measure CPU over a few ticks: the
	// second keeps the first's figures and its base.
	again := measured && since < processMinGap
	ticks := map[processKey]uint64{}
	busy := map[processKey]time.Time{}
	cpus := map[processKey]float64{}
	out := make([]api.ProcessTree, 0, len(trees))
	for _, t := range trees {
		var used uint64
		for _, p := range t.Processes {
			k := keyOf(p)
			ticks[k] = p.CPUTicks
			if before, ok := h.ticks[k]; ok && before <= p.CPUTicks {
				used += p.CPUTicks - before
			} else if measured && p.Started.After(h.at) {
				used += p.CPUTicks // all of it since the last reading
			}
		}
		root := t.Root()
		rk := keyOf(root)
		cpu := 0.0
		switch {
		case again:
			cpu = h.cpu[rk]
		case measured:
			cpu = float64(used) / agent.ClockTicks / since.Seconds() * 100
		}
		last, seen := h.busy[rk]
		switch {
		case measured && cpu >= processBusy:
			last = now
		case !seen:
			// First seen: idle since it started if it has hardly used any
			// CPU in all that time, busy now otherwise.
			last = now
			if age := now.Sub(root.Started); !root.Started.IsZero() && age > 0 {
				if lifetime := float64(t.CPUTicks()) / agent.ClockTicks / age.Seconds() * 100; lifetime < processQuiet {
					last = root.Started
				}
			}
		}
		busy[rk] = last
		cpus[rk] = cpu
		tree := apiTree(t, cpu, now.Sub(last))
		if ps.workers != nil && !tree.Protected {
			tree.Workers = ps.workers(instance, treeCgroups(t))
		}
		out = append(out, tree)
	}
	if again {
		return out
	}
	h.at, h.ticks, h.busy, h.cpu = now, ticks, busy, cpus
	return out
}

// treeCgroups is the cgroups a tree's processes are in, below their
// machine's, each once.
func treeCgroups(t agent.ProcessTree) []string {
	var out []string
	for _, p := range t.Processes {
		if p.Cgroup != "" && p.Cgroup != "." && !slices.Contains(out, p.Cgroup) {
			out = append(out, p.Cgroup)
		}
	}
	return out
}

// apiTree is a tree as the API has it.
func apiTree(t agent.ProcessTree, cpu float64, idle time.Duration) api.ProcessTree {
	root := t.Root()
	memory, rss, pss := t.Memory()
	kind := t.Kind
	if kind == agent.TreeAgent {
		kind = api.ProcessTreeAgent
	}
	out := api.ProcessTree{
		PID:         root.MachinePID,
		Label:       t.Label,
		Command:     root.Command,
		Kind:        kind,
		Protected:   t.Protected(),
		Processes:   len(t.Processes),
		Memory:      memory,
		RSS:         rss,
		PSS:         pss,
		CPU:         cpu,
		CPUSeconds:  float64(t.CPUTicks()) / agent.ClockTicks,
		IdleSeconds: max(int64(idle/time.Second), 0),
		Members:     make([]api.ProcessMember, 0, len(t.Processes)),
	}
	if !root.Started.IsZero() {
		out.Started = root.Started.UTC()
	}
	if !out.Protected {
		out.Heavy = memory >= api.ProcessTreeHeavy
		out.Idle = idle >= api.ProcessTreeIdle
	}
	byVM := map[int]int{}
	for _, p := range t.Processes {
		byVM[p.PID] = p.MachinePID
	}
	for _, p := range t.Processes {
		out.Members = append(out.Members, api.ProcessMember{PID: p.MachinePID, PPID: byVM[p.PPID], Name: p.Name, Command: p.Command, Memory: p.Memory()})
	}
	return out
}

// agentProcesses puts an agent's trees, or why it has none, as the API has
// them.
func agentProcesses(a state.Agent, trees []api.ProcessTree, err error) api.AgentProcesses {
	out := api.AgentProcesses{Ref: a.Ref(), Project: a.Project, Name: a.Name, Title: a.Title, Trees: []api.ProcessTree{}}
	switch {
	case errors.Is(err, agent.ErrMachineNotRunning):
		return out
	case err != nil:
		out.Error = err.Error()
		return out
	}
	out.Running = true
	out.Trees = trees
	for _, t := range trees {
		out.Memory += t.Memory
		out.CPU += t.CPU
		out.Processes += t.Processes
		if !t.Protected {
			out.Killable += t.Memory
		}
	}
	return out
}

// listProcesses is GET /v1/processes: every agent with a running machine, in
// ?project= or every project, the one holding most first.
func (s *Server) listProcesses(w http.ResponseWriter, r *http.Request) error {
	agents, err := s.store.Agents(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		return err
	}
	var machines []state.Agent
	var instances []string
	for _, a := range agents {
		if a.IsLead() || a.Instance == "" {
			continue
		}
		machines = append(machines, a)
		instances = append(instances, a.Instance)
	}
	trees, errs := s.processes.sample(instances)
	out := api.ProcessList{Agents: []api.AgentProcesses{}, At: s.processes.now().UTC()}
	for _, a := range machines {
		ap := agentProcesses(a, trees[a.Instance], errs[a.Instance])
		if ap.Running || ap.Error != "" {
			out.Agents = append(out.Agents, ap)
		}
	}
	sortAgentProcesses(out.Agents)
	return writeJSON(w, http.StatusOK, out)
}

func sortAgentProcesses(agents []api.AgentProcesses) {
	slices.SortStableFunc(agents, func(a, b api.AgentProcesses) int {
		return cmp.Or(cmp.Compare(b.Memory, a.Memory), strings.Compare(a.Ref, b.Ref))
	})
}

// getAgentProcesses is GET /v1/agents/{project}/{agent}/processes.
func (s *Server) getAgentProcesses(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	trees, errs := s.processes.sample([]string{a.Instance})
	return writeJSON(w, http.StatusOK, agentProcesses(a, trees[a.Instance], errs[a.Instance]))
}

// killProcess is POST /v1/agents/{project}/{agent}/processes/{pid}/kill: the
// process and what it started get SIGTERM, then SIGKILL after
// agent.KillGrace, and the agent is told, so it doesn't start it again
// without a thought. The machine's own processes and its AI tool's are
// refused.
func (s *Server) killProcess(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil || pid <= 0 {
		return fmt.Errorf("%q isn't a process number", r.PathValue("pid"))
	}
	// The kill finishes, and is told, even when whoever asked goes away.
	ctx := context.WithoutCancel(r.Context())
	k, err := s.processes.kill(ctx, a.Instance, pid, agent.KillGrace)
	switch {
	case errors.Is(err, agent.ErrProtectedProcess):
		return writeJSON(w, http.StatusForbidden, api.Error{Error: fmt.Sprintf("%s: %v", a.Ref(), err), Code: api.ErrorProtectedProcess})
	case errors.Is(err, agent.ErrNoSuchProcess):
		return writeJSON(w, http.StatusNotFound, api.Error{Error: fmt.Sprintf("%s: %v", a.Ref(), err)})
	case errors.Is(err, agent.ErrMachineNotRunning):
		return writeJSON(w, http.StatusConflict, api.Error{Error: fmt.Sprintf("%s: %v", a.Ref(), err)})
	case err != nil:
		return err
	}
	memory, _, _ := k.Tree.Memory()
	out := api.KillProcessResult{Ref: a.Ref(), PID: pid, Label: k.Tree.Label, Processes: len(k.Tree.Processes), Memory: memory, Forced: k.Forced, Left: k.Left}
	s.logf("processes: the user killed pid %d in %s (%s, %d processes, %s; %d forced, %d left)",
		pid, a.Ref(), headWords(k.Tree.Label, 80), out.Processes, agent.HumanBytes(memory), k.Forced, k.Left)
	text := killedText(k, pid, s.processes.now())
	go func() {
		if _, err := s.processTell(ctx, a, text); err != nil {
			s.logf("processes: telling %s its process was killed: %v", a.Ref(), err)
		}
		s.captureEvent(ctx, a.Project, a.Name, "process_killed", map[string]any{
			"pid": pid, "label": k.Tree.Label, "processes": out.Processes, "memory": memory, "forced": k.Forced, "left": k.Left,
		}, "")
	}()
	return writeJSON(w, http.StatusOK, out)
}

// killedText is what the agent is told when the user kills one of its
// process trees.
func killedText(k agent.Killed, pid int, now time.Time) string {
	root := k.Tree.Root()
	memory, _, _ := k.Tree.Memory()
	what := fmt.Sprintf("pid %d", pid)
	if n := len(k.Tree.Processes) - 1; n == 1 {
		what += " and the process it started"
	} else if n > 1 {
		what += fmt.Sprintf(" and the %d processes it started", n)
	}
	what += ", holding " + agent.HumanBytes(memory)
	if !root.Started.IsZero() && now.After(root.Started) {
		what += ", running for " + shortDuration(now.Sub(root.Started))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The user killed `%s` in your machine (%s) from AgentBox's list of what agents have running.", k.Tree.Label, what)
	if k.Left > 0 {
		fmt.Fprintf(&b, " %d of its processes hadn't ended even after SIGKILL.", k.Left)
	}
	b.WriteString(" Don't start it again unless your task needs it now, and stop what you start in the background once you're done with it.")
	return b.String()
}
