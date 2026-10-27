package agentd_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agentd"
)

// testRepo makes the shape a pod's workspace has: a repository with one commit,
// a bare repository that is its origin, and a clone of it to work in. It
// returns the clone and the commit the branch starts from.
func testRepo(t *testing.T) (workspace, base string) {
	t.Helper()
	// git's own configuration on this machine is not the pod's: signing and
	// identity here would make the tests depend on who runs them.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	workspace = filepath.Join(root, "workspace")
	gitTest(t, root, "init", "--quiet", "--bare", "-b", "main", origin)
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTest(t, seed, "init", "--quiet", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# pawly\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, seed, "add", "README.md")
	gitTest(t, seed, "commit", "--quiet", "-m", "first")
	base = gitTest(t, seed, "rev-parse", "HEAD")
	gitTest(t, seed, "remote", "add", "origin", origin)
	gitTest(t, seed, "push", "--quiet", "origin", "main")
	gitTest(t, root, "clone", "--quiet", origin, workspace)
	return workspace, base
}

// gitTest runs one git command, and fails the test with everything git said.
func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPrepareCreatesTheBranchFromTheBase(t *testing.T) {
	workspace, base := testRepo(t)
	w := agentd.Workspace{Dir: workspace, Branch: "agent/vm/test-01", Base: base, Log: t.Logf}
	if err := w.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := gitTest(t, workspace, "symbolic-ref", "--short", "HEAD"); got != "agent/vm/test-01" {
		t.Errorf("HEAD is on %q, and the branch is agent/vm/test-01", got)
	}
	if got := gitTest(t, workspace, "rev-parse", "HEAD"); got != base {
		t.Errorf("the branch starts at %s, and the base commit is %s", got, base)
	}
	// A workspace that wandered to another branch is put back on its own.
	gitTest(t, workspace, "checkout", "--quiet", "main")
	if err := w.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := gitTest(t, workspace, "symbolic-ref", "--short", "HEAD"); got != "agent/vm/test-01" {
		t.Errorf("a second Prepare left HEAD on %q, and the branch is agent/vm/test-01", got)
	}
}

func TestPrepareLeavesADirtyTreeAlone(t *testing.T) {
	workspace, base := testRepo(t)
	w := agentd.Workspace{Dir: workspace, Branch: "agent/vm/test-01", Base: base, Log: t.Logf}
	if err := w.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	// What a restart finds when the agent worked without committing: an edit
	// and a file git has never seen.
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// And on a branch the master did not name, where a checkout would have to
	// move the changes.
	gitTest(t, workspace, "checkout", "--quiet", "-b", "elsewhere")
	if err := w.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := gitTest(t, workspace, "symbolic-ref", "--short", "HEAD"); got != "elsewhere" {
		t.Errorf("a dirty tree was moved to %q, and it is left where it was", got)
	}
	for file, want := range map[string]string{"README.md": "changed\n", "notes.txt": "mine\n"} {
		got, err := os.ReadFile(filepath.Join(workspace, file))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s holds %q, and the work in it was %q", file, got, want)
		}
	}
}

func TestCheckpointCommitsAndThenHasNothingToDo(t *testing.T) {
	workspace, base := testRepo(t)
	w := agentd.Workspace{Dir: workspace, Branch: "agent/vm/test-01", Base: base, Log: t.Logf}
	if err := w.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := w.Checkpoint(context.Background(), "agentbox checkpoint turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 40 || first == base {
		t.Fatalf("the checkpoint is %q, and it is a new commit", first)
	}
	if got := gitTest(t, workspace, "rev-parse", "HEAD"); got != first {
		t.Errorf("HEAD is %s, and the checkpoint is %s", got, first)
	}
	if got := gitTest(t, workspace, "status", "--porcelain"); got != "" {
		t.Errorf("the workspace still has changes: %q", got)
	}
	if got := gitTest(t, workspace, "log", "-1", "--format=%an <%ae>"); got != "AgentBox <agentbox@localhost>" {
		t.Errorf("the checkpoint is committed by %q, and it is the master's", got)
	}
	// Nothing changed between the two calls, and Checkpoint answers with the
	// branch's head rather than making an empty commit.
	second, err := w.Checkpoint(context.Background(), "agentbox checkpoint turn-2")
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("a checkpoint with nothing to commit answered %q, and the branch is at %q", second, first)
	}
	if got := gitTest(t, workspace, "rev-list", "--count", "HEAD"); got != "2" {
		t.Errorf("the branch has %s commits, and the empty checkpoint made none", got)
	}
}

func TestPrepareCarriesOnWithoutARepository(t *testing.T) {
	// A pod's first start: the volume is empty, and the clone arrives with the
	// git profile work. Prepare must not keep the pod from dialing the master,
	// and it says what it found.
	dir := t.TempDir()
	var said []string
	w := agentd.Workspace{
		Dir: dir, Branch: "agent/vm/test-01", Base: "0123456789abcdef0123456789abcdef01234567",
		Log: func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) },
	}
	if err := w.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare of a workspace with no repository = %v, and a fresh pod serves without one", err)
	}
	if len(said) == 0 || !strings.Contains(strings.Join(said, " "), "no repository") {
		t.Errorf("Prepare of a workspace with no repository said %q, and it says there is no repository", said)
	}
}

func TestPrepareFailsOnARepositoryItCannotRead(t *testing.T) {
	// A .git that isn't a repository is not a fresh pod: something is wrong
	// with the volume, and serving a workspace that can't be prepared hides it.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := agentd.Workspace{Dir: dir, Branch: "agent/vm/test-01", Base: "0123456789abcdef0123456789abcdef01234567"}
	if err := w.Prepare(context.Background()); err == nil {
		t.Error("Prepare of a .git that is not a repository = nil, and it is a git failure to report")
	}
}
