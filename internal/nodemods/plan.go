// Package nodemods keeps agents' node_modules on the VM's own disk. A
// worktree on a Mac is a folder the VM shares (virtiofs): writing the tens of
// thousands of small files of a node_modules there is five to fifteen times
// slower than on a disk of the VM's, deleting them as slow again, and every
// one held open is a file descriptor of the share's. So each directory of a
// worktree that installs packages gets a directory of the VM's mounted over
// its node_modules (agent/nodemodules.go), which the Mac sees empty.
//
// A project's agents install the same lockfile over and over, so what one
// agent installed is kept when it's destroyed, as a snapshot keyed by its
// lockfile, and a new agent whose lockfile is the same starts with it in
// place: cloned where the disk can (btrfs, the agents' disk of a Cloud
// Hypervisor VM), copied where it can't (ext4, a Lima VM's), never sharing a
// writable file with the snapshot or another agent. Its install then finds
// nothing to do.
package nodemods

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Lockfiles are the files that make their directory the root of an install:
// its node_modules, and its workspace members'.
var Lockfiles = []string{"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb"}

// MaxMounts is the most node_modules directories an agent gets of the VM's,
// the shallowest first: each is a mount of its own. A monorepo with more
// packages keeps the rest in its worktree, as before.
const MaxMounts = 32

// Group is one install: a directory with a lockfile, and the workspace
// members its package manager writes a node_modules into.
type Group struct {
	// Lockfile is the lockfile's path in the worktree; its directory is the
	// group's root.
	Lockfile string `json:"lockfile"`
	// Dirs are the directories that get a node_modules of the VM's, relative
	// to the worktree ("." for its top), the root first.
	Dirs []string `json:"dirs"`
}

// Root is the group's directory in the worktree.
func (g Group) Root() string { return path.Dir(g.Lockfile) }

// Plan works out the installs in a worktree from the files git tracks in it
// (slash-separated, relative to it): a group for each directory with a
// lockfile, with the directories its workspaces name that have a
// package.json. A package.json no lockfile covers — a test fixture, an
// example — gets nothing. readFile reads a file of the worktree by that same
// relative path.
func Plan(files []string, readFile func(rel string) ([]byte, error)) []Group {
	packages := map[string]bool{}
	locks := map[string]string{}
	for _, f := range files {
		dir, name := path.Split(f)
		dir = path.Clean(dir)
		if strings.Contains("/"+f, "/node_modules/") {
			continue
		}
		switch {
		case name == "package.json":
			packages[dir] = true
		case slices.Contains(Lockfiles, name):
			// One lockfile a directory: the first by Lockfiles' order.
			if have, ok := locks[dir]; !ok || slices.Index(Lockfiles, name) < slices.Index(Lockfiles, path.Base(have)) {
				locks[dir] = f
			}
		}
	}
	var roots []string
	for dir := range locks {
		if packages[dir] {
			roots = append(roots, dir)
		}
	}
	slices.Sort(roots)
	taken := map[string]bool{}
	for _, r := range roots {
		taken[r] = true
	}
	var groups []Group
	for _, root := range roots {
		g := Group{Lockfile: locks[root], Dirs: []string{root}}
		include, exclude := workspaces(root, readFile)
		var members []string
		for dir := range packages {
			if taken[dir] || !within(root, dir) {
				continue
			}
			rel := relTo(root, dir)
			if matchAny(include, rel) && !matchAny(exclude, rel) {
				members = append(members, dir)
			}
		}
		slices.SortFunc(members, byDepth)
		for _, m := range members {
			taken[m] = true
		}
		g.Dirs = append(g.Dirs, members...)
		groups = append(groups, g)
	}
	return capMounts(groups)
}

