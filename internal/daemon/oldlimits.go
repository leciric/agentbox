package daemon

import (
	"context"
	"strings"

	"agentbox/internal/agent"
)

// dropOldLimits takes off what earlier releases set to keep agents from
// freezing the host they ran on: the shared budget's cgroup limits and the
// reserve it kept for the host's apps (agent.ResetLegacyBudget), and every
// agent machine's own caps (agent.DropOldLimits). It runs once, as the daemon
// starts; a machine it misses has them taken off when it next starts.
func (s *Server) dropOldLimits(ctx context.Context) {
	if reset, err := agent.ResetLegacyBudget(); err != nil {
		s.logf("lifting an earlier release's shared budget: %v", err)
	} else if len(reset) > 0 {
		s.logf("lifted what an earlier release's shared budget set: %s", strings.Join(reset, ", "))
	}
	if changed, err := s.manager(nil).DropOldLimits(ctx); err != nil {
		s.logf("taking an earlier release's limits off the agents' machines: %v", err)
	} else if changed > 0 {
		s.logf("took an earlier release's limits off %d agent machine(s)", changed)
	}
}
