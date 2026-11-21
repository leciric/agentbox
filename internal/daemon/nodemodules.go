package daemon

import (
	"context"
	"os"
	"path/filepath"

	"agentbox/internal/agent"
	"agentbox/internal/hostos"
	"agentbox/internal/nodemods"
	"agentbox/internal/state"
)

// Agents' node_modules on the VM's disk rather than in their worktrees
// (internal/nodemods, agent/nodemodules.go), for an agent whose worktree is
// in a folder the VM shares with the machine it runs on (one made before
// worktrees moved to the VM's disk, or an iOS project's on a Mac), while
// SettingLocalNodeModules is on. The snapshots are trimmed with the package
// caches.

func (s *Server) newNodeModules() *nodemods.Store {
	n := &nodemods.Store{
		Dir: s.cfg.Paths.NodeModules(),
		Room: func() int64 {
			free, total, _, ok := diskSpace(existingParent(s.cfg.Paths.NodeModules()))
			if !ok {
				return 0
			}
			return free - s.diskFloor(context.Background()).For(total)
		},
		Logf: s.logf,
	}
	if os.Geteuid() == 0 && s.cfg.User.UID != 0 {
		n.Owner = &[2]int{s.cfg.User.UID, s.cfg.User.GID}
	}
	return n
}

// placeNodeModules points the store at where its directory really is, once
// the data directory is in place: in a VM, a symlink to the agents' disk
// (moveDataToPool). Incus mounts that path.
func (s *Server) placeNodeModules() {
	dir := s.cfg.Paths.NodeModules()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.logf("node_modules: %v", err)
		return
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		s.nodeModules.Dir = real
	}
}

// placeWorktrees has new agents' worktrees made where the directory for them
// really is, once the data directory is in place: in a Cloud Hypervisor VM, a
// symlink to the agents' disk (moveDataToPool). Incus mounts that path into
// the agent's machine, and git writes it down, so it has no symlink in it.
func (s *Server) placeWorktrees() {
	if !hostos.InVM() || s.cfg.Paths.WorktreesDir != "" {
		return
	}
	dir := s.cfg.Paths.Worktrees()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.logf("worktrees: %v", err)
		return
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		s.cfg.Paths.WorktreesDir = real
	}
}

// nodeModulesOn says whether the agent with worktree gets its node_modules
// on the VM's disk: while it's on, and only for a worktree in a shared
// folder, which is what makes them slow. An agent's worktrees directory is
// looked at once: every worktree in it is on the same file system.
func (s *Server) nodeModulesOn(ctx context.Context, worktree string) bool {
	on, err := s.store.FlagOn(ctx, state.SettingLocalNodeModules)
	if err != nil || !on || worktree == "" {
		return false
	}
	dir := filepath.Dir(filepath.Dir(filepath.Clean(worktree)))
	if shared, ok := s.sharedWorktrees.Load(dir); ok {
		return shared.(bool)
	}
	shared := sharedFolder(existingParent(dir))
	s.sharedWorktrees.Store(dir, shared)
	return shared
}

// trimNodeModules removes what agents that are gone left, and the snapshots
// beyond their cap.
func (s *Server) trimNodeModules(ctx context.Context) {
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		return
	}
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, a := range agents {
		live[a.Instance] = true
	}
	known := map[string]bool{}
	for _, p := range projects {
		known[p.Name] = true
	}
	if freed := s.nodeModules.Trim(live, known); freed > 0 {
		s.logf("node_modules: removed %s of snapshots and agents' leftovers", agent.HumanBytes(freed))
	}
}
