package daemon

import (
	"context"
	"encoding/json"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// Machines left frozen for memory. The experimental "Pause whole agents for
// memory" (Settings → Experimental) is gone: with swap full, a machine held
// past its longest wait thawed only if another could be frozen in its place,
// so agents could stay frozen, and no new machine boot, until AgentBox
// restarted. Nothing freezes a machine or a heavy command for memory now
// (pressure.go). A daemon from before that may have stopped with machines it
// froze still frozen (state.SettingAgentFreezeFrozen) and heavy commands it
// paused still paused: this one thaws them as it starts, so an install stuck
// that way recovers by upgrading.

// machineCgroups is what the daemon reads and thaws of the agents' machines'
// cgroups: agent's functions, or a test's.
type machineCgroups interface {
	// Running reports whether the machine's cgroup has processes, and
	// whether it is frozen.
	Running(instance string) (running, frozen bool)
	// Thaw thaws the machine's own cgroup.
	Thaw(instance string) error
	// ThawRuns thaws its heavy commands' cgroups that are frozen, and says
	// how many it thawed.
	ThawRuns(instance string) (int, error)
}

type agentMachineCgroups struct{}

func (agentMachineCgroups) Running(instance string) (bool, bool) {
	return agent.MachineRunning(instance)
}
func (agentMachineCgroups) Thaw(instance string) error { return agent.ThawMachine(instance) }
func (agentMachineCgroups) ThawRuns(instance string) (int, error) {
	return agent.ThawRuns(instance)
}

// thawLeftFrozen thaws, as the daemon starts, the machines a daemon before
// it froze for memory and the heavy commands it paused, and forgets which
// machines those were. Nothing freezes a heavy command's cgroup now, so any
// frozen one was left so.
func (s *Server) thawLeftFrozen(ctx context.Context) {
	var left []string
	if v, err := s.store.Setting(ctx, state.SettingAgentFreezeFrozen); err == nil && v != "" {
		if err := json.Unmarshal([]byte(v), &left); err != nil {
			s.logf("memory: reading the agents frozen for memory before AgentBox restarted: %v", err)
		}
	}
	for _, ref := range left {
		a, err := s.agentByRef(ctx, ref)
		if err != nil || a.Instance == "" {
			continue
		}
		s.logf("memory: thawing %s, frozen for memory before AgentBox restarted", ref)
		if err := s.machineCgroups.Thaw(a.Instance); err != nil {
			s.logf("memory: thawing %s: %v", ref, err)
		}
	}
	if agents, err := s.store.Agents(ctx, ""); err == nil {
		for _, a := range agents {
			if a.IsLead() || a.Instance == "" {
				continue
			}
			n, err := s.machineCgroups.ThawRuns(a.Instance)
			if n > 0 {
				s.logf("memory: resumed %d heavy command(s) of %s, paused for memory before AgentBox restarted", n, a.Ref())
			}
			if err != nil {
				s.logf("memory: resuming %s's heavy commands paused before AgentBox restarted: %v", a.Ref(), err)
			}
		}
	}
	if err := s.store.DeleteSetting(ctx, state.SettingAgentFreezeFrozen); err != nil {
		s.logf("memory: forgetting the agents frozen for memory: %v", err)
	}
}
