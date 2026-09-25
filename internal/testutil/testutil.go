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

// GitEnv isolates git from the user's global and system configuration.
func GitEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "AgentBox Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@agentbox.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "AgentBox Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@agentbox.invalid")
}

// FixtureDir returns the path of testdata/fixtures/<name> in this repository.
func FixtureDir(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "fixtures", name)
}

// FixtureRepo copies a fixture into a new git repository with one commit on main.
func FixtureRepo(t *testing.T, name string) string {
	t.Helper()
	GitEnv(t)
	dst := filepath.Join(t.TempDir(), name)
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
