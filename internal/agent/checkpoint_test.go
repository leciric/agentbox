package agent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

func checkpointAgent(t *testing.T, f fixture) state.Agent {
	t.Helper()
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", Branch: "agentbox/agent-01", Worktree: filepath.Join(t.TempDir(), "agent-01")}
	if err := f.repo.AddWorktree(a.Worktree, a.Branch, "HEAD", "test"); err != nil {
		t.Fatal(err)
	}
	return a
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestRollBackToACheckpoint: turn 1 left a new file, turn 2 changed it and
// committed; rolling back to turn 1 brings the file and the branch back,
// drops turn 2's checkpoint, and keeps what there was as a saved checkpoint.
func TestRollBackToACheckpoint(t *testing.T) {
	f := setup(t, fakeIncus(t, `exit 0`))
	ctx := context.Background()
	a := checkpointAgent(t, f)
	start := gitIn(t, a.Worktree, "rev-parse", "HEAD")
	notes := filepath.Join(a.Worktree, "notes.txt")

	write(t, notes, "turn one\n")
	one, err := f.m.Checkpoint(ctx, a, "u1", 1, "Write the notes")
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != "turn-1" || one.Head != start || one.Prompt != "Write the notes" {
		t.Fatalf("Checkpoint() = %+v", one)
	}
	if status := gitIn(t, a.Worktree, "status", "--porcelain"); status != "?? notes.txt" {
		t.Fatalf("a checkpoint touched the worktree: status %q", status)
	}
	write(t, notes, "turn two\n")
	gitIn(t, a.Worktree, "add", "notes.txt")
	gitIn(t, a.Worktree, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "turn two")
	if _, err := f.m.Checkpoint(ctx, a, "u2", 2, "Change them"); err != nil {
		t.Fatal(err)
	}

	saved, err := f.m.RollBack(ctx, a, one, "the whole conversation")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(notes); string(got) != "turn one\n" {
		t.Errorf("notes.txt after the rollback = %q", got)
	}
	if head := gitIn(t, a.Worktree, "rev-parse", "HEAD"); head != start {
		t.Errorf("the branch is on %s, want %s", head, start)
	}
	list, err := f.m.Checkpoints(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, cp := range list {
		ids = append(ids, cp.ID)
	}
	if len(list) != 2 || list[0].ID != "turn-1" || list[1].ID != saved.ID || saved.Kind != state.CheckpointSaved || saved.Context != "the whole conversation" || saved.Number != 2 {
		t.Fatalf("checkpoints after the rollback = %v, saved %+v", ids, saved)
	}
	if _, err := f.repo.ResolveRef("refs/agentbox/snapshots/agent-01/turn-2"); err == nil {
		t.Error("turn-2's ref outlived the rollback")
	}
	// What turn 2 had is in the saved checkpoint: its commit and its file.
	if got := gitIn(t, a.Worktree, "show", saved.Commit+":notes.txt"); got != "turn two" {
		t.Errorf("the saved checkpoint has notes.txt = %q", got)
	}
	if _, err := f.m.RollBack(ctx, a, saved, ""); err == nil || !strings.Contains(err.Error(), "fork from it") {
		t.Errorf("rolling back to a saved checkpoint: %v", err)
	}
}

// TestCheckpointsArePruned keeps the latest turns' checkpoints only.
func TestCheckpointsArePruned(t *testing.T) {
	f := setup(t, fakeIncus(t, `exit 0`))
	ctx := context.Background()
	a := checkpointAgent(t, f)
	n := agent.KeepTurnCheckpoints + 2
	for i := 1; i <= n; i++ {
		if _, err := f.m.Checkpoint(ctx, a, "u", i, ""); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := f.m.Checkpoints(ctx, a)
	if len(list) != agent.KeepTurnCheckpoints || list[0].Number != 3 {
		t.Fatalf("kept %d checkpoints from turn %d, want %d from turn 3", len(list), list[0].Number, agent.KeepTurnCheckpoints)
	}
	if _, err := f.repo.ResolveRef("refs/agentbox/snapshots/agent-01/turn-2"); err == nil {
		t.Error("a pruned checkpoint kept its ref")
	}
}

func TestCheckpointID(t *testing.T) {
	for in, want := range map[string]string{"3": "turn-3", "turn-3": "turn-3", "saved-x": "saved-x", "0": "0"} {
		if got := agent.CheckpointID(in); got != want {
			t.Errorf("CheckpointID(%q) = %q, want %q", in, got, want)
		}
	}
}
