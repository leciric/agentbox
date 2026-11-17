package agent

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"agentbox/internal/state"
)

// An agent's Cargo builds go to the VM's disk too, like its node_modules
// (nodemodules.go): each Cargo workspace of its worktree gets a directory of
// the VM's mounted over its target/, a disk device named buildOutputDevice
// and a number. Written into the worktree, a target/ went through the shared
// folder to the host's own disk, many GiB a build, outside the VM's disk and
// so outside the weights that make agents' disk use yield (ioweight.go): the
// host flushed it in bursts that held up the user's own writes, and kept it
// after the agent was done with it (28 GiB of target/ in one user's
// worktrees). On the VM's disk it's in the agent's cgroup, and goes with the
// agent.
//
// Only target/, only Cargo's: a directory every Cargo build writes to and
// nothing else does, and that git never tracks. Other build outputs (dist/,
// build/, .next/) are a project's own choice, sometimes committed or read by
// the host. The host sees target/ empty, as it does node_modules; a target/
// the worktree already has stays where it is.
//
// The directories are in the node_modules store's directory of the agent, so
// they go with it when the agent is destroyed (nodemods.Store.Remove, Trim).
// It's on whenever node_modules are kept in the VM (NodeModulesOn).

// buildOutputDevice is the prefix of the disk devices that mount them.
const buildOutputDevice = "build-output-"

// maxBuildOutputs bounds the mounts an agent gets: a repository of many
// separate crates keeps the rest of its target/ in its worktree.
const maxBuildOutputs = 8

// cargoWorkspaces are the directories of a worktree, from the files git
// tracks in it, whose target/ Cargo writes: each Cargo.lock's, and each
// Cargo.toml's with no Cargo.toml above it (a library's workspace, whose
// lockfile isn't committed). Slash-separated, "." for the top; shallowest
// first, at most maxBuildOutputs.
func cargoWorkspaces(files []string) []string {
	manifests := map[string]bool{}
	var locks []string
	for _, f := range files {
		switch path.Base(f) {
		case "Cargo.toml":
			manifests[path.Dir(f)] = true
		case "Cargo.lock":
			locks = append(locks, path.Dir(f))
		}
	}
	under := func(dir string) bool {
		for d := dir; d != "."; {
			d = path.Dir(d)
			if manifests[d] {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	for _, dir := range locks {
		if manifests[dir] {
			add(dir)
		}
	}
	for dir := range manifests {
		if !under(dir) {
			add(dir)
		}
	}
	depth := func(dir string) int {
		if dir == "." {
			return -1
		}
		return strings.Count(dir, "/")
	}
	slices.SortFunc(out, func(a, b string) int {
		if c := depth(a) - depth(b); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	if len(out) > maxBuildOutputs {
		out = out[:maxBuildOutputs]
	}
	return out
}

func (m *Manager) buildOutputsPath(id string) string {
	return m.NodeModules.AgentFile(id, "build-outputs.json")
}

// buildOutputSource is the VM's directory mounted over dir's target/.
func (m *Manager) buildOutputSource(instance, dir string) string {
	return m.NodeModules.AgentFile(instance, "target-"+strings.NewReplacer("%", "%25", "/", "%2F").Replace(dir))
}

// ensureBuildOutputs mounts a's Cargo target/ directories from the VM's disk
// while node_modules are kept there, and takes them out when they aren't.
// Best-effort: without them, Cargo builds into the worktree as before.
func (m *Manager) ensureBuildOutputs(ctx context.Context, a state.Agent) {
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		m.logf("%s keeps target/ in its worktree: %v", a.Ref(), err)
		return
	}
	want := map[string][2]string{} // device: source, path
	defer func() {
		for name, d := range devices {
			if !strings.HasPrefix(name, buildOutputDevice) {
				continue
			}
			if w, ok := want[name]; ok && d["source"] == w[0] && d["path"] == w[1] {
				delete(want, name)
				continue
			}
			if err := m.Incus.RemoveDevice(ctx, a.Instance, name); err != nil {
				m.logf("removing %s from %s: %v", name, a.Ref(), err)
			}
		}
		for name, w := range want {
			if err := m.Incus.AddDevice(ctx, a.Instance, name, "disk", "source="+w[0], "path="+w[1]); err != nil {
				m.logf("%s keeps %s in its worktree: %v", a.Ref(), w[1], err)
			}
		}
	}()
	if m.NodeModules == nil || m.NodeModulesOn == nil || !m.NodeModulesOn(ctx) {
		return
	}
	var dirs []string
	for _, dir := range cargoWorkspaces(trackedFilesNamed(a.Worktree, "Cargo.toml", "Cargo.lock")) {
		// A target/ the worktree already has, from before, stays in use.
		if entries, err := os.ReadDir(filepath.Join(a.Worktree, dir, "target")); err == nil && len(entries) > 0 {
			continue
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		return
	}
	for i, dir := range dirs {
		src := m.buildOutputSource(a.Instance, dir)
		if err := m.makeBuildOutputSource(src); err != nil {
			m.logf("%s keeps target/ in its worktree: %v", a.Ref(), err)
			return
		}
		want[buildOutputDevice+strconv.Itoa(i)] = [2]string{src, filepath.Join(a.Worktree, dir, "target")}
	}
	if b, err := json.Marshal(dirs); err == nil {
		_ = os.WriteFile(m.buildOutputsPath(a.Instance), b, 0o644)
	}
}

func (m *Manager) makeBuildOutputSource(src string) error {
	if err := os.MkdirAll(src, 0o755); err != nil {
		return err
	}
	if m.NodeModules.Owner != nil {
		return os.Chown(src, m.NodeModules.Owner[0], m.NodeModules.Owner[1])
	}
	return nil
}

// buildOutputDirs are the directories of a's worktree whose target/ is on
// the VM's disk, as the brief names them.
func (m *Manager) buildOutputDirs(a state.Agent) []string {
	if m.NodeModules == nil {
		return nil
	}
	b, err := os.ReadFile(m.buildOutputsPath(a.Instance))
	if err != nil {
		return nil
	}
	var dirs []string
	if json.Unmarshal(b, &dirs) != nil {
		return nil
	}
	return dirs
}

// buildOutputMounts are where a's target/ directories of the VM's are
// mounted in its machine.
func (m *Manager) buildOutputMounts(a state.Agent) []string {
	var out []string
	for _, dir := range m.buildOutputDirs(a) {
		out = append(out, filepath.Join(a.Worktree, dir, "target"))
	}
	return out
}

// buildOutputSources makes again the directories a's build-output devices
// mount, before its machine starts: Incus won't start one whose disk's
// source is missing, and an empty target/ is only a build away.
func (m *Manager) buildOutputSources(ctx context.Context, a state.Agent) {
	if m.NodeModules == nil {
		return
	}
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return
	}
	for name, d := range devices {
		if strings.HasPrefix(name, buildOutputDevice) && d["source"] != "" {
			if _, err := os.Stat(d["source"]); err != nil {
				_ = m.makeBuildOutputSource(d["source"])
			}
		}
	}
}
