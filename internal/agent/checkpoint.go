package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// Checkpoints are the cheap half of a snapshot, taken at the end of every chat
// turn: the worktree alone, as a commit on refs/agentbox/snapshots/<agent>/turn-<n>
// (gitrepo.SnapshotWorktree), with no Incus snapshot beside it. The branch,
// the index and the files are untouched, and git stores only what changed
// since the last one. The row in state says which turn of the conversation it
// ends, which is what rolling back and forking need besides the files.
//
// A rollback saves what the worktree had first, on
// refs/agentbox/pre-restore/<agent>/<time>, as a checkpoint of its own: the
// conversation after the turn goes, but the work done in it can still be
// forked from.

const (
	// keepTurnCheckpoints is how many of the latest turns can be rolled back
	// to or forked from. Older ones are dropped as new turns end; their
	// commits go when git next collects garbage.
	keepTurnCheckpoints = 50
	// keepSavedCheckpoints is how many pre-rollback states are kept.
	keepSavedCheckpoints = 10
)

// TurnCheckpointID names the checkpoint ending a conversation's nth turn.
func TurnCheckpointID(n int) string { return "turn-" + strconv.Itoa(n) }

// CheckpointID reads what someone typed for a checkpoint: its ID, or just the
// turn's number.
func CheckpointID(s string) string {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return TurnCheckpointID(n)
	}
	return s
}

// Checkpoint records the agent's worktree as the end of a chat turn: the
// number'th of its conversation, headed by the user message turn, whose text
// starts with prompt.
func (m *Manager) Checkpoint(ctx context.Context, a state.Agent, turn string, number int, prompt string) (state.Checkpoint, error) {
	id := TurnCheckpointID(number)
	ref := snapshotRef(a.Name, id)
	commit, err := m.snapshotWorktree(ctx, a, ref, "agentbox checkpoint "+a.Ref()+"@"+id)
	if err != nil {
		return state.Checkpoint{}, err
	}
	head, _ := gitrepo.Repo{Root: a.Worktree}.ResolveCommit(commit + "^")
	cp := state.Checkpoint{
		Project: a.Project, Agent: a.Name, ID: id, Kind: state.CheckpointTurn,
		Turn: turn, Number: number, Prompt: clip(prompt, 200),
		Ref: ref, Commit: commit, Head: head, CreatedAt: time.Now(),
	}
	if err := m.Store.SaveCheckpoint(ctx, cp); err != nil {
		return state.Checkpoint{}, err
	}
	m.pruneCheckpoints(ctx, a)
	return cp, nil
}

// snapshotWorktree commits the worktree's files to ref. Files the agent's
// machine wrote can be its user's alone, so a refusal is retried once they
// are handed back, as Snapshot does.
func (m *Manager) snapshotWorktree(ctx context.Context, a state.Agent, ref, message string) (string, error) {
	commit, err := gitrepo.SnapshotWorktree(a.Worktree, ref, message)
	if err != nil && strings.Contains(err.Error(), "ermission denied") {
		m.handBackFiles(ctx, a)
		commit, err = gitrepo.SnapshotWorktree(a.Worktree, ref, message)
	}
	if err != nil {
		return "", fmt.Errorf("checkpointing the worktree: %w", err)
	}
	return commit, nil
}

// Checkpoints lists the agent's checkpoints, oldest first.
func (m *Manager) Checkpoints(ctx context.Context, a state.Agent) ([]state.Checkpoint, error) {
	return m.Store.Checkpoints(ctx, a.Project, a.Name)
}

// pruneCheckpoints drops the oldest turns' checkpoints past keepTurnCheckpoints,
// and the oldest saved ones past keepSavedCheckpoints. It is best-effort: a
// checkpoint left over costs a ref.
func (m *Manager) pruneCheckpoints(ctx context.Context, a state.Agent) {
	all, err := m.Store.Checkpoints(ctx, a.Project, a.Name)
	if err != nil {
		return
	}
	var turns, saved []state.Checkpoint
	for _, cp := range all {
		if cp.Kind == state.CheckpointSaved {
			saved = append(saved, cp)
		} else {
			turns = append(turns, cp)
		}
	}
	// Turns go by number rather than by age: after a rollback, turn 4 is
	// newer than the turn 9 it replaced, and that turn 9 is already gone.
	for _, list := range [][]state.Checkpoint{oldestTurns(turns, keepTurnCheckpoints), oldest(saved, keepSavedCheckpoints)} {
		for _, cp := range list {
			m.dropCheckpoint(ctx, a, cp)
		}
	}
}

