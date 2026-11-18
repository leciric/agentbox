package chv

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"agentbox/internal/api"
)

// The host's last resort (memDecision.Shed): with the host under its floor
// for a few seconds, the supervisor stops the agent that holds the most
// memory, through the daemon's API, as any client could:
//
//  1. GET /v1/usage/memory: each agent's machine and what its cgroup holds
//     (api.MemoryUsageAgent.Memory, RAM without swap).
//  2. The running or paused agent holding the most: POST
//     /v1/agents/{project}/{agent}/pause first, when it runs, which freezes
//     it at once, so it takes no more while it stops.
//  3. POST /v1/agents/{project}/{agent}/stop?why=…: the daemon stops its chat
//     saying why, and its machine (30 s for the guest's own shutdown, then
//     forced, as a frozen machine can't take the signal), on purpose, so it
//     isn't resumed when the VM next starts.
//
// One agent at a time, memShedEvery apart at the soonest, while the host
// stays under its floor; the memory loop shrinks the VM by what each gives
// back.

// shedWhy is what the agent's chat is told.
const shedWhy = "the agent was stopped because the user's computer was running out of memory"

// shedAgent freezes and stops the agent holding the most memory, unless one
// is being stopped already.
func (s *supervisor) shedAgent(ctx context.Context, reason string) {
	if !s.shedding.CompareAndSwap(false, true) {
		return
	}
	defer s.shedding.Store(false)
	s.mu.Lock()
	ready := s.daemonReady
	s.mu.Unlock()
	if !ready {
		s.logf("memory: %s, but the daemon doesn't answer yet: no agent to stop", reason)
		return
	}
	client := s.daemonClient(agentsStopTimeout)
	var usage api.MemoryUsage
	if err := daemonJSON(ctx, client, http.MethodGet, "/v1/usage/memory", &usage); err != nil {
		s.logf("memory: %s, and the daemon can't say which agent holds the most: %v", reason, err)
		return
	}
	a, ok := biggestAgent(usage.Agents)
	if !ok {
		s.logf("memory: %s, and no agent runs to stop", reason)
		return
	}
	project, name, _ := strings.Cut(a.Ref, "/")
	path := "/v1/agents/" + url.PathEscape(project) + "/" + url.PathEscape(name)
	s.logf("memory: %s: stopping %s, which holds %s, the most of any agent", reason, a.Ref, gib(a.Memory))
	if a.State == "running" {
		if err := daemonJSON(ctx, client, http.MethodPost, path+"/pause", nil); err != nil {
			s.logf("memory: freezing %s before stopping it: %v", a.Ref, err)
		}
	}
	if err := daemonJSON(ctx, client, http.MethodPost, path+"/stop?why="+url.QueryEscape(shedWhy), nil); err != nil {
		s.logf("memory: stopping %s: %v", a.Ref, err)
		return
	}
	s.logf("memory: stopped %s for this machine", a.Ref)
}

// biggestAgent is the running or paused agent holding the most memory.
func biggestAgent(agents []api.MemoryUsageAgent) (api.MemoryUsageAgent, bool) {
	var held []api.MemoryUsageAgent
	for _, a := range agents {
		if (a.State == "running" || a.State == "paused") && a.Memory > 0 && strings.Contains(a.Ref, "/") {
			held = append(held, a)
		}
	}
	if len(held) == 0 {
		return api.MemoryUsageAgent{}, false
	}
	return slices.MaxFunc(held, func(a, b api.MemoryUsageAgent) int { return cmp.Compare(a.Memory, b.Memory) }), true
}
