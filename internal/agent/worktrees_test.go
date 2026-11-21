package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/hostos"
	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// A new agent's worktree is in Paths.Worktrees, the VM's own disk, except an
// iOS project's on a Mac, which the simulator helper there installs the app
// from, and which so stays on the Mac's share.
func TestWorktreeFor(t *testing.T) {
	f := setup(t, fakeIncus(t, `exit 0`))
	shared := t.TempDir()
	t.Setenv(paths.SharedWorktreesEnv, shared)
	p := state.Project{Name: "app", Root: t.TempDir()}
	onDisk := f.m.Paths.Worktree("app", "agent-01")

	t.Setenv(hostos.Env, "darwin")
	if got := f.m.WorktreeFor(p, "agent-01"); got != onDisk {
		t.Errorf("a Mac's project: WorktreeFor = %s, want %s, on the VM's disk", got, onDisk)
	}
	if err := os.MkdirAll(filepath.Join(p.Root, "App.xcodeproj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := f.m.WorktreeFor(p, "agent-01"), filepath.Join(shared, "app", "agent-01"); got != want {
		t.Errorf("a Mac's iOS project: WorktreeFor = %s, want %s, on the share", got, want)
	}
	t.Setenv(hostos.Env, hostos.Linux)
	if got := f.m.WorktreeFor(p, "agent-01"); got != onDisk {
		t.Errorf("an iOS project on Linux, which has no simulator: WorktreeFor = %s, want %s", got, onDisk)
	}
}
