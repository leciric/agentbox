package gitrepo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/gitrepo"
	"agentbox/internal/testutil"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenResolvesMainCheckout(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "wt")
	testutil.Git(t, root, "worktree", "add", "-q", "-b", "feature", worktree)

	for _, path := range []string{root, sub, worktree} {
		repo, err := gitrepo.Open(path)
		if err != nil {
			t.Fatalf("Open(%s): %v", path, err)
		}
		if repo.Root != root || repo.GitDir != filepath.Join(root, ".git") {
			t.Errorf("Open(%s) = %+v, want root %s", path, repo, root)
		}
	}
}

func TestCloneCopiesCommitsAndKeepsOrigin(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	testutil.Git(t, root, "remote", "add", "origin", "git@github.com:ana/hello-stack.git")
	write(t, root, "uncommitted.txt", "not copied")
	src, _ := gitrepo.Open(root)

	dest := filepath.Join(t.TempDir(), "src", "hello-stack")
	repo, err := gitrepo.Clone(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root != dest {
		t.Errorf("Clone root = %s, want %s", repo.Root, dest)
	}
	if got, want := testutil.Git(t, dest, "rev-parse", "HEAD"), testutil.Git(t, root, "rev-parse", "HEAD"); got != want {
		t.Errorf("the copy is at %s, the source at %s", got, want)
	}
	if got := testutil.Git(t, dest, "remote", "get-url", "origin"); got != "git@github.com:ana/hello-stack.git" {
		t.Errorf("origin = %q, want the source's", got)
	}
	if got := testutil.Git(t, dest, "remote", "get-url", "windows"); got != root {
		t.Errorf("windows = %q, want %s", got, root)
	}
	if _, err := os.Stat(filepath.Join(dest, "uncommitted.txt")); err == nil {
		t.Error("an uncommitted file was copied")
	}

	if _, err := gitrepo.Clone(src, dest); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Clone onto an existing folder = %v, want it refused", err)
	}
}

func TestCloneWithoutOrigin(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	src, _ := gitrepo.Open(root)
	dest := filepath.Join(t.TempDir(), "copy")
	if _, err := gitrepo.Clone(src, dest); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, dest, "remote"); got != "windows" {
		t.Errorf("remotes = %q, want only windows", got)
	}
}

func TestOpenRejectsNonRepo(t *testing.T) {
	testutil.GitEnv(t)
	if _, err := gitrepo.Open(t.TempDir()); err == nil {
		t.Error("Open(non-repo) succeeded")
	}
}

func TestHasCommitsAndCurrentBranch(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	repo, _ := gitrepo.Open(root)
	if !repo.HasCommits() {
		t.Error("HasCommits() = false for fixture repo")
	}
	if got := repo.CurrentBranch(); got != "main" {
		t.Errorf("CurrentBranch() = %q, want main", got)
	}

	empty := t.TempDir()
	testutil.Git(t, empty, "init", "-q")
	repo, _ = gitrepo.Open(empty)
	if repo.HasCommits() {
		t.Error("HasCommits() = true for empty repo")
	}
}

func TestEnvFiles(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	write(t, root, ".env", "SECRET=1\n")                // ignored by the fixture's .gitignore
	write(t, root, "apps/web/.env", "X=1\n")            // same pattern, nested
	write(t, root, "apps/api/.gitignore", ".env*\n")    //
	write(t, root, "apps/api/.env.local", "SECRET=2\n") // ignored: copied
	write(t, root, "apps/api/.env.bak", "OLD=1\n")      // ignored backup: skipped
	write(t, root, "apps/api/.env.example", "X=\n")     // ignored example: skipped
	write(t, root, "node_modules/pkg/.env", "X=1\n")    // inside an ignored directory
	write(t, root, "notes/.env.production", "X=1\n")    // untracked but not ignored

	repo, _ := gitrepo.Open(root)
	got, err := repo.EnvFiles()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".env", "apps/api/.env.local", "apps/web/.env"}
	if !slices.Equal(got, want) {
		t.Errorf("EnvFiles() = %q, want %q", got, want)
	}
}

func TestListFiles(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	write(t, root, "notes/todo.md", "stuff\n")      // untracked but not ignored
	write(t, root, ".env", "SECRET=1\n")            // ignored by the fixture's .gitignore
	write(t, root, "node_modules/pkg/index.js", "") // inside an ignored directory

	got, truncated, err := gitrepo.ListFiles(root, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("ListFiles(1000) truncated on a handful of files")
	}
	for _, want := range []string{"notes/todo.md"} {
		if !slices.Contains(got, want) {
			t.Errorf("ListFiles() = %q, missing untracked file %q", got, want)
		}
	}
	for _, unwanted := range []string{".env", "node_modules/pkg/index.js"} {
		if slices.Contains(got, unwanted) {
			t.Errorf("ListFiles() = %q, should not contain ignored file %q", got, unwanted)
		}
	}

	if _, truncated, err := gitrepo.ListFiles(root, 1); err != nil {
		t.Fatal(err)
	} else if !truncated {
		t.Error("ListFiles(1) should report truncated with more than one file")
	}
}

