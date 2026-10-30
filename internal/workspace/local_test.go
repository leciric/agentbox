package workspace_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/gitrepo"
	"agentbox/internal/testutil"
	"agentbox/internal/workspace"
)

// setup makes a project repository and a workspace for a new agent in it, on
// branch agentbox/fix-login from main.
func setup(t *testing.T) (repo gitrepo.Repo, w workspace.Local) {
	t.Helper()
	root := testutil.FixtureRepo(t, "hello-stack")
	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	base := testutil.Git(t, root, "rev-parse", "HEAD")
	dir := filepath.Join(t.TempDir(), "worktrees", "hello-stack", "agent-01")
	w = workspace.Local{Dir: dir}
	if err := w.Prepare(context.Background(), repo, workspace.PrepareOptions{
		Branch: "agentbox/fix-login",
		Base:   base,
		Reason: "agentbox: hello-stack/agent-01",
	}); err != nil {
		t.Fatal(err)
	}
	return repo, w
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareMakesTheBranchWorktreeAndEnv(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	base := testutil.Git(t, root, "rev-parse", "HEAD")
	write(t, filepath.Join(root, ".env"), "TOKEN=from-the-project\n")

	dir := filepath.Join(t.TempDir(), "worktrees", "agent-01")
	w := workspace.Local{Dir: dir}
	if err := w.Prepare(context.Background(), repo, workspace.PrepareOptions{
		Branch:  "agentbox/fix-login",
		Base:    base,
		EnvFrom: root,
		Env:     []string{".env"},
		Reason:  "agentbox: hello-stack/agent-01",
	}); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "agentbox/fix-login" {
		t.Errorf("the worktree is on branch %q, want agentbox/fix-login", got)
	}
	if got := testutil.Git(t, dir, "rev-parse", "HEAD"); got != base {
		t.Errorf("the branch starts at %q, want %q", got, base)
	}
	if got := read(t, filepath.Join(dir, ".env")); got != "TOKEN=from-the-project\n" {
		t.Errorf("the worktree's .env = %q, want the project's", got)
	}
	if !repo.HasWorktree(dir) {
		t.Error("git doesn't know the worktree Prepare made")
	}
	if w.Path() != dir {
		t.Errorf("Path() = %q, want %q", w.Path(), dir)
	}
}

// A tree on top of the base is where a fork starts: the source's uncommitted
// and untracked files are the fork's, still uncommitted.
func TestPrepareAppliesATreeAsUncommittedChanges(t *testing.T) {
	repo, _ := setup(t)
	root := repo.Root
	// A commit holding a README and a file main doesn't have.
	write(t, filepath.Join(root, "README.md"), "# changed by the snapshot\n")
	write(t, filepath.Join(root, "notes.md"), "from the snapshot\n")
	testutil.Git(t, root, "add", "README.md", "notes.md")
	testutil.Git(t, root, "commit", "-q", "-m", "snapshot")
	tree := testutil.Git(t, root, "rev-parse", "HEAD")
	base := testutil.Git(t, root, "rev-parse", "HEAD^")

	dir := filepath.Join(t.TempDir(), "worktrees", "agent-02")
	w := workspace.Local{Dir: dir}
	if err := w.Prepare(context.Background(), repo, workspace.PrepareOptions{
		Branch: "agentbox/from-a-snapshot",
		Base:   base,
		Tree:   tree,
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "README.md")); !strings.Contains(got, "changed by the snapshot") {
		t.Errorf("README.md = %q, want the snapshot's", got)
	}
	status := testutil.Git(t, dir, "status", "--porcelain")
	for _, want := range []string{"README.md", "notes.md"} {
		if !strings.Contains(status, want) {
			t.Errorf("status %q doesn't show %s as uncommitted", status, want)
		}
	}
	if got := testutil.Git(t, dir, "rev-parse", "HEAD"); got != base {
		t.Errorf("HEAD = %q, want the base %q: the tree is uncommitted", got, base)
	}
}

func TestPrepareThatFailsLeavesNothingBehind(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "worktrees", "agent-01")
	w := workspace.Local{Dir: dir}
	err = w.Prepare(context.Background(), repo, workspace.PrepareOptions{
		Branch:  "agentbox/fix-login",
		Base:    testutil.Git(t, root, "rev-parse", "HEAD"),
		EnvFrom: root,
		Env:     []string{".env.missing"},
	})
	var step *workspace.StepError
	if !errors.As(err, &step) || step.Step != "env files" {
		t.Fatalf("Prepare() error = %v, want a StepError naming env files", err)
	}
	if repo.HasWorktree(dir) {
		t.Error("a failed Prepare left the worktree registered")
	}
	if repo.BranchExists("agentbox/fix-login") {
		t.Error("a failed Prepare left its branch behind")
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a failed Prepare left %s behind: %v", dir, err)
	}
}

