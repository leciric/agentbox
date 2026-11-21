package daemon

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/hostos"
	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// In a VM, new agents' worktrees go where the data directory's worktrees
// really is: on a Cloud Hypervisor VM, the agents' disk its symlink names
// (moveDataToPool). Incus mounts that path and git writes it down. A host
// install's are left as they were.
func TestPlaceWorktrees(t *testing.T) {
	data, pool := t.TempDir(), t.TempDir()
	if err := os.Symlink(pool, filepath.Join(data, "worktrees")); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTBOX_WORKTREES", "")
	t.Setenv(hostos.Env, "")
	s := &Server{cfg: Config{Paths: paths.Paths{Data: data}, Log: io.Discard}}
	if s.placeWorktrees(); s.cfg.Paths.WorktreesDir != "" {
		t.Errorf("on a host install: WorktreesDir = %q, want it unset", s.cfg.Paths.WorktreesDir)
	}
	t.Setenv(hostos.Env, hostos.Linux)
	if s.placeWorktrees(); s.cfg.Paths.Worktrees() != want {
		t.Errorf("in a VM: Worktrees = %q, want %q, with no symlink in it", s.cfg.Paths.Worktrees(), want)
	}
	if got, want := s.cfg.Paths.WorktreeTrash(), filepath.Join(filepath.Dir(want), "worktrees-trash"); got != want {
		t.Errorf("WorktreeTrash = %q, want %q, on the same disk", got, want)
	}
}

// Only a worktree in a shared folder gets its node_modules on the VM's disk:
// one already on the VM's disk has them there in the worktree.
func TestNodeModulesOnlyForASharedWorktree(t *testing.T) {
	dir := t.TempDir()
	if sharedFolder(dir) {
		t.Skip("the test's own directory is in a shared folder")
	}
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SetFlag(t.Context(), state.SettingLocalNodeModules, true); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store}
	if s.nodeModulesOn(t.Context(), filepath.Join(dir, "p", "agent-1")) {
		t.Error("a worktree on this machine's own disk gets node_modules mounts")
	}
	if s.nodeModulesOn(t.Context(), "") {
		t.Error("an agent with no worktree gets node_modules mounts")
	}
}
