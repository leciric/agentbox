package gitrepo_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/gitrepo"
	"agentbox/internal/testutil"
)

// plainFolder is a project that was never a repository: code, its installed
// dependencies, a build, a cache and one file too big for git.
func plainFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	write(t, dir, "package.json", "{}\n")
	write(t, dir, "src/index.js", "console.log(1)\n")
	write(t, dir, "node_modules/left-pad/index.js", strings.Repeat("x", 1000))
	write(t, dir, "web/node_modules/react/index.js", "r")
	write(t, dir, "tools/Cargo.toml", "[package]\n")
	write(t, dir, "tools/target/debug/tool", "bin")
	write(t, dir, "target/notes.md", "a folder called target with no Cargo.toml is source\n")
	write(t, dir, "py/__pycache__/m.pyc", "c")
	write(t, dir, "env/pyvenv.cfg", "home = /usr\n")
	write(t, dir, "env/lib/site.py", "s")
	big := filepath.Join(dir, "data", "dump.sql")
	write(t, dir, "data/dump.sql", "")
	if err := os.Truncate(big, gitrepo.LargeFile); err != nil { // sparse: big to stat, nothing on disk
		t.Fatal(err)
	}
	return dir
}

func paths(skipped []gitrepo.Skipped) []string {
	var out []string
	for _, s := range skipped {
		out = append(out, s.Path)
	}
	slices.Sort(out)
	return out
}

func TestHeavyFindsDependenciesBuildsCachesAndLargeFiles(t *testing.T) {
	testutil.GitEnv(t)
	dir := plainFolder(t)
	skipped, err := gitrepo.Heavy(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"data/dump.sql", "env", "node_modules", "py/__pycache__", "tools/target", "web/node_modules"}
	if got := paths(skipped); !slices.Equal(got, want) {
		t.Errorf("Heavy() = %q, want %q", got, want)
	}
	if skipped[0].Path != "data/dump.sql" || skipped[0].Reason != "large-file" {
		t.Errorf("biggest first: %+v", skipped[0])
	}
	if i := slices.IndexFunc(skipped, func(s gitrepo.Skipped) bool { return s.Path == "node_modules" }); skipped[i].Bytes != 1000 || skipped[i].Reason != "dependencies" {
		t.Errorf("node_modules = %+v", skipped[i])
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Error("Heavy made a repository")
	}
}

