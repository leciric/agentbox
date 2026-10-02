package daemon

import (
	"context"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// stopAgent stops an agent, freeing its Docker space first unless Settings
// turned that off: every stop goes through here — by hand, retired, idle, or
// every agent at once. Destroy doesn't, as it deletes the machine, Docker and
// all. The disk guard pauses rather than stops, and a paused machine can't
// run a prune.
func (s *Server) stopAgent(ctx context.Context, m *agent.Manager, a state.Agent) error {
	s.pruneDocker(ctx, m, a)
	return m.Stop(ctx, a)
}

// pruneDocker frees the Docker space of an agent about to stop
// (agent.Manager.PruneDocker): what it freed goes to the daemon's log and,
// when it's anything, to the agent's events. A failure is logged and
// otherwise ignored: it must never keep the agent from stopping.
func (s *Server) pruneDocker(ctx context.Context, m *agent.Manager, a state.Agent) {
	if on, err := s.store.FlagOn(ctx, state.SettingDockerPruneOnStop); err != nil || !on {
		return
	}
	start := time.Now()
	pruneCtx, cancel := context.WithTimeout(ctx, s.dockerPruneTimeout)
	defer cancel()
	p, err := m.PruneDocker(pruneCtx, a)
	if err != nil {
		s.logf("docker prune: %s: %v (%s)", a.Ref(), err, p)
	} else {
		s.logf("docker prune: %s: %s in %s", a.Ref(), p, time.Since(start).Round(time.Second))
	}
	if p.Freed() == 0 {
		return
	}
	s.record(ctx, api.AgentEvent{
		Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title,
		Kind: api.AgentDockerPruned, Summary: "Freed " + agent.HumanBytes(p.Freed()) + " of Docker images and build cache", At: time.Now(),
	})
}
