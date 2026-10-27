package agent

import (
	"context"
	"time"

	"agentbox/internal/state"
)

// Moving an agent's machine between clusters is a daemon operation: it stops the
// pod, archives the claim, restores it into a new one, and writes the row. The
// one part that is the agent's own is the checkpoint taken before any of that,
// because it decides whether the worktree survives an archive that fails.

// CheckpointForMove records the agent's worktree on a ref of its own, and
// publishes it, before its machine leaves the cluster it is on. It is a
// snapshot's worktree half without the machine: a pod's checkpoint is pushed to
// the project's remote, so the commit and the files it holds outlive the claim
// even if the archive that carries the rest of the machine never lands.
//
// The ref is not a turn's: a move is not part of the conversation, so nothing
// lists it as a checkpoint to roll back to or fork from, and pruning the turns
// leaves it alone. Its name carries the moment, so a second move of the same
// agent is a second record rather than an overwrite.
func (m *Manager) CheckpointForMove(ctx context.Context, a state.Agent, target string) (string, error) {
	ref := "refs/agentbox/moves/" + a.Name + "/" + time.Now().UTC().Format("20060102-150405")
	return m.snapshotWorktree(ctx, a, ref, "agentbox: "+a.Ref()+" before it moves to cluster "+target)
}
