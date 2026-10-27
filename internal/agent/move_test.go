package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/testutil"
)

// TestCheckpointForMoveRecordsTheWorktreeOnARefOfItsOwn: a move's checkpoint is
// the worktree as a commit on refs/agentbox/moves/<agent>/<time>, pushed by the
// pod's workspace, and the branch it was taken from is left where it was. It is
// not a turn, so it is not one of the checkpoints a rollback or a fork lists.
func TestCheckpointForMoveRecordsTheWorktreeOnARefOfItsOwn(t *testing.T) {
	f, a, dir := clusterTree(t)
	ctx := context.Background()
	head := testutil.Git(t, dir, "rev-parse", "HEAD")
	writeTree(t, filepath.Join(dir, "notes.md"), "from the pod, before the move\n")
	// A checkpoint the conversation took, which the move's is not.
	if _, err := f.m.Checkpoint(ctx, a, "u1", 1, "Write the notes"); err != nil {
		t.Fatalf("Checkpoint() = %v", err)
	}

	commit, err := f.m.CheckpointForMove(ctx, a, "k3s")
	if err != nil {
		t.Fatalf("CheckpointForMove() = %v", err)
	}
	if commit == "" {
		t.Fatal("CheckpointForMove() made no commit")
	}
	if got := testutil.Git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("the branch moved to %q, want it still at %q", got, head)
	}
	if got := testutil.Git(t, dir, "show", commit+":notes.md"); !strings.Contains(got, "before the move") {
		t.Errorf("the commit holds %q, not the worktree's file", got)
	}
	refs := testutil.Git(t, dir, "for-each-ref", "--format=%(refname)", "refs/agentbox/moves/")
	if !strings.Contains(refs, "refs/agentbox/moves/agent-01/") {
		t.Errorf("the checkpoint is not on a move ref: %v", refs)
	}
	if !ranCommand(f.pod.userExecs, "push", "--quiet", "origin", "refs/agentbox/moves/agent-01/") {
		t.Errorf("the checkpoint wasn't published, so it would not outlive the pod: %v", f.pod.userExecs)
	}

	// The turn checkpoints stay what they were: a move is not part of the
	// conversation, so nothing lists it as one a rollback may go back to.
	checkpoints, err := f.m.Checkpoints(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 1 || checkpoints[0].ID != "turn-1" {
		t.Errorf("Checkpoints() = %+v, want only the conversation's turn", checkpoints)
	}
}
