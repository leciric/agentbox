package agent_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/memory"
	"agentbox/internal/testutil"
)

// TestNextNameSkipsLeftoverWorktreeDirectory covers the floor's worktree
// source: a directory named agent-01 left on disk (no agent row, no branch)
// is still enough to keep its number from being handed out again.
func TestNextNameSkipsLeftoverWorktreeDirectory(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  query) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
  copy) echo "Error: simulated copy failure" >&2; exit 1 ;;
esac`))
	ctx := context.Background()
	leftover := f.m.Paths.Worktree("hello-stack", "agent-01")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
	if err == nil {
		t.Fatal("Create() succeeded, want the simulated copy failure")
	}
	if !strings.Contains(err.Error(), "hello-stack/agent-02") {
		t.Errorf("Create() should skip agent-01's leftover worktree: %v", err)
	}
}

// TestNextNameSkipsPastAgentEvents covers the floor's other source: a
// project's memory (its events, here, or its reports) mentioning an agent
// that doesn't exist any more.
func TestNextNameSkipsPastAgentEvents(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  query) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
  copy) echo "Error: simulated copy failure" >&2; exit 1 ;;
esac`))
	ctx := context.Background()

	mem := memory.New(f.st.DB())
	if _, err := mem.AppendEvent(ctx, memory.Event{Project: "hello-stack", Agent: "agent-04", Type: "created"}); err != nil {
		t.Fatal(err)
	}

	_, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
	if err == nil {
		t.Fatal("Create() succeeded, want the simulated copy failure")
	}
	if !strings.Contains(err.Error(), "hello-stack/agent-05") {
		t.Errorf("Create() should skip agent-04's number, seen only in the project's memory: %v", err)
	}
}

// TestNextNameSkipsRemoteBranch covers the floor's branch source for a
// remote's branch, not just a local one: a colleague sharing the repository
// can claim a number this machine never checked out.
func TestNextNameSkipsRemoteBranch(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  query) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
  copy) echo "Error: simulated copy failure" >&2; exit 1 ;;
esac`))
	ctx := context.Background()
	testutil.Git(t, f.repo.Root, "remote", "add", "origin", "https://example.invalid/repo.git")
	testutil.Git(t, f.repo.Root, "update-ref", "refs/remotes/origin/agentbox/agent-03", "main")

	_, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
	if err == nil {
		t.Fatal("Create() succeeded, want the simulated copy failure")
	}
	if !strings.Contains(err.Error(), "hello-stack/agent-04") {
		t.Errorf("Create() should skip agent-03's number, claimed only on a remote: %v", err)
	}
}

// TestCreateConcurrentGetsDistinctNames is the race fixed alongside name
// reuse: two Create calls landing at the same moment must never both pick
// agent-07 and have the second fail with "already exists". The copy step
// fails on purpose, so this only has to wait on the name reservation itself,
// not on a whole machine coming up; build() reserves the name (and rolls the
// reservation's own side effects back on failure, but never the number
// itself) before it ever calls incus.
func TestCreateConcurrentGetsDistinctNames(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  query) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
  copy) echo "Error: simulated copy failure" >&2; exit 1 ;;
esac`))
	ctx := context.Background()

	const n = 5
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "none"})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "simulated copy failure") {
			t.Fatalf("create %d: %v, want the simulated failure", i, err)
		}
		name, ok := agentNameIn(err.Error())
		if !ok {
			t.Fatalf("create %d: no hello-stack/agent-NN in %v", i, err)
		}
		if seen[name] {
			t.Fatalf("name %q handed out twice", name)
		}
		seen[name] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct names, want %d", len(seen), n)
	}
}

// agentNameIn finds this package's own "hello-stack/agent-NN" in an error
// build() wrapped with a.Ref().
func agentNameIn(s string) (string, bool) {
	const prefix = "hello-stack/agent-"
	i := strings.Index(s, prefix)
	if i < 0 {
		return "", false
	}
	s = s[i:]
	end := len(prefix)
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	return s[:end], true
}