func TestStatusFollowsTheWorktree(t *testing.T) {
	repo, w := setup(t)
	ctx := context.Background()
	st, err := w.Status(ctx)
	if err != nil || !st.Exists || st.Dirty {
		t.Fatalf("Status() on a fresh worktree = %+v, %v, want there and clean", st, err)
	}
	write(t, filepath.Join(w.Path(), "notes.md"), "one\n")
	if st, err = w.Status(ctx); err != nil || !st.Exists || !st.Dirty {
		t.Fatalf("Status() after a new file = %+v, %v, want there and dirty", st, err)
	}
	if err := w.Destroy(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if st, err = w.Status(ctx); err != nil || st.Exists {
		t.Fatalf("Status() after Destroy = %+v, %v, want gone without an error", st, err)
	}
}

func TestFilesListsTrackedAndNewFiles(t *testing.T) {
	_, w := setup(t)
	write(t, filepath.Join(w.Path(), "notes.md"), "one\n")
	files, truncated, err := w.Files(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"README.md", "notes.md"} {
		if !contains(files, want) {
			t.Errorf("Files() = %v, want it to hold %s", files, want)
		}
	}
	if truncated {
		t.Error("Files() says a small tree was truncated")
	}
	if files, truncated, err = w.Files(context.Background(), 1); err != nil || len(files) != 1 || !truncated {
		t.Errorf("Files(limit 1) = %v, %v, %v, want one file and truncated", files, truncated, err)
	}
}

func TestReadFileStaysInsideTheWorktree(t *testing.T) {
	_, w := setup(t)
	ctx := context.Background()
	if got, err := w.ReadFile(ctx, "README.md"); err != nil || !strings.Contains(string(got), "hello-stack") {
		t.Errorf("ReadFile(README.md) = %q, %v, want the fixture's README", got, err)
	}
	for _, bad := range []string{"", ".", "..", "../outside", "/etc/passwd", "scripts/../../outside"} {
		if _, err := w.ReadFile(ctx, bad); err == nil {
			t.Errorf("ReadFile(%q) succeeded, want it refused", bad)
		}
	}
	if _, err := w.ReadFile(ctx, "nope.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile(nope.md) error = %v, want not found", err)
	}
}

func TestCheckpointRecordsWithoutTouchingTheWorktree(t *testing.T) {
	repo, w := setup(t)
	ctx := context.Background()
	base := testutil.Git(t, w.Path(), "rev-parse", "HEAD")
	write(t, filepath.Join(w.Path(), "README.md"), "# agent work\n")
	write(t, filepath.Join(w.Path(), "notes.md"), "untracked\n")

	commit, err := w.Checkpoint(ctx, "refs/agentbox/snapshots/agent-01/turn-1", "agentbox checkpoint hello-stack/agent-01@turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repo.ResolveRef("refs/agentbox/snapshots/agent-01/turn-1"); err != nil || got != commit {
		t.Errorf("the ref = %q, %v, want %q", got, err, commit)
	}
	if got := testutil.Git(t, w.Path(), "rev-parse", commit+"^"); got != base {
		t.Errorf("the checkpoint's parent = %q, want the branch's commit %q", got, base)
	}
	if got := testutil.Git(t, w.Path(), "show", commit+":README.md"); got != "# agent work" {
		t.Errorf("the checkpoint's README.md = %q, want the worktree's", got)
	}
	if got := testutil.Git(t, w.Path(), "show", commit+":notes.md"); got != "untracked" {
		t.Errorf("the checkpoint's notes.md = %q, want the worktree's", got)
	}
	// The worktree is untouched: same branch, same commit, still dirty.
	if got := testutil.Git(t, w.Path(), "rev-parse", "HEAD"); got != base {
		t.Errorf("HEAD = %q after a checkpoint, want %q", got, base)
	}
	if status := testutil.Git(t, w.Path(), "status", "--porcelain"); !strings.Contains(status, "README.md") {
		t.Errorf("status %q after a checkpoint, want the worktree still changed", status)
	}
}

