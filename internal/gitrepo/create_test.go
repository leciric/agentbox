package gitrepo_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/gitrepo"
	"agentbox/internal/testutil"
)

func TestCreateMakesARepositoryOnMainWithACommit(t *testing.T) {
	testutil.GitEnv(t)
	for _, tc := range []struct {
		name  string
		setup func(dir string)
	}{
		{"missing", func(dir string) { _ = os.MkdirAll(filepath.Dir(dir), 0o755) }},
		{"missing with missing parents", func(string) {}},
		{"empty", func(dir string) { _ = os.MkdirAll(dir, 0o755) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "code", "my-app")
			tc.setup(dir)
			_, existed := os.Stat(dir)
			repo, created, err := gitrepo.Create(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			if repo.Root != dir || !repo.HasCommits() || repo.CurrentBranch() != "main" {
				t.Errorf("Create() = %+v on %s, with commits %v", repo, repo.CurrentBranch(), repo.HasCommits())
			}
			if created != (existed != nil) {
				t.Errorf("created = %v, but the folder existed: %v", created, existed == nil)
			}
			if got := testutil.Git(t, dir, "log", "--format=%s"); got != "Initial commit" {
				t.Errorf("log = %q", got)
			}
		})
	}
}

func TestCreateOpensARepositoryThatHasCommits(t *testing.T) {
	testutil.GitEnv(t)
	root := testutil.FixtureRepo(t, "hello-stack")
	head := testutil.Git(t, root, "rev-parse", "HEAD")
	repo, created, err := gitrepo.Create(root, false)
	if err != nil || repo.Root != root || created {
		t.Fatalf("Create() = %+v, %v, %v", repo, created, err)
	}
	if got := testutil.Git(t, root, "rev-parse", "HEAD"); got != head {
		t.Error("it committed to a repository that already had commits")
	}
}

func TestCreateCommitsFilesOnlyWhenAsked(t *testing.T) {
	testutil.GitEnv(t)
	for _, repoFirst := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "notes")
		write(t, dir, "README.md", "# notes\n")
		write(t, dir, ".gitignore", ".env\n")
		write(t, dir, ".env", "SECRET=1\n")
		if repoFirst {
			testutil.Git(t, dir, "init", "-q", "-b", "trunk")
		}

		if _, _, err := gitrepo.Create(dir, false); !errors.Is(err, gitrepo.ErrNotEmpty) {
			t.Fatalf("Create(%v) = %v, want ErrNotEmpty", repoFirst, err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); (err == nil) != repoFirst {
			t.Errorf("refusing left .git behind: %v", err)
		}

		repo, created, err := gitrepo.Create(dir, true)
		if err != nil || created {
			t.Fatalf("Create(%v, commit) = %+v, %v, %v", repoFirst, repo, created, err)
		}
		files := testutil.Git(t, dir, "ls-tree", "--name-only", "HEAD")
		if files != ".gitignore\nREADME.md" {
			t.Errorf("committed %q, want .gitignore and README.md", files)
		}
		// A repository that already had a branch keeps it.
		if want := map[bool]string{false: "main", true: "trunk"}[repoFirst]; repo.CurrentBranch() != want {
			t.Errorf("branch = %s, want %s", repo.CurrentBranch(), want)
		}
	}
}

func TestCreateRefusesAFolderInsideARepository(t *testing.T) {
	testutil.GitEnv(t)
	root := testutil.FixtureRepo(t, "hello-stack")
	for _, dir := range []string{filepath.Join(root, "new"), filepath.Join(root, "a", "b")} {
		_, _, err := gitrepo.Create(dir, true)
		if err == nil || !strings.Contains(err.Error(), "inside the repository "+root) {
			t.Errorf("Create(%s) = %v", dir, err)
		}
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("%s was made anyway", dir)
		}
	}
}

func TestCreateRefusesAFile(t *testing.T) {
	testutil.GitEnv(t)
	dir := t.TempDir()
	write(t, dir, "file", "x")
	if _, _, err := gitrepo.Create(filepath.Join(dir, "file"), true); err == nil || !strings.Contains(err.Error(), "is a file") {
		t.Errorf("Create(file) = %v", err)
	}
}
