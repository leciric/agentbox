package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/nodemods"
	"agentbox/internal/state"
)

// An agent's node_modules are on the VM's disk (internal/nodemods): each
// directory of its worktree that installs packages gets a directory of the
// VM's mounted over its node_modules, a disk device named nodeModulesDevice
// and a number. Writing them in a worktree the VM shares with a Mac was five
// to fifteen times slower, and held as many of the share's file descriptors.
// The Mac, and the project's lead with it, sees those node_modules empty.
//
// What an agent installed is kept when it's destroyed, keyed by its lockfile
// (keepNodeModules), and the next agent with that lockfile starts with it.

// nodeModulesDevice is the prefix of the disk devices that mount them.
const nodeModulesDevice = "node-modules-"

// nodeModulesMeta is what the store keeps of an agent's installs, read again
// when it's destroyed: the groups it planned, and its Node.js ABI.
type nodeModulesMeta struct {
	Groups []nodemods.Group `json:"groups"`
	ABI    string           `json:"abi"`
}

func (m *Manager) nodeModulesMetaPath(a state.Agent) string {
	return m.NodeModules.AgentFile(a.Instance, "installs.json")
}

// ensureNodeModules mounts a's node_modules from the VM's disk, filled from
// the project's snapshots where its lockfile has one, when that's on; a
// machine copied from another agent loses that agent's. Best-effort: without
// them the agent installs into its worktree, as before.
func (m *Manager) ensureNodeModules(ctx context.Context, a state.Agent) {
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		m.logf("%s keeps node_modules in its worktree: %v", a.Ref(), err)
		return
	}
	want := map[string][2]string{} // device: source, path
	defer func() {
		for name, d := range devices {
			if !strings.HasPrefix(name, nodeModulesDevice) {
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
	groups := nodemods.Plan(trackedFiles(a.Worktree), nodemods.ReadWorktreeFile(a.Worktree))
	// A node_modules the worktree already has, from before, stays in use.
	var kept []nodemods.Group
	for _, g := range groups {
		var dirs []string
		for _, dir := range g.Dirs {
			if entries, err := os.ReadDir(filepath.Join(a.Worktree, dir, "node_modules")); err == nil && len(entries) > 0 {
				continue
			}
			dirs = append(dirs, dir)
		}
		if len(dirs) > 0 && dirs[0] == g.Dirs[0] {
			g.Dirs = dirs
			kept = append(kept, g)
		}
	}
	if len(kept) == 0 {
		return
	}
	meta := nodeModulesMeta{Groups: kept, ABI: m.nodeABI(ctx, a)}
	installs := nodeModulesInstalls(a.Worktree, meta)
	start := time.Now()
	restored, err := m.NodeModules.Prepare(a.Instance, a.Project, installs)
	if err != nil {
		m.logf("%s keeps node_modules in its worktree: %v", a.Ref(), err)
		return
	}
	if len(restored) > 0 {
		m.logf("Gave %s the node_modules already installed for %s, in %s", a.Ref(), strings.Join(restored, ", "), time.Since(start).Round(time.Millisecond))
	}
	if b, err := json.Marshal(meta); err == nil {
		_ = os.WriteFile(m.nodeModulesMetaPath(a), b, 0o644)
	}
	n := 0
	for _, g := range kept {
		for _, dir := range g.Dirs {
			want[nodeModulesDevice+strconv.Itoa(n)] = [2]string{m.NodeModules.MountDir(a.Instance, dir), filepath.Join(a.Worktree, dir, "node_modules")}
			n++
		}
	}
}

// nodeModulesDirs are the directories of a's worktree whose node_modules are
// on the VM's disk, as the brief names them.
func (m *Manager) nodeModulesDirs(a state.Agent) []string {
	meta, ok := m.nodeModulesMeta(a)
	if !ok {
		return nil
	}
	var dirs []string
	for _, g := range meta.Groups {
		dirs = append(dirs, g.Dirs...)
	}
	return dirs
}

// nodeModulesSources makes again the directories a's node_modules devices
// mount, before its machine starts: Incus won't start one whose disk's
// source is missing, and an empty node_modules is only an install away.
func (m *Manager) nodeModulesSources(ctx context.Context, a state.Agent) {
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return
	}
	for name, d := range devices {
		if strings.HasPrefix(name, nodeModulesDevice) && d["source"] != "" {
			if _, err := os.Stat(d["source"]); err != nil {
				_ = os.MkdirAll(d["source"], 0o755)
				if m.NodeModules != nil && m.NodeModules.Owner != nil {
					_ = os.Chown(d["source"], m.NodeModules.Owner[0], m.NodeModules.Owner[1])
				}
			}
		}
	}
}

// nodeModulesInstalls is what a destroyed agent installed, read from its
// worktree before it's emptied: its lockfiles now, which its node_modules
// are kept under.
// running, its Node.js ABI is read again: the agent may have moved to
// another Node.js since it was made, and built its native modules for it.
func (m *Manager) nodeModulesInstalls(ctx context.Context, a state.Agent, running bool) []nodemods.Install {
	meta, ok := m.nodeModulesMeta(a)
	if !ok {
		return nil
	}
	if running {
		if abi := m.nodeABI(ctx, a); abi != "" {
			meta.ABI = abi
		}
	}
	return nodeModulesInstalls(a.Worktree, meta)
}

func (m *Manager) nodeModulesMeta(a state.Agent) (nodeModulesMeta, bool) {
	var meta nodeModulesMeta
	if m.NodeModules == nil {
		return meta, false
	}
	b, err := os.ReadFile(m.nodeModulesMetaPath(a))
	if err != nil || json.Unmarshal(b, &meta) != nil {
		return meta, false
	}
	return meta, true
}

// nodeModulesMounts are where a's node_modules of the VM's are mounted in
// its machine.
func (m *Manager) nodeModulesMounts(a state.Agent) []string {
	var out []string
	for _, dir := range m.nodeModulesDirs(a) {
		out = append(out, filepath.Join(a.Worktree, dir, "node_modules"))
	}
	return out
}

func nodeModulesInstalls(worktree string, meta nodeModulesMeta) []nodemods.Install {
	var out []nodemods.Install
	for _, g := range meta.Groups {
		in := nodemods.Install{Group: g}
		path := filepath.Join(worktree, filepath.FromSlash(g.Lockfile))
		if lock, err := os.ReadFile(path); err == nil {
			if info, err := os.Stat(path); err == nil {
				in.Key = nodemods.Key(g, lock, meta.ABI)
				in.LockTime = info.ModTime()
			}
		}
		out = append(out, in)
	}
	return out
}

// handBackNodeModules is clearWorktree's chown for a machine that isn't
// running: without it, what container root made in a's node_modules stays
// root's, which a daemon that isn't root can neither keep as a snapshot nor
// delete, and it piles up in the store's trash. Only when the store finds
// something there that isn't the user's is the machine started for it (or
// thawed, when paused), just long enough to hand it back: Destroy deletes it
// next. Best-effort, like clearWorktree.
func (m *Manager) handBackNodeModules(ctx context.Context, a state.Agent) {
	mounts := m.nodeModulesMounts(a)
	if len(mounts) == 0 || os.Geteuid() == 0 || !m.NodeModules.Foreign(a.Instance, m.User.UID) {
		return
	}
	m.logf("Starting %s to hand its node_modules back to you", a.Ref())
	if err := m.Incus.Start(ctx, a.Instance); err != nil {
		m.logf("Starting %s: %v; what root made in its node_modules stays", a.Ref(), err)
		return
	}
	args := append([]string{"chown", "-R", fmt.Sprintf("%d:%d", m.User.UID, m.User.GID)}, mounts...)
	if _, err := m.Incus.Exec(ctx, a.Instance, args...); err != nil {
		m.logf("Handing %s's node_modules back: %v", a.Ref(), err)
	}
}

// keepNodeModules keeps what a installed as its project's snapshots, once its
// machine is gone, and removes the rest.
func (m *Manager) keepNodeModules(a state.Agent, installs []nodemods.Install) {
	if m.NodeModules == nil {
		return
	}
	m.NodeModules.Capture(a.Instance, a.Project, installs)
}

// nodeABI is the ABI of the Node.js a's worktree runs (mise may pin one),
// which native modules are built for; "" without Node.js.
func (m *Manager) nodeABI(ctx context.Context, a state.Agent) string {
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := "cd " + shellQuote(a.Worktree) + " && node -p process.versions.modules 2>/dev/null"
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, cmd, nil, &out, nil); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// trackedFiles are the package.json files, lockfiles and pnpm workspaces git
// tracks in worktree, from its index: no walk through the share.
func trackedFiles(worktree string) []string {
	var patterns []string
	for _, name := range append([]string{"package.json", "pnpm-workspace.yaml"}, nodemods.Lockfiles...) {
		patterns = append(patterns, name, ":(glob)**/"+name)
	}
	out, err := exec.Command("git", append([]string{"-C", worktree, "ls-files", "-z", "--"}, patterns...)...).Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, path.Clean(f))
		}
	}
	return files
}
