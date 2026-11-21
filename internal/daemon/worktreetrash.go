package daemon

import (
	"context"

	"agentbox/internal/agent"
)

// emptyWorktreeTrash deletes destroyed agents' worktrees in the background
// (agent.EmptyWorktreeTrash): at start, what a daemon that stopped left, and
// after each destroy. A worktree on the VM's disk frees the host's disk only
// once the VM trims it, so a trim follows: the one the destroy asked for may
// have run before a large worktree was gone.
func (s *Server) emptyWorktreeTrash(ctx context.Context) {
	for {
		if agent.EmptyWorktreeTrash(ctx, s.cfg.Paths, s.shareRoom, s.logf) > 0 {
			s.trim.Kick()
		}
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
