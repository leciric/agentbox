package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// "Free resources" is the top bar's one click that stops every running or
// paused agent at once, keeping each one's worktree and branch the way a
// single stop does. It runs as a job so the app can follow it and show what
// was freed at the end: each agent's RAM, swap and CPU are measured just
// before stopping it, and the host's memory around the whole job.

// stopAgentsParallel is how many agents are stopped at the same time. Each
// stop waits for the machine to shut down cleanly, up to 30 seconds, so one
// at a time would make ten agents take minutes; all at once would put every
// shutdown's disk writes on the host together, which is what this is meant
// to relieve.
const stopAgentsParallel = 4

// cpuSampleInterval is how long the job samples each agent's CPU before
// stopping them, the same interval the top bar's own figure uses.
const cpuSampleInterval = time.Second

func (s *Server) stopAgents(w http.ResponseWriter, r *http.Request) error {
	var req api.StopAgentsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	m := s.manager(s.cfg.Log)
	targets, err := s.stopTargets(r.Context(), m, req.Refs)
	if err != nil {
		return err
	}
	target := "all"
	if len(req.Refs) > 0 {
		target = fmt.Sprintf("%d agents", len(req.Refs))
	}
	return s.startJob(w, "stop-agents", target, func(ctx context.Context, log io.Writer) (any, error) {
		return s.runStopAgents(ctx, m, targets, log)
	})
}

// stopTargets is every running or paused agent, of every project, or only
// those of them refs names. A ref that isn't an agent is an error, so a typo
// doesn't quietly stop nothing; one that is already stopped is skipped.
func (s *Server) stopTargets(ctx context.Context, m *agent.Manager, refs []string) ([]agent.Status, error) {
	statuses, err := m.List(ctx, "")
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	var out []agent.Status
	for _, st := range statuses {
		if st.IsLead() {
			continue
		}
		known[st.Ref()] = true
		if len(refs) > 0 && !slices.Contains(refs, st.Ref()) {
			continue
		}
		if st.State == "running" || st.State == "paused" {
			out = append(out, st)
		}
	}
	for _, ref := range refs {
		if !known[ref] {
			return nil, fmt.Errorf("no agent %s", ref)
		}
	}
	return out, nil
}

func (s *Server) runStopAgents(ctx context.Context, m *agent.Manager, targets []agent.Status, log io.Writer) (api.StopAgentsResult, error) {
	result := api.StopAgentsResult{Stopped: []api.StoppedAgent{}}
	if len(targets) == 0 {
		_, _ = fmt.Fprintln(log, "No agent is running.")
		return result, nil
	}
	_, _ = fmt.Fprintf(log, "Measuring what %d %s use…\n", len(targets), plural(len(targets), "agent", "agents"))
	memory, cpu, hostUsed := s.measureAgents(ctx, m)
	result.HostMemoryBefore = hostUsed

	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, stopAgentsParallel)
	for _, st := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			ref, label := st.Ref(), agentLabel(st.Title, st.Ref())
			working := s.chat.State(ref) == api.ChatRunning
			mu.Lock()
			_, _ = fmt.Fprintf(log, "Stopping %s…\n", label)
			mu.Unlock()
			s.chat.Stop(ref, "every agent was stopped to free resources")
			err := s.stopAgent(ctx, m, st.Agent)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				_, _ = fmt.Fprintf(log, "Couldn't stop %s: %v\n", label, err)
				result.Failed = append(result.Failed, api.StopAgentFailure{Ref: ref, Title: st.Title, Error: err.Error()})
				return
			}
			_, _ = fmt.Fprintf(log, "Stopped %s\n", label)
			result.Stopped = append(result.Stopped, api.StoppedAgent{Ref: ref, Title: st.Title, Memory: memory[ref], CPU: cpu[ref], Working: working})
			result.FreedMemory += memory[ref]
			result.FreedCPU += cpu[ref]
			// Each agent shows as stopped as soon as it is, so the app's rail
			// and its progress move one agent at a time.
			s.refreshAgents(ctx)
		}()
	}
	wg.Wait()

	s.refreshAgents(ctx)
	if mem, err := m.MemoryUsage(ctx); err == nil {
		result.HostMemoryAfter = mem.AgentsUsed + mem.OtherUsed
	}
	slices.SortFunc(result.Stopped, func(a, b api.StoppedAgent) int { return cmp.Compare(b.Memory, a.Memory) })
	if len(result.Failed) > 0 && len(result.Stopped) == 0 {
		return result, errors.New(result.Failed[0].Error)
	}
	return result, nil
}

// measureAgents is what each agent holds (RAM and swap) and uses (CPU) right
// now, by ref, and the host's memory in use. Any of them may come back empty
// on a host that can't read it: the agents still get stopped, the result
// only says less about what that freed.
func (s *Server) measureAgents(ctx context.Context, m *agent.Manager) (memory map[string]int64, cpu map[string]float64, hostUsed int64) {
	memory, cpu = map[string]int64{}, map[string]float64{}
	if mem, err := m.MemoryUsage(ctx); err == nil {
		hostUsed = mem.AgentsUsed + mem.OtherUsed
		for _, a := range mem.Agents {
			memory[a.Ref] = a.Memory + a.Swap
		}
	} else {
		s.logf("stop agents: measuring memory: %v", err)
	}
	if use, err := m.CPUUsage(ctx, cpuSampleInterval); err == nil {
		for _, a := range use.Agents {
			cpu[a.Ref] = a.CPU
		}
	} else {
		s.logf("stop agents: measuring CPU: %v", err)
	}
	return memory, cpu, hostUsed
}

func agentLabel(title, ref string) string {
	if title == "" {
		return ref
	}
	return title + " (" + ref + ")"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