func oldest(list []state.Checkpoint, keep int) []state.Checkpoint {
	if len(list) <= keep {
		return nil
	}
	return list[:len(list)-keep]
}

func oldestTurns(turns []state.Checkpoint, keep int) []state.Checkpoint {
	if len(turns) <= keep {
		return nil
	}
	highest := 0
	for _, cp := range turns {
		highest = max(highest, cp.Number)
	}
	var out []state.Checkpoint
	for _, cp := range turns {
		if cp.Number <= highest-keep {
			out = append(out, cp)
		}
	}
	return out
}

func (m *Manager) dropCheckpoint(ctx context.Context, a state.Agent, cp state.Checkpoint) {
	_ = gitrepo.Repo{Root: a.Worktree}.DeleteRef(cp.Ref)
	_ = m.Store.DeleteCheckpoint(ctx, a.Project, a.Name, cp.ID)
}

// RollBack puts the agent's worktree and branch back to a turn's checkpoint.
// What the worktree has now is saved first as a checkpoint of its own, with
// conversation — the conversation as it stands, which the caller is about to cut —
// for a fork to be told. The checkpoints of the turns after cp are dropped:
// those turns are gone from the conversation, and the next turn to end is
// cp's number plus one again. The chat is the caller's to rewind.
func (m *Manager) RollBack(ctx context.Context, a state.Agent, cp state.Checkpoint, conversation string) (state.Checkpoint, error) {
	if cp.Kind != state.CheckpointTurn {
		return state.Checkpoint{}, fmt.Errorf("%s is what %s had before a rollback, not a turn: fork from it instead", cp.ID, a.Ref())
	}
	repo := gitrepo.Repo{Root: a.Worktree}
	if _, err := repo.ResolveRef(cp.Ref); err != nil {
		return state.Checkpoint{}, fmt.Errorf("%s's checkpoint %s is gone from the repository", a.Ref(), cp.ID)
	}
	all, err := m.Store.Checkpoints(ctx, a.Project, a.Name)
	if err != nil {
		return state.Checkpoint{}, err
	}
	last := cp.Number
	for _, other := range all {
		if other.Kind == state.CheckpointTurn {
			last = max(last, other.Number)
		}
	}
	// Machines write files as their own user, which the host can't remove.
	m.handBackFiles(ctx, a)
	now := time.Now()
	id := "saved-" + now.UTC().Format("20060102-150405")
	ref := "refs/agentbox/pre-restore/" + a.Name + "/" + now.UTC().Format("20060102-150405")
	commit, err := m.snapshotWorktree(ctx, a, ref, "agentbox: "+a.Ref()+" before rolling back to "+cp.ID)
	if err != nil {
		return state.Checkpoint{}, err
	}
	head, _ := repo.ResolveCommit(commit + "^")
	saved := state.Checkpoint{
		Project: a.Project, Agent: a.Name, ID: id, Kind: state.CheckpointSaved,
		Number: last, Prompt: "Before rolling back to turn " + strconv.Itoa(cp.Number),
		Ref: ref, Commit: commit, Head: head, Context: conversation, CreatedAt: now,
	}
	if err := m.Store.SaveCheckpoint(ctx, saved); err != nil {
		return state.Checkpoint{}, err
	}
	m.logf("Rolling %s back to %s (what it had is saved as %s)", a.Ref(), cp.ID, id)
	if err := gitrepo.RestoreWorktree(a.Worktree, cp.Commit); err != nil {
		return state.Checkpoint{}, fmt.Errorf("restoring the worktree: %w", err)
	}
	for _, other := range all {
		if other.Kind == state.CheckpointTurn && other.Number > cp.Number {
			m.dropCheckpoint(ctx, a, other)
		}
	}
	m.pruneCheckpoints(ctx, a)
	return saved, nil
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