func TestWorktreeLifecycle(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	repo, _ := gitrepo.Open(root)
	base, err := repo.ResolveCommit("main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveCommit("no-such-branch"); err == nil {
		t.Error("ResolveCommit(no-such-branch) succeeded")
	}

	wt := filepath.Join(t.TempDir(), "worktrees", "agent-01")
	if err := repo.AddWorktree(wt, "agentbox/agent-01", base); err != nil {
		t.Fatal(err)
	}
	if !repo.BranchExists("agentbox/agent-01") {
		t.Error("branch agentbox/agent-01 missing after AddWorktree")
	}
	if branches, _ := repo.Branches("agentbox"); !slices.Equal(branches, []string{"agentbox/agent-01"}) {
		t.Errorf("Branches(agentbox) = %q", branches)
	}
	if dirty, err := gitrepo.Dirty(wt); err != nil || dirty {
		t.Errorf("fresh worktree: Dirty() = %v, %v", dirty, err)
	}

	write(t, wt, "message.txt", "hello\ncommitted\n")
	testutil.Git(t, wt, "commit", "-qam", "committed change")
	write(t, wt, "message.txt", "hello\ncommitted\nuncommitted\n")
	write(t, wt, "new.txt", "untracked\n")
	if dirty, _ := gitrepo.Dirty(wt); !dirty {
		t.Error("Dirty() = false with uncommitted changes")
	}

	stat, err := gitrepo.DiffWorktree(wt, base, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"message.txt", "new.txt", "2 files changed"} {
		if !strings.Contains(stat, want) {
			t.Errorf("DiffWorktree --stat missing %q:\n%s", want, stat)
		}
	}
	if staged := testutil.Git(t, wt, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("DiffWorktree staged files in the worktree's index: %q", staged)
	}

	if err := repo.RemoveWorktree(wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists: %v", err)
	}
	if !repo.BranchExists("agentbox/agent-01") {
		t.Error("RemoveWorktree deleted the branch")
	}
	if err := repo.DeleteBranch("agentbox/agent-01"); err != nil {
		t.Fatal(err)
	}
	if repo.BranchExists("agentbox/agent-01") {
		t.Error("branch still exists after DeleteBranch")
	}
}

// A `git worktree prune` run where a worktree's path doesn't exist, like inside
// an agent's machine, unregisters it but leaves its files and .git pointer.
// Removing it must still remove the directory, so the path can be reused.
func TestRemoveWorktreeGitForgot(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	repo, _ := gitrepo.Open(root)
	base, err := repo.ResolveCommit("main")
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "worktrees", "lead")
	if err := repo.AddWorktreeDetached(wt, base); err != nil {
		t.Fatal(err)
	}
	if !repo.HasWorktree(wt) {
		t.Fatal("HasWorktree() = false for a fresh worktree")
	}

	elsewhere := wt + ".elsewhere"
	if err := os.Rename(wt, elsewhere); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "worktree", "prune")
	if err := os.Rename(elsewhere, wt); err != nil {
		t.Fatal(err)
	}
	if repo.HasWorktree(wt) {
		t.Error("HasWorktree() = true for a worktree git forgot")
	}

	if err := repo.RemoveWorktree(wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists: %v", err)
	}
	if err := repo.AddWorktreeDetached(wt, base); err != nil {
		t.Errorf("AddWorktreeDetached() at the same path: %v", err)
	}
}

func TestBranchesStartingWithCountsRemotes(t *testing.T) {
	root := testutil.FixtureRepo(t, "hello-stack")
	repo, _ := gitrepo.Open(root)
	testutil.Git(t, root, "branch", "agentbox/agent-01")
	testutil.Git(t, root, "branch", "agentbox/deeper/agent-05")
	testutil.Git(t, root, "branch", "other/agent-06")
	testutil.Git(t, root, "branch", "agent-07")
	testutil.Git(t, root, "remote", "add", "origin", "https://example.invalid/repo.git")
	// A colleague's agents, fetched but never checked out here.
	testutil.Git(t, root, "update-ref", "refs/remotes/origin/agentbox/agent-01", "main")
	testutil.Git(t, root, "update-ref", "refs/remotes/origin/agentbox/agent-02", "main")
	testutil.Git(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	testutil.Git(t, root, "update-ref", "refs/remotes/origin/main", "main")

	got, err := repo.BranchesStartingWith("agentbox/")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"agentbox/agent-01", "agentbox/agent-02"}; !slices.Equal(got, want) {
		t.Errorf("BranchesStartingWith(agentbox/) = %q, want %q", got, want)
	}
	got, _ = repo.BranchesStartingWith("")
	slices.Sort(got)
	if want := []string{"agent-07", "main"}; !slices.Equal(got, want) {
		t.Errorf("BranchesStartingWith(\"\") = %q, want %q", got, want)
	}
	if got, _ := repo.BranchesStartingWith("nobody/"); got != nil {
		t.Errorf("BranchesStartingWith(nobody/) = %q", got)
	}

	if got := repo.BranchInTheWay("agent-07/x/"); got != "agent-07" {
		t.Errorf("BranchInTheWay(agent-07/x/) = %q", got)
	}
	if got := repo.BranchInTheWay("agentbox/"); got != "" {
		t.Errorf("BranchInTheWay(agentbox/) = %q", got)
	}
}

// CheckBranchPrefix is git check-ref-format's rules written out, so it is
// checked against git itself.
func TestCheckBranchPrefixAgreesWithGit(t *testing.T) {
	for _, prefix := range []string{
		"", "agentbox/", "thiago/agentbox/", "thiago-", "a.b/", "a./", "UPPER/",
		"/", "/a/", "a//", "-a/", "a..b/", ".a/", "a/.b/", "a.lock/", "a b/", "a~/", "a^/",
		"a:/", "a?/", "a*/", "a[/", "a\\/", "a@{/", "a\t/", "@/", "é/",
	} {
		err := gitrepo.CheckBranchPrefix(prefix)
		gitErr := exec.Command("git", "check-ref-format", "--branch", prefix+"agent-01").Run()
		if (err == nil) != (gitErr == nil) {
			t.Errorf("CheckBranchPrefix(%q) = %v, but git check-ref-format says %v", prefix, err, gitErr)
		}
	}
}
