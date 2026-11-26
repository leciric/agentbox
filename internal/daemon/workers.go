package daemon

import (
	"context"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// How many workers a heavy command starts with (agent/workers.go,
// agent/workermemory.go): its share of the VM's cores among the heavy
// commands running, and, with "Fewer build workers when memory is short" on
// (the default, 26 Nov 2026), no more than the memory it can start in holds
// at what one worker of its tool takes, at least one. Off, it is its share
// of the cores exactly as before. Either way the count is made once, as the
// command starts: nothing waits for memory, and nothing started changes.
//
// The memory it can start in (workerBudget) is what the VM has available,
// counting what the kernel can take back from its caches, and what it can
// still grow to (its cap, lowered to what the user's computer can spare:
// hostLoad.withCeiling), less what the other heavy commands running would
// likely still take (agent.WorkerClaim); no more than what its own agent's
// memory limit leaves, less what that agent's other commands would take, as
// past that limit the kernel kills in it.
//
// What one worker takes is learned per project and tool from the runs that
// end (learnWorkers): their cgroup's memory.peak, less the caches it still
// holds, over the workers they started with.

// runCount is what the daemon keeps of a heavy command's workers, by its run.
type runCount struct {
	project string
	tool    string
	count   api.HeavyWorkers
}

// workerMemory is what one worker of each tool takes in each project, read
// from the store as first needed and kept beside it.
type workerMemory struct {
	mu    sync.Mutex
	known map[workerKey]state.WorkerMemory
}

type workerKey struct{ project, tool string }

func newWorkerMemory() *workerMemory {
	return &workerMemory{known: map[workerKey]state.WorkerMemory{}}
}

// perWorker is what one worker of tool is taken to take in project: learned
// from its runs, or the tool's default (learned false).
func (s *Server) perWorker(ctx context.Context, project, tool string) (per int64, learned bool) {
	w, ok := s.workerMemoryOf(ctx, project, tool)
	if !ok || w.PerWorker <= 0 {
		return agent.WorkerDefault(tool), false
	}
	return w.PerWorker, true
}

func (s *Server) workerMemoryOf(ctx context.Context, project, tool string) (state.WorkerMemory, bool) {
	k := workerKey{project, tool}
	m := s.workerMemory
	m.mu.Lock()
	w, ok := m.known[k]
	m.mu.Unlock()
	if ok {
		return w, w.Runs > 0
	}
	w, ok, err := s.store.WorkerMemory(ctx, project, tool)
	if err != nil {
		return state.WorkerMemory{}, false
	}
	if !ok {
		w = state.WorkerMemory{Project: project, Tool: tool}
	}
	m.mu.Lock()
	m.known[k] = w
	m.mu.Unlock()
	return w, ok
}

// countWorkers makes the count of a heavy command (key, of a's, in instance)
// starting now with running heavy commands going, this one counted.
func (s *Server) countWorkers(ctx context.Context, a state.Agent, instance, key, command string, running int) api.HeavyWorkers {
	cores := s.vmCores()
	cpu := agent.HeavyWorkers(cores, running)
	count := api.HeavyWorkers{Workers: cpu, CPU: cpu, Cores: cores, Running: running, Limit: agent.LimitCPU}
	if on, err := s.store.FlagOn(ctx, state.SettingFewerWorkersForMemory); err != nil || !on {
		return count
	}
	free, claimed, byAgent, ok := s.workerBudget(ctx, a.Ref(), instance, key)
	if !ok {
		return count // memory unread: the cores alone
	}
	tool := agent.HeavyTool(command)
	per, learned := s.perWorker(ctx, a.Project, tool)
	n, limit := agent.MemoryWorkers(cpu, free, per)
	if byAgent && limit == agent.LimitMemory {
		limit = agent.LimitAgent
	}
	count.Workers, count.Limit = n, limit
	count.Memory, count.Tool, count.PerWorker, count.Learned = true, tool, per, learned
	count.Free, count.Claimed = free, claimed
	return count
}

// workerBudget is the memory a heavy command (key, of ref's agent in
// instance) can start in now, and what the other heavy commands running
// would likely still take of it (claimed); byAgent says the agent's own
// limit left less than the VM. ok is false while the VM's memory is unread.
func (s *Server) workerBudget(ctx context.Context, ref, instance, key string) (free, claimed int64, byAgent, ok bool) {
	type other struct {
		instance, key, project, tool string
		workers                      int
		per                          int64
		placed                       bool
	}
	h := s.heavy
	h.mu.Lock()
	mem := h.reading.Mem
	var others []other
	for _, r := range h.g.Running() {
		if r.Agent == ref && r.Key == key {
			continue
		}
		c := h.counts[r.ID()]
		if c == nil {
			continue
		}
		others = append(others, other{h.instance[r.ID()], r.Key, c.project, c.tool, c.count.Workers, c.count.PerWorker, r.Placed})
	}
	h.mu.Unlock()
	if !mem.Known() {
		return 0, 0, false, false
	}
	vm := mem.Available
	if mem.Cap > mem.Total {
		vm += mem.Cap - mem.Total
	}
	var agentClaimed int64
	for _, o := range others {
		if agent.ServerTool(o.tool) {
			continue
		}
		per := o.per
		if per <= 0 {
			per, _ = s.perWorker(ctx, o.project, o.tool)
		}
		var used int64
		if o.placed {
			used, _, _ = s.runCgroups.Memory(o.instance, o.key)
		}
		c := agent.WorkerClaim(o.tool, o.workers, per, used)
		claimed += c
		if o.instance == instance {
			agentClaimed += c
		}
	}
	free = vm - claimed
	if room, ok := s.agentRoom(instance); ok && room-agentClaimed < free {
		free, byAgent = room-agentClaimed, true
	}
	return max(free, 0), claimed, byAgent, true
}

// keepCount keeps a run's count beside it, for the Running view, for what
// it claims while others start, and to learn from as it ends.
func (s *Server) keepCount(id, project, command string, count api.HeavyWorkers) {
	tool := count.Tool
	if tool == "" {
		tool = agent.HeavyTool(command)
	}
	h := s.heavy
	h.mu.Lock()
	if _, running := h.instance[id]; running {
		h.counts[id] = &runCount{project: project, tool: tool, count: count}
	}
	h.mu.Unlock()
}

// countOf is a run's count, if its start was seen.
func (s *Server) countOf(id string) (api.HeavyWorkers, bool) {
	h := s.heavy
	h.mu.Lock()
	defer h.mu.Unlock()
	if c := h.counts[id]; c != nil {
		return c.count, true
	}
	return api.HeavyWorkers{}, false
}

// endedRun is a run that ended, with what learning from it needs.
type endedRun struct {
	instance string
	key      string
	placed   bool
	count    *runCount
}

// learnWorkers learns what one worker of a run's tool takes from what it
// used at most, read from its cgroup before it is removed; ok says it
// succeeded (agent.LearnPerWorker). Its cgroup gone or unread, and a run
// that was never counted, teach nothing.
func (s *Server) learnWorkers(ctx context.Context, r endedRun, ok bool) {
	if !r.placed || r.count == nil || r.count.project == "" || r.count.count.Workers <= 0 {
		return
	}
	_, peak, read := s.runCgroups.Memory(r.instance, r.key)
	if !read || peak <= 0 {
		return
	}
	c := r.count
	w, _ := s.workerMemoryOf(ctx, c.project, c.tool)
	next, changed := agent.LearnPerWorker(c.tool, w.PerWorker, peak, c.count.Workers, ok)
	if !changed {
		return
	}
	w = state.WorkerMemory{Project: c.project, Tool: c.tool, PerWorker: next, Runs: w.Runs + 1, UpdatedAt: time.Now()}
	m := s.workerMemory
	m.mu.Lock()
	m.known[workerKey{c.project, c.tool}] = w
	m.mu.Unlock()
	if err := s.store.SetWorkerMemory(ctx, w); err != nil {
		s.logOnce("worker memory", "heavy commands: keeping what a worker of "+c.tool+" takes: "+err.Error())
	}
}

// runWorkers is the count of the heavy command a process tree of instance's
// machine runs, by the cgroups its processes are in (cgroups, below the
// machine's): nil for none.
func (s *Server) runWorkers(instance string, cgroups []string) *api.HeavyWorkers {
	var names []string
	for _, cg := range cgroups {
		top, _, _ := strings.Cut(cg, "/")
		if strings.HasPrefix(top, agent.RunCgroupName("")) {
			names = append(names, top)
		}
	}
	if len(names) == 0 {
		return nil
	}
	h := s.heavy
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.g.Running() {
		c := h.counts[r.ID()]
		if c == nil || h.instance[r.ID()] != instance {
			continue
		}
		name := agent.RunCgroupName(r.Key)
		for _, n := range names {
			if n == name {
				out := c.count
				return &out
			}
		}
	}
	return nil
}