// capMounts keeps MaxMounts directories at most, every group's root before any
// member, shallower members before deeper ones.
func capMounts(groups []Group) []Group {
	n := 0
	for _, g := range groups {
		n += len(g.Dirs)
	}
	if n <= MaxMounts {
		return groups
	}
	var members []string
	for _, g := range groups {
		members = append(members, g.Dirs[1:]...)
	}
	slices.SortFunc(members, byDepth)
	keep := map[string]bool{}
	for _, m := range members[:max(0, min(len(members), MaxMounts-len(groups)))] {
		keep[m] = true
	}
	var out []Group
	for i, g := range groups {
		if i >= MaxMounts {
			break
		}
		c := Group{Lockfile: g.Lockfile, Dirs: []string{g.Dirs[0]}}
		for _, d := range g.Dirs[1:] {
			if keep[d] {
				c.Dirs = append(c.Dirs, d)
			}
		}
		out = append(out, c)
	}
	return out
}

func byDepth(a, b string) int {
	if d := strings.Count(a, "/") - strings.Count(b, "/"); d != 0 {
		return d
	}
	return strings.Compare(a, b)
}

func within(root, dir string) bool {
	return root == "." || dir == root || strings.HasPrefix(dir, root+"/")
}

func relTo(root, dir string) string {
	if root == "." {
		return dir
	}
	return strings.TrimPrefix(strings.TrimPrefix(dir, root), "/")
}

// workspaces are the globs a root's package.json ("workspaces", npm, Yarn
// and Bun) and pnpm-workspace.yaml ("packages") name, and those they leave out
// with a leading "!".
func workspaces(root string, readFile func(string) ([]byte, error)) (include, exclude []string) {
	var globs []string
	if b, err := readFile(path.Join(root, "package.json")); err == nil {
		var p struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(b, &p) == nil && len(p.Workspaces) > 0 {
			var list []string
			var obj struct {
				Packages []string `json:"packages"`
			}
			if json.Unmarshal(p.Workspaces, &list) == nil {
				globs = append(globs, list...)
			} else if json.Unmarshal(p.Workspaces, &obj) == nil {
				globs = append(globs, obj.Packages...)
			}
		}
	}
	if b, err := readFile(path.Join(root, "pnpm-workspace.yaml")); err == nil {
		globs = append(globs, pnpmPackages(string(b))...)
	}
	for _, g := range globs {
		g = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(g), "./"), "/")
		if neg, ok := strings.CutPrefix(g, "!"); ok {
			exclude = append(exclude, strings.TrimPrefix(neg, "./"))
		} else if g != "" {
			include = append(include, g)
		}
	}
	return include, exclude
}

// pnpmPackages reads the list under "packages:" in a pnpm-workspace.yaml:
// only that list, in its block form, which is the one pnpm documents.
func pnpmPackages(yaml string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "-") {
			in = strings.HasPrefix(trimmed, "packages:")
			continue
		}
		if item, ok := strings.CutPrefix(trimmed, "- "); in && ok {
			if i := strings.Index(item, " #"); i >= 0 {
				item = item[:i]
			}
			out = append(out, strings.Trim(strings.TrimSpace(item), `"'`))
		}
	}
	return out
}

func matchAny(globs []string, rel string) bool {
	for _, g := range globs {
		if matchGlob(strings.Split(g, "/"), strings.Split(rel, "/")) {
			return true
		}
	}
	return false
}

// matchGlob matches a workspace glob, segment by segment, "**" being any
// number of them.
func matchGlob(glob, segs []string) bool {
	if len(glob) == 0 {
		return len(segs) == 0
	}
	if glob[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchGlob(glob[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, err := path.Match(glob[0], segs[0]); err != nil || !ok {
		return false
	}
	return matchGlob(glob[1:], segs[1:])
}

// ReadWorktreeFile is a readFile for Plan that reads from a worktree.
func ReadWorktreeFile(worktree string) func(string) ([]byte, error) {
	return func(rel string) ([]byte, error) { return os.ReadFile(filepath.Join(worktree, filepath.FromSlash(rel))) }
}