func TestHeavyLeavesOutWhatGitignoreAlreadyDoes(t *testing.T) {
	testutil.GitEnv(t)
	dir := plainFolder(t)
	write(t, dir, ".gitignore", "node_modules/\n*.sql\n")
	skipped, err := gitrepo.Heavy(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths(skipped), []string{"env", "py/__pycache__", "tools/target"}; !slices.Equal(got, want) {
		t.Errorf("Heavy() = %q, want %q", got, want)
	}
}

func TestCreateOnAPlainFolderCommitsItsFilesAndLeavesTheHeavyOnesOut(t *testing.T) {
	testutil.GitEnv(t)
	dir := plainFolder(t)
	if _, err := gitrepo.Open(dir); !errors.Is(err, gitrepo.ErrNotARepo) {
		t.Fatalf("Open() = %v, want ErrNotARepo", err)
	}
	repo, created, err := gitrepo.Create(dir, true)
	if err != nil || created || repo.CurrentBranch() != "main" {
		t.Fatalf("Create() = %+v, %v, %v", repo, created, err)
	}
	files := strings.Fields(testutil.Git(t, dir, "ls-tree", "-r", "--name-only", "HEAD"))
	if want := []string{"package.json", "src/index.js", "target/notes.md", "tools/Cargo.toml"}; !slices.Equal(files, want) {
		t.Errorf("committed %q, want %q", files, want)
	}
	// Left out for good, worktrees included, without touching the user's files.
	if status := testutil.Git(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("status after the first commit = %q", status)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err == nil {
		t.Error("it wrote a .gitignore into the user's folder")
	}
	exclude, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if !strings.Contains(string(exclude), "/node_modules/\n") || !strings.Contains(string(exclude), "/data/dump.sql\n") {
		t.Errorf("info/exclude = %q", exclude)
	}
}

func TestHeavyLeavesOutNestedRepositories(t *testing.T) {
	testutil.GitEnv(t)
	dir := filepath.Join(t.TempDir(), "work")
	write(t, dir, "README.md", "# work\n")
	write(t, dir, "vendor/lib/lib.go", "package lib\n")
	testutil.Git(t, filepath.Join(dir, "vendor", "lib"), "init", "-q")
	write(t, dir, "linked/x.txt", "x\n")
	write(t, dir, "linked/.git", "gitdir: /somewhere/else\n") // a worktree's or submodule's .git file
	skipped, err := gitrepo.Heavy(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths(skipped), []string{"linked", "vendor/lib"}; !slices.Equal(got, want) {
		t.Fatalf("Heavy() = %q, want %q", got, want)
	}
	for _, s := range skipped {
		if s.Reason != "repository" {
			t.Errorf("%s: reason %q", s.Path, s.Reason)
		}
	}
	if _, _, err := gitrepo.Create(dir, true); err != nil {
		t.Fatal(err)
	}
	// No empty gitlink in its place: the nested ones aren't in the commit at all.
	if got := testutil.Git(t, dir, "ls-tree", "-r", "--name-only", "HEAD"); got != "README.md" {
		t.Errorf("committed %q", got)
	}
}

func TestHeavyGivesUpOnAFolderTooBigToRead(t *testing.T) {
	testutil.GitEnv(t)
	old := gitrepo.HeavyEntries
	gitrepo.HeavyEntries = 5
	t.Cleanup(func() { gitrepo.HeavyEntries = old })
	dir := t.TempDir()
	for i := range 3 {
		write(t, dir, filepath.Join("node_modules", strings.Repeat("p", i+1), "index.js"), "x")
	}
	if _, err := gitrepo.Heavy(dir); !errors.Is(err, gitrepo.ErrTooBig) {
		t.Errorf("Heavy() = %v, want ErrTooBig", err)
	}
	if _, _, err := gitrepo.Create(dir, true); !errors.Is(err, gitrepo.ErrTooBig) {
		t.Errorf("Create() = %v, want ErrTooBig", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Error("giving up left a .git behind")
	}
}

func TestTooBroad(t *testing.T) {
	home := "/home/ana"
	for path, broad := range map[string]bool{
		"/": true, "/usr": true, "/home": true, "/home/ana": true, "/home/ana/": true, "/mnt/c": true,
		"/home/ana/code/site": false, "/home/ana/Documents": false, "/srv/www/site": false, "/home/bob": false, "/mnt/c/Users/ana/site": false,
	} {
		if err := gitrepo.TooBroad(path, home); (err != nil) != broad {
			t.Errorf("TooBroad(%s) = %v, want broad %v", path, err, broad)
		}
	}
}

// A .git that git can't open is someone's history, not a plain folder: Open
// doesn't call it ErrNotARepo, and Create never inits over or removes it.
func TestAnUnreadableRepositoryIsNeverMadeAgain(t *testing.T) {
	testutil.GitEnv(t)
	dir := filepath.Join(t.TempDir(), "old")
	write(t, dir, "a.txt", "a\n")
	write(t, dir, ".git", "gitdir: /nowhere/that/exists\n")
	_, err := gitrepo.Open(dir)
	if err == nil || errors.Is(err, gitrepo.ErrNotARepo) || !strings.Contains(err.Error(), "can't open") {
		t.Fatalf("Open() = %v, want a repository git can't open", err)
	}
	if _, err := gitrepo.Open(filepath.Join(dir, "sub")); err == nil || errors.Is(err, gitrepo.ErrNotARepo) {
		t.Errorf("Open(below it) = %v", err)
	}
	if _, _, err := gitrepo.Create(dir, true); err == nil {
		t.Fatal("Create() made a repository over one git can't open")
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".git")); err != nil || !strings.Contains(string(b), "/nowhere/") {
		t.Errorf("the .git was touched: %q, %v", b, err)
	}
}
