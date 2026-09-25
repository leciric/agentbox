package gitrepo_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/gitrepo"
	"agentbox/internal/testutil"
)

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// snapshotFixture returns a worktree with a commit, an uncommitted edit, an
// untracked file and an ignored file, plus a snapshot of it.
func snapshotFixture(t *testing.T) (repo gitrepo.Repo, wt, head, snapshot string) {
	t.Helper()
	root := testutil.FixtureRepo(t, "hello-stack")
	repo, _ = gitrepo.Open(root)
	base, _ := repo.ResolveCommit("main")
	wt = filepath.Join(t.TempDir(), "agent-01")
	if err := repo.AddWorktree(wt, "agentbox/agent-01", base); err != nil {
		t.Fatal(err)
	}
	write(t, wt, "message.txt", "hello\ncommitted\n")
	testutil.Git(t, wt, "commit", "-qam", "committed")
	head = testutil.Git(t, wt, "rev-parse", "HEAD")
	write(t, wt, "message.txt", "hello\ncommitted\nuncommitted\n")
	write(t, wt, "notes/todo.txt", "untracked\n")
	write(t, wt, ".env", "SECRET=1\n")

	snapshot, err := gitrepo.SnapshotWorktree(wt, "refs/agentbox/snapshots/agent-01/before", "before")
	if err != nil {
		t.Fatal(err)
	}
	return repo, wt, head, snapshot
}

func TestSnapshotAndRestoreWorktree(t *testing.T) {
	repo, wt, head, snapshot := snapshotFixture(t)

	if got, _ := repo.ResolveRef("refs/agentbox/snapshots/agent-01/before"); got != snapshot {
		t.Errorf("snapshot ref = %q, want %q", got, snapshot)
	}
	if staged := testutil.Git(t, wt, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("SnapshotWorktree staged files: %q", staged)
	}

	// Break everything: delete and rename files, commit, add junk, change the ignored .env.
	_ = os.Remove(filepath.Join(wt, "server.mjs"))
	testutil.Git(t, wt, "mv", "README.md", "README.old")
	write(t, wt, "message.txt", "broken\n")
	testutil.Git(t, wt, "add", "-A")
	testutil.Git(t, wt, "commit", "-qm", "break everything")
	write(t, wt, "junk.txt", "junk\n")
	write(t, wt, ".env", "SECRET=changed\n")

	if err := gitrepo.RestoreWorktree(wt, snapshot); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, wt, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD after restore = %s, want %s", got, head)
	}
	for rel, want := range map[string]string{
		"message.txt":    "hello\ncommitted\nuncommitted\n",
		"notes/todo.txt": "untracked\n",
		"README.md":      read(t, repo.Root, "README.md"),
		"server.mjs":     read(t, repo.Root, "server.mjs"),
		"README.old":     "<missing>",
		"junk.txt":       "<missing>",
		".env":           "SECRET=changed\n", // ignored files are left alone
	} {
		if got := read(t, wt, rel); got != want {
			t.Errorf("%s after restore = %q, want %q", rel, got, want)
		}
	}
	if status := testutil.Git(t, wt, "status", "--porcelain"); status != "M message.txt\n?? notes/" {
		t.Errorf("status after restore = %q", status)
	}
}

func TestApplyTreeToNewWorktree(t *testing.T) {
	repo, _, head, snapshot := snapshotFixture(t)

	fork := filepath.Join(t.TempDir(), "agent-02")
	if err := repo.AddWorktree(fork, "agentbox/agent-02", head); err != nil {
		t.Fatal(err)
	}
	if err := gitrepo.ApplyTree(fork, snapshot); err != nil {
		t.Fatal(err)
	}
	if got := read(t, fork, "message.txt"); got != "hello\ncommitted\nuncommitted\n" {
		t.Errorf("message.txt = %q", got)
	}
	if got := read(t, fork, "notes/todo.txt"); got != "untracked\n" {
		t.Errorf("notes/todo.txt = %q", got)
	}
	if got := read(t, fork, ".env"); got != "<missing>" {
		t.Errorf("ignored .env came along: %q", got)
	}
	if got := testutil.Git(t, fork, "rev-parse", "HEAD"); got != head {
		t.Errorf("fork HEAD = %s, want %s", got, head)
	}
}

func TestRefs(t *testing.T) {
	repo, wt, _, _ := snapshotFixture(t)
	if _, err := gitrepo.SnapshotWorktree(wt, "refs/agentbox/snapshots/agent-01/after", "after"); err != nil {
		t.Fatal(err)
	}
	refs, err := repo.Refs("refs/agentbox/snapshots/agent-01/")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"refs/agentbox/snapshots/agent-01/after", "refs/agentbox/snapshots/agent-01/before"}
	if !slices.Equal(refs, want) {
		t.Errorf("Refs() = %q, want %q", refs, want)
	}
	if err := repo.DeleteRef(want[0]); err != nil {
		t.Fatal(err)
	}
	if refs, _ := repo.Refs("refs/agentbox/"); len(refs) != 1 || !strings.HasSuffix(refs[0], "/before") {
		t.Errorf("Refs() after delete = %q", refs)
	}
}
