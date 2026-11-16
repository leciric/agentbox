package daemon

import (
	"context"
	"os"
	"path/filepath"

	"agentbox/internal/agent"
	"agentbox/internal/nodemods"
	"agentbox/internal/state"
)

// Agents' node_modules on the VM's disk rather than in their worktrees
// (internal/nodemods, agent/nodemodules.go), while worktrees are in a folder
// the VM shares with the machine it runs on, and SettingLocalNodeModules is
// on. The snapshots are trimmed with the package caches.

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

// nodeModulesOn says whether a new agent gets its node_modules on the VM's
// disk: while it's on, and only where worktrees are in a shared folder, which
// is what makes them slow.
func (s *Server) nodeModulesOn(ctx context.Context) bool {
	on, err := s.store.FlagOn(ctx, state.SettingLocalNodeModules)
	if err != nil || !on {
		return false
	}
	s.sharedWorktreesOnce.Do(func() {
		s.sharedWorktrees = sharedFolder(existingParent(s.cfg.Paths.Worktrees()))
	})
	return s.sharedWorktrees
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
