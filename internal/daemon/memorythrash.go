package daemon

import (
	"context"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// watchMemoryThrash samples every running agent's cgroup on a ticker
// (agent.ThrashWatch), whoever is watching: the samples are what say an agent
// is thrashing, and a window missed while the app was closed would leave
// `agentbox limits` without an answer. When an agent starts or stops, the log
// says so and the app is told through the agent's own event.
func (s *Server) watchMemoryThrash(ctx context.Context) {
	ticker := time.NewTicker(agent.ThrashInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		started, stopped := s.thrash.Sample(ctx, time.Now())
		for _, t := range started {
			s.logf("%s is thrashing at its memory limit: %.0f%% memory pressure, re-reading %s/s of what it dropped, %s/s read from disk", t.Instance, t.Pressure, agent.HumanBytes(t.RefaultRate), agent.HumanBytes(t.ReadRate))
		}
		for _, t := range stopped {
			s.logf("%s is no longer thrashing at its memory limit", t.Instance)
		}
		if len(started)+len(stopped) > 0 {
			s.refreshAgents(ctx)
		}
	}
}

// memoryShortage is an agent's MemoryShortage for the API: nil unless it's
// thrashing.
func (s *Server) memoryShortage(instance string) *api.MemoryShortage {
	t, ok := s.thrash.Thrashing(instance)
	if !ok {
		return nil
	}
	return &api.MemoryShortage{
		Since:       t.Since,
		Pressure:    t.Pressure,
		RefaultRate: t.RefaultRate,
		ReadRate:    t.ReadRate,
		Limit:       t.Limit,
		InBudget:    t.InBudget,
		RaiseTo:     t.RaiseTo,
	}
}