func TestSnapshotIsAWorktreeCommitLikeACheckpoint(t *testing.T) {
	repo, w := setup(t)
	write(t, filepath.Join(w.Path(), "notes.md"), "snapshotted\n")
	commit, err := w.Snapshot(context.Background(), "refs/agentbox/snapshots/agent-01/initial", "agentbox snapshot hello-stack/agent-01@initial")
	if err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, w.Path(), "show", commit+":notes.md"); got != "snapshotted" {
		t.Errorf("the snapshot's notes.md = %q, want the worktree's", got)
	}
	if got, err := repo.ResolveRef("refs/agentbox/snapshots/agent-01/initial"); err != nil || got != commit {
		t.Errorf("the ref = %q, %v, want %q", got, err, commit)
	}
}

func TestRestorePutsTheBranchAndFilesBackAndKeepsIgnoredOnes(t *testing.T) {
	repo, w := setup(t)
	ctx := context.Background()
	write(t, filepath.Join(w.Path(), "notes.md"), "at the checkpoint\n")
	commit, err := w.Checkpoint(ctx, "refs/agentbox/snapshots/agent-01/turn-1", "checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	// Work after the checkpoint, and something ignored the agent built.
	write(t, filepath.Join(w.Path(), "README.md"), "# later work\n")
	write(t, filepath.Join(w.Path(), "later.md"), "later\n")
	testutil.Git(t, w.Path(), "add", "README.md")
	testutil.Git(t, w.Path(), "commit", "-q", "-m", "later")
	write(t, filepath.Join(w.Path(), "test-results.xml"), "ignored\n")
	write(t, filepath.Join(w.Path(), "node_modules", "kept.txt"), "kept\n")

	if err := w.Restore(ctx, commit); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, w.Path(), "rev-parse", "HEAD"); got != testutil.Git(t, repo.Root, "rev-parse", commit+"^") {
		t.Errorf("HEAD = %q after a restore, want the checkpoint's parent", got)
	}
	if got := read(t, filepath.Join(w.Path(), "notes.md")); got != "at the checkpoint\n" {
		t.Errorf("notes.md = %q after a restore, want the checkpoint's", got)
	}
	if _, err := os.Stat(filepath.Join(w.Path(), "later.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("later.md is still there after the restore: %v", err)
	}
	if got := read(t, filepath.Join(w.Path(), "node_modules", "kept.txt")); got != "kept\n" {
		t.Error("the restore removed an ignored file")
	}
}

func TestDiffShowsWhatChangedAgainstTheBase(t *testing.T) {
	_, w := setup(t)
	ctx := context.Background()
	write(t, filepath.Join(w.Path(), "README.md"), "# agent work\n")
	write(t, filepath.Join(w.Path(), "notes.md"), "untracked\n")

	diff, err := w.Diff(ctx, workspace.DiffOptions{BaseRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-# hello-stack", "+# agent work", "+untracked"} {
		if !strings.Contains(diff, want) {
			t.Errorf("Diff() = %q, want it to hold %q", diff, want)
		}
	}
	stat, err := w.Diff(ctx, workspace.DiffOptions{BaseRef: "main", Stat: true})
	if err != nil || !strings.Contains(stat, "README.md") {
		t.Errorf("Diff(Stat) = %q, %v, want README.md's summary", stat, err)
	}
	one, err := w.Diff(ctx, workspace.DiffOptions{BaseRef: "main", Paths: []string{"notes.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(one, "README.md") || !strings.Contains(one, "untracked") {
		t.Errorf("Diff(paths notes.md) = %q, want notes.md alone", one)
	}
}

func TestDestroyRemovesTheWorktree(t *testing.T) {
	repo, w := setup(t)
	write(t, filepath.Join(w.Path(), "notes.md"), "one\n")
	if err := w.Destroy(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.Path()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s is still there after Destroy: %v", w.Path(), err)
	}
	if repo.HasWorktree(w.Path()) {
		t.Error("git still knows the worktree Destroy removed")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
