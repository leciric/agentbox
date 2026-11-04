package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"agentbox/internal/testutil"
)

// TestPersistentIndexGivesTheSameTree: a worktree's tree through its
// persistent index is the one a throwaway copy gives, however the files
// change between calls, and a damaged index is replaced by the copy.
func TestPersistentIndexGivesTheSameTree(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	repo, _ := Open(root)
	base, _ := repo.ResolveCommit("main")
	worktree := filepath.Join(t.TempDir(), "agent-01")
	if err := repo.AddWorktree(worktree, "agentbox/agent-01", base, "test"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "idx")
	SetIndexDir(dir)
	t.Cleanup(func() { SetIndexDir("") })

	check := func(what string) {
		t.Helper()
		got, err := worktreeTree(worktree)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		want, err := copiedTree(worktree)
		if err != nil || got != want {
			t.Fatalf("%s: tree %s, want %s (%v)", what, got, want, err)
		}
	}
	check("clean")
	files, _ := filepath.Glob(filepath.Join(dir, "*.index"))
	if len(files) != 1 {
		t.Fatalf("persistent index files = %v, want one", files)
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(worktree, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "a\n")
	check("new file")
	write("a.txt", "changed\n")
	check("edited in the same second")
	if err := os.Remove(filepath.Join(worktree, "a.txt")); err != nil {
		t.Fatal(err)
	}
	check("removed")
	// Untracked in the worktree's index but still on disk, and ignored.
	write("kept.log", "x\n")
	write(".gitignore", "*.log\n")
	check("ignored")
	if out, err := exec.Command("git", "-C", worktree, "add", "-f", "kept.log").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	check("ignored but tracked")
	if out, err := exec.Command("git", "-C", worktree, "rm", "-q", "--cached", "kept.log").CombinedOutput(); err != nil {
		t.Fatalf("git rm: %v\n%s", err, out)
	}
	check("no longer tracked")
	if err := os.WriteFile(files[0], []byte("not an index"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("damaged")
}
