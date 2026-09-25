// Package testutil holds helpers shared by tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// gitIsolated is set once IsolateGit has isolated the whole test binary.
var gitIsolated bool

// GitEnv isolates git from the user's global and system configuration. It
// sets the environment for one test, which can't then run in parallel, unless
// IsolateGit has already done it for the whole test binary.
func GitEnv(t *testing.T) {
	t.Helper()
	if gitIsolated {
		return
	}
	for k, v := range gitEnv(filepath.Join(t.TempDir(), "gitconfig")) {
		t.Setenv(k, v)
	}
}

// IsolateGit does what GitEnv does for the whole test binary, from TestMain,
// before any test runs: for packages whose parallel tests run git, or run code
// that does. It answers a function that removes what it made.
func IsolateGit() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "agentbox-gitconfig-")
	if err != nil {
		return nil, err
	}
	for k, v := range gitEnv(filepath.Join(dir, "gitconfig")) {
		if err := os.Setenv(k, v); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
	}
	gitIsolated = true
	return func() { _ = os.RemoveAll(dir) }, nil
}

func gitEnv(config string) map[string]string {
	return map[string]string{
		"GIT_CONFIG_GLOBAL":   config,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "AgentBox Test",
		"GIT_AUTHOR_EMAIL":    "test@agentbox.invalid",
		"GIT_COMMITTER_NAME":  "AgentBox Test",
		"GIT_COMMITTER_EMAIL": "test@agentbox.invalid",
		// Without this, git commit/merge spawns "git maintenance run --auto
		// --detach", a background process that keeps writing into .git after
		// the command that started it has returned — and so after a test that
		// ran it has already returned too, racing t.TempDir's cleanup ("directory
		// not empty").
		"GIT_CONFIG_COUNT":   "1",
		"GIT_CONFIG_KEY_0":   "maintenance.auto",
		"GIT_CONFIG_VALUE_0": "false",
	}
}

// FixtureDir returns the path of testdata/fixtures/<name> in this repository.
func FixtureDir(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "fixtures", name)
}

// FixtureRepo copies a fixture into a new git repository with one commit on main.
func FixtureRepo(t *testing.T, name string) string {
	t.Helper()
	return FixtureRepoIn(t, t.TempDir(), name)
}

// FixtureRepoIn is FixtureRepo in dir/<name>, for a test that decides when
// the repository is removed.
func FixtureRepoIn(t *testing.T, dir, name string) string {
	t.Helper()
	GitEnv(t)
	dst := filepath.Join(dir, name)
	if err := os.CopyFS(dst, os.DirFS(FixtureDir(name))); err != nil {
		t.Fatal(err)
	}
	Git(t, dst, "init", "-q", "-b", "main")
	Git(t, dst, "add", "-A")
	Git(t, dst, "commit", "-q", "-m", "fixture")
	return dst
}

// Git runs git in dir and fails the test on error.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
