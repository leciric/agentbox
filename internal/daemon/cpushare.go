package daemon

import (
	"context"
	"time"
)

// cpuShareInterval is how often the agents' CPU shares are checked without a
// kick, for what changes without the daemon: a machine stopped with incus, or
// one that died. Each pass is one `incus list` when nothing changed.
const cpuShareInterval = 30 * time.Second

// kickCPU asks balanceCPU for a pass now: an agent started, stopped, paused or
// went away, so the running agents' shares of the VM's cores are due again.
func (s *Server) kickCPU() {
	select {
	case s.cpuKick <- struct{}{}:
	default:
	}
}

// balanceCPU keeps every running agent's share of the VM's cores right for how
// many agents run (agent.BalanceCPU): as the daemon starts, which also
// replaces whatever share an earlier daemon or release left, whenever it is
// kicked, and on a timer.
func (s *Server) balanceCPU(ctx context.Context) {
	if s.cpuEvery <= 0 {
		return // a test drives it itself
	}
	ticker := time.NewTicker(s.cpuEvery)
	defer ticker.Stop()
	var lastErr string
	for {
		changed, err := s.manager(nil).BalanceCPU(ctx)
		switch {
		case err != nil && err.Error() != lastErr:
			// Once, not every pass, while the same thing keeps failing.
			s.logf("giving the agents their share of the VM's cores: %v", err)
		case changed > 0:
			s.logf("gave %d agent machine(s) a new share of the VM's cores", changed)
		}
		lastErr = ""
		if err != nil {
			lastErr = err.Error()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.cpuKick:
		}
	}
}
