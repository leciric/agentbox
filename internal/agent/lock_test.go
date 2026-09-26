package agent_test

import (
	"context"
	"os"
	"testing"

	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// TestLockAgentWorktreeSurvivesPrune checks the fix for one agent's
// `git worktree prune` unregistering every other agent's worktree, because
// their paths don't exist inside its own machine: an agent's worktree is
// locked when it's created, and LockAgentWorktree — what the daemon calls for
// every agent when it starts — relocks one that somehow lost its lock,
// without erroring on one that's locked already.
func TestLockAgentWorktreeSurvivesPrune(t *testing.T) {
	f := setup(t, fakeIncus(t, ""))
	ctx := context.Background()
	a := destroyFixture(t, f)

	// destroyFixture's worktree is already locked by AddWorktree; relocking it
	// (what the daemon does for every agent on start) must not error.
	if err := f.m.LockAgentWorktree(ctx, a); err != nil {
		t.Fatalf("LockAgentWorktree() on an already-locked worktree: %v", err)
	}

	// Simulate running `git worktree prune` from inside this agent's own
	// machine, where every other agent's worktree path doesn't exist.
	elsewhere := a.Worktree + ".elsewhere"
	if err := os.Rename(a.Worktree, elsewhere); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.repo.Root, "worktree", "prune")
	if err := os.Rename(elsewhere, a.Worktree); err != nil {
		t.Fatal(err)
	}

	if !f.repo.HasWorktree(a.Worktree) {
		t.Error("prune unregistered a locked worktree")
	}

	// LockAgentWorktree on an agent whose worktree was actually removed (not
	// merely made unreachable) must not error either.
	if err := f.m.LockAgentWorktree(ctx, state.Agent{Project: "hello-stack", Name: "agent-99", Worktree: "/does/not/exist"}); err != nil {
		t.Errorf("LockAgentWorktree() on a missing worktree: %v", err)
	}
}
