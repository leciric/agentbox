package daemon

import (
	"context"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// mayReclaim reports whether an agent's machine may have its memory pushed
// out now: it is one of the daemon's agents, ready (not stopped, On hold,
// being removed or gone), and its machine runs. A reclaim is started aside,
// often seconds after it was decided; on 2026-11-06 one spun on a machine
// no longer in the daemon's list. Machines that stopped meanwhile are left
// alone too: their cgroup is gone or empty.
func (s *Server) mayReclaim(ctx context.Context, instance string) bool {
	if !s.reclaimsAgent(ctx, instance) {
		return false
	}
	running, _ := s.machineCgroups.Running(instance)
	return running
}

// mayReclaimRun is mayReclaim for a paused run: its agent the daemon's and
// ready, and the run's cgroup still holding processes.
func (s *Server) mayReclaimRun(ctx context.Context, instance, key string) bool {
	return s.reclaimsAgent(ctx, instance) && s.runCgroups.Populated(instance, key)
}

// reclaimsAgent reports whether instance is one of the daemon's agents,
// ready.
func (s *Server) reclaimsAgent(ctx context.Context, instance string) bool {
	if instance == "" {
		return false
	}
	a, err := s.store.AgentByInstance(ctx, instance)
	return err == nil && !a.IsLead() && a.Status == state.AgentReady
}

// killReclaimWriters kills what a daemon before this one left writing to a
// memory.reclaim: nothing of this daemon's is yet, and one left over spins
// for as long as the VM runs.
func (s *Server) killReclaimWriters() {
	if n := agent.KillReclaimWriters(); n > 0 {
		s.logf("memory: killed %d memory.reclaim writers left from before this daemon started", n)
	}
}
