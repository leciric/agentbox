package daemon

import (
	"context"

	"agentbox/internal/agent"
)

// emptyWorktreeTrash deletes destroyed agents' worktrees in the background
// (agent.EmptyWorktreeTrash): at start, what a daemon that stopped left, and
// after each destroy.
func (s *Server) emptyWorktreeTrash(ctx context.Context) {
	for {
		agent.EmptyWorktreeTrash(ctx, s.cfg.Paths, s.shareRoom, s.logf)
		select {
		case <-ctx.Done():
			return
		case <-s.worktreeTrash:
		}
	}
}

func (s *Server) worktreeTrashed() {
	select {
	case s.worktreeTrash <- struct{}{}:
	default:
	}
}
