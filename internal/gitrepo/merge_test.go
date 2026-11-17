package gitrepo_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/gitrepo"
	"agentbox/internal/testutil"
)

// localRepo is a repository with no remote, on main, with one commit and an
// agent's worktree on agentbox/a.
func localRepo(t *testing.T) (gitrepo.Repo, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	write(t, dir, "a.txt", "one\n")
	repo, _, err := gitrepo.Create(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	testutil.Git(t, dir, "worktree", "add", "-q", "-b", "agentbox/a", wt)
	return repo, wt
}

func commit(t *testing.T, dir, file, content, msg string) {
	t.Helper()
	write(t, dir, file, content)
	testutil.Git(t, dir, "add", file)
	testutil.Git(t, dir, "commit", "-q", "-m", msg)
}

func TestMergeFastForwardsTheCheckedOutBaseAndItsFiles(t *testing.T) {
	testutil.GitEnv(t)
	repo, wt := localRepo(t)
	commit(t, wt, "b.txt", "agent\n", "agent's work")
	m, err := repo.Merge("agentbox/a", "main", "")
	if err != nil || !m.FastForward {
		t.Fatalf("Merge() = %+v, %v", m, err)
	}
	if got := testutil.Git(t, repo.Root, "show", "HEAD:b.txt"); got != "agent" {
		t.Errorf("main has b.txt = %q", got)
	}
	if status := testutil.Git(t, repo.Root, "status", "--porcelain"); status != "" {
		t.Errorf("the checkout's files didn't move with main: %q", status)
	}
	if m, err := repo.Merge("agentbox/a", "main", ""); err != nil || !m.UpToDate {
		t.Errorf("merging again = %+v, %v", m, err)
	}
}

func TestMergeMakesAMergeCommitWhenTheBaseMovedOn(t *testing.T) {
	testutil.GitEnv(t)
	repo, wt := localRepo(t)
	commit(t, wt, "b.txt", "agent\n", "agent's work")
	commit(t, repo.Root, "c.txt", "user\n", "user's work")
	m, err := repo.Merge("agentbox/a", "main", "")
	if err != nil || m.FastForward {
		t.Fatalf("Merge() = %+v, %v", m, err)
	}
	if parents := strings.Fields(testutil.Git(t, repo.Root, "log", "-1", "--format=%P")); len(parents) != 2 {
		t.Errorf("merge commit has parents %q", parents)
	}
	if files := testutil.Git(t, repo.Root, "ls-files"); files != "a.txt\nb.txt\nc.txt" {
		t.Errorf("checkout has %q", files)
	}
}

func TestMergeRefusesAConflictAndChangesNothing(t *testing.T) {
	testutil.GitEnv(t)
	repo, wt := localRepo(t)
	commit(t, wt, "a.txt", "agent\n", "agent's work")
	commit(t, repo.Root, "a.txt", "user\n", "user's work")
	head := testutil.Git(t, repo.Root, "rev-parse", "HEAD")
	_, err := repo.Merge("agentbox/a", "main", "")
	var conflict *gitrepo.MergeConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, gitrepo.ErrMergeConflict) || strings.Join(conflict.Files, ",") != "a.txt" {
		t.Fatalf("Merge() = %v, want a conflict in a.txt", err)
	}
	if testutil.Git(t, repo.Root, "rev-parse", "HEAD") != head || testutil.Git(t, repo.Root, "status", "--porcelain") != "" {
		t.Error("a conflict changed main or its checkout")
	}
}

func TestMergeRefusesACheckoutWithChangesButMovesABaseCheckedOutNowhere(t *testing.T) {
	testutil.GitEnv(t)
	repo, wt := localRepo(t)
	commit(t, wt, "b.txt", "agent\n", "agent's work")
	write(t, repo.Root, "a.txt", "editing\n")
	if _, err := repo.Merge("agentbox/a", "main", ""); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("Merge() with the user's changes = %v", err)
	}
	testutil.Git(t, repo.Root, "checkout", "-q", "-b", "elsewhere")
	if _, err := repo.Merge("agentbox/a", "main", ""); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, repo.Root, "rev-parse", "main"); got != testutil.Git(t, repo.Root, "rev-parse", "agentbox/a") {
		t.Error("main didn't move")
	}
}

// Two merges into one branch at once both land: the second reads where the
// first left it rather than racing it.
func TestMergesIntoOneBranchTakeTurns(t *testing.T) {
	testutil.GitEnv(t)
	repo, wt := localRepo(t)
	wt2 := filepath.Join(t.TempDir(), "wt2")
	testutil.Git(t, repo.Root, "worktree", "add", "-q", "-b", "agentbox/b", wt2)
	commit(t, wt, "a2.txt", "a\n", "a")
	commit(t, wt2, "b2.txt", "b\n", "b")
	testutil.Git(t, repo.Root, "checkout", "-q", "-b", "elsewhere") // main checked out nowhere: moved by update-ref
	errs := make(chan error, 2)
	for _, branch := range []string{"agentbox/a", "agentbox/b"} {
		go func() {
			_, err := repo.Merge(branch, "main", "")
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if files := testutil.Git(t, repo.Root, "ls-tree", "--name-only", "main"); files != "a.txt\na2.txt\nb2.txt" {
		t.Errorf("main has %q", files)
	}
}
