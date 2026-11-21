package agent

import (
	"path/filepath"

	"agentbox/internal/hostos"
	"agentbox/internal/iossim"
	"agentbox/internal/state"
)

// Where an agent's worktree is. In a VM, a new agent's worktree is on the
// VM's own disk (Paths.Worktrees: on a Cloud Hypervisor VM the agents' disk,
// through the data directory's worktrees symlink; on a Mac, Lima's disk),
// not in the host's home the VM shares: through the share every file a build,
// an install or a checkout writes was a round trip to the host, many times
// slower than the VM's disk, and the host's own disk took the writes.
//
// It's still a worktree of the project's repository, which stays on the
// host: its .git pointer names the repository's .git, which the agent's
// machine mounts at the same path, so its commits and branch are in the
// repository the moment they're made, where the user's own git sees them,
// with no push or fetch. Only the files checked out, and what is built and
// installed among them, are on the VM's disk. The host's git sees the
// worktree's path missing; AgentBox locks every worktree it makes, so a
// `git worktree prune` there (or git gc's) leaves its entry alone, as it
// does inside every other agent's machine already.
//
// An agent keeps its worktree where it was made: those made before stay on
// the share (Paths.SharedWorktrees), with the node_modules and target/
// mounts that only a worktree on the share gets (NodeModulesOn), until
// they're destroyed. The worktree is at the same path in the agent's
// machine as in the VM, which is where Open in editor opens it.

// onShare reports whether worktree is in Paths.SharedWorktrees.
func (m *Manager) onShare(worktree string) bool {
	shared := m.Paths.SharedWorktrees()
	if shared == "" {
		return false
	}
	rel, err := filepath.Rel(shared, filepath.Clean(worktree))
	return err == nil && filepath.IsLocal(rel)
}

// WorktreeFor is where a new agent called name of p gets its worktree:
// Paths.Worktrees, except an iOS project's on a Mac, which stays on the
// share: the simulator helper on the Mac installs the app from the agent's
// worktree (internal/iossim), and only sees the Mac's own files.
func (m *Manager) WorktreeFor(p state.Project, name string) string {
	if shared := m.Paths.SharedWorktree(p.Name, name); shared != "" && hostos.OS() == "darwin" && iossim.IsProject(p.Root) {
		return shared
	}
	return m.Paths.Worktree(p.Name, name)
}
