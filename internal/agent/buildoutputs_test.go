package agent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/nodemods"
	"agentbox/internal/state"
)

func cargoWorktree(t *testing.T, withTarget bool) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{"Cargo.toml": "[workspace]\n", "Cargo.lock": "", "crates/a/Cargo.toml": ""}
	if withTarget {
		files["target/debug/.fingerprint"] = ""
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "Cargo.toml", "Cargo.lock", "crates"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func TestCargoTargetGoesOnTheVMsDisk(t *testing.T) {
	f, calls := packageCacheFixture(t, `{}`)
	store := &nodemods.Store{Dir: t.TempDir()}
	f.m.NodeModules = store
	f.m.NodeModulesOn = func(context.Context) bool { return true }
	a := state.Agent{Project: "p", Name: "agent-1", Instance: "ab-p-agent-1", Worktree: cargoWorktree(t, false)}
	f.m.EnsureBuildOutputs(context.Background(), a)
	got := strings.Join(calls(), "\n")
	src := store.AgentFile(a.Instance, "target-.")
	want := "config device add ab-p-agent-1 build-output-0 disk source=" + src + " path=" + filepath.Join(a.Worktree, "target")
	if !strings.Contains(got, want) {
		t.Fatalf("no %q in:\n%s", want, got)
	}
	if n := strings.Count(got, "config device add"); n != 1 {
		t.Errorf("%d devices added, want one for the workspace:\n%s", n, got)
	}
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		t.Errorf("the mount's source wasn't made: %v", err)
	}
	// It goes with the agent.
	store.Remove(a.Instance)
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("a removed agent's target/ is still there: %v", err)
	}
}

// A target/ the worktree already has stays in use; off, a copied machine
// loses its devices.
func TestCargoTargetKeptInTheWorktreeWhenThereOrOff(t *testing.T) {
	f, calls := packageCacheFixture(t, `{}`)
	f.m.NodeModules = &nodemods.Store{Dir: t.TempDir()}
	f.m.NodeModulesOn = func(context.Context) bool { return true }
	f.m.EnsureBuildOutputs(context.Background(), state.Agent{Instance: "ab-p-agent-1", Worktree: cargoWorktree(t, true)})
	if got := strings.Join(calls(), "\n"); strings.Contains(got, "config device add") {
		t.Errorf("mounted over a target/ the worktree has:\n%s", got)
	}

	f, calls = packageCacheFixture(t, `{"build-output-0": {"type": "disk", "source": "/vm/x", "path": "/w/target"}}`)
	f.m.NodeModules = &nodemods.Store{Dir: t.TempDir()}
	f.m.NodeModulesOn = func(context.Context) bool { return false }
	f.m.EnsureBuildOutputs(context.Background(), state.Agent{Instance: "ab-p-agent-1", Worktree: cargoWorktree(t, false)})
	if got := strings.Join(calls(), "\n"); !strings.Contains(got, "config device remove ab-p-agent-1 build-output-0") {
		t.Errorf("the device wasn't removed:\n%s", got)
	}
}
