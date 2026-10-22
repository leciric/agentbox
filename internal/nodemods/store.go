package nodemods

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// The store's directory holds
//
//	agents/<instance>/<dir>/      what each agent's node_modules are, mounted over them
//	snapshots/<project>/<key>/    one install kept, <dir>/ a directory, and meta.json
//
// both on one file system, so that a snapshot is taken by renaming, and
// given by cloning where the file system can.

// DefaultMax is what the snapshots may hold together, when Max isn't set.
const DefaultMax int64 = 10 << 30

// KeepPerLockfile is how many snapshots of one lockfile of a project are
// kept, the newest: a branch's lockfile and main's, say. Older ones are what
// a project's lockfile used to be.
const KeepPerLockfile = 2

// recent is how long a snapshot or an agent's directory is left alone after
// it was last touched, in case it's being made now.
const recent = 10 * time.Minute

// Markers are the files a package manager writes in node_modules when an
// install finishes: npm's hidden lockfile, pnpm's, Yarn 1's and Yarn Berry's,
// Bun's. A node_modules with none, or with none newer than its lockfile, isn't
// known to match it, and isn't kept.
var Markers = []string{".package-lock.json", ".modules.yaml", ".yarn-integrity", ".yarn-state.yml", ".bun-tag"}

// Store is the VM's directory of agents' node_modules and of snapshots.
type Store struct {
	Dir string
	// Owner, when set, is who the directories it makes belong to: the agents'
	// user, when the daemon runs as root.
	Owner *[2]int
	// Max is the most the snapshots may hold together; DefaultMax when nil or
	// zero.
	Max func() int64
	// Room, when set, is how many more bytes the disk can take before it's at
	// its floor. Below it, Trim evicts snapshots until it's above again.
	Room func() int64
	Logf func(format string, args ...any)

	// mu is held for what's quick — checks, renames — never through a copy
	// or a delete: those happen outside it, a delete by first renaming what
	// goes into trash/ (dispose).
	mu sync.Mutex
	// restoring counts the copies being made from each snapshot, which Trim
	// leaves alone.
	restoring map[string]int
	now       func() time.Time
}

// Install is a group as an agent has it: its lockfile's key, and when the
// lockfile last changed.
type Install struct {
	Group
	Key      string
	LockTime time.Time
}

// Key names a snapshot: the lockfile's contents, where it is, the directories
// installed, and the Node.js ABI (process.versions.modules) native modules
// were built for.
func Key(g Group, lockfile []byte, abi string) string {
	h := sha256.New()
	h.Write([]byte("nodemods v1\n" + g.Lockfile + "\n" + strings.Join(g.Dirs, "\n") + "\nabi " + abi + "\n"))
	h.Write(lockfile)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// name is a worktree directory's name in the store.
func name(dir string) string {
	return "d-" + strings.NewReplacer("%", "%25", "/", "%2F").Replace(dir)
}

func (s *Store) agentDir(instance string) string { return filepath.Join(s.Dir, "agents", instance) }

// MountDir is what's mounted over dir's node_modules in instance.
func (s *Store) MountDir(instance, dir string) string {
	return filepath.Join(s.agentDir(instance), name(dir))
}

// AgentFile is a file the daemon keeps about instance, removed with its
// directories.
func (s *Store) AgentFile(instance, file string) string {
	return filepath.Join(s.agentDir(instance), file)
}

func (s *Store) snapshotDir(project, key string) string {
	return filepath.Join(s.Dir, "snapshots", project, key)
}

// HasSnapshot says whether project has a snapshot of key.
func (s *Store) HasSnapshot(project, key string) bool {
	_, err := os.Stat(filepath.Join(s.snapshotDir(project, key), metaFile))
	return err == nil
}

const metaFile = "meta.json"

type snapshotMeta struct {
	Lockfile string `json:"lockfile"`
	Bytes    int64  `json:"bytes"`
}

// Prepare makes instance's directories for groups, and fills each group's
// that are empty from the project's snapshot of its key, when there is one. It
// returns the lockfiles whose install it gave. A directory that already holds
// something — the agent's own, when its machine is made again — is left as
// it is.
func (s *Store) Prepare(instance, project string, installs []Install) (restored []string, err error) {
	if err := s.mkdirs(s.agentDir(instance)); err != nil {
		return nil, err
	}
	for _, in := range installs {
		empty := true
		for _, dir := range in.Dirs {
			target := s.MountDir(instance, dir)
			if err := s.mkdirs(target); err != nil {
				return restored, err
			}
			if entries, err := os.ReadDir(target); err != nil || len(entries) > 0 {
				empty = false
			}
		}
		if in.Key == "" || !empty {
			continue
		}
		snap, ok := s.useSnapshot(project, in.Key)
		if !ok {
			continue
		}
		var errs []error
		for _, dir := range in.Dirs {
			src := filepath.Join(snap, name(dir))
			if _, err := os.Lstat(src); err != nil {
				continue
			}
			errs = append(errs, s.copyTree(src, s.MountDir(instance, dir)))
		}
		s.doneWith(snap)
		if err := errors.Join(errs...); err != nil {
			// Half a node_modules is worse than none: the agent installs.
			for _, dir := range in.Dirs {
				_ = s.emptyDir(s.MountDir(instance, dir))
			}
			s.logf("node_modules: restoring %s for %s: %v", in.Lockfile, instance, err)
			continue
		}
		restored = append(restored, in.Lockfile)
	}
	return restored, nil
}

// useSnapshot finds project's snapshot of key and marks it used now and
// being copied, until doneWith.
func (s *Store) useSnapshot(project, key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.HasSnapshot(project, key) {
		return "", false
	}
	snap := s.snapshotDir(project, key)
	if s.restoring == nil {
		s.restoring = map[string]int{}
	}
	s.restoring[snap]++
	now := s.clock()
	_ = os.Chtimes(snap, now, now)
	return snap, true
}

func (s *Store) doneWith(snap string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restoring[snap]--; s.restoring[snap] <= 0 {
		delete(s.restoring, snap)
	}
}

// dispose moves path into trash/, from where emptyTrash deletes it once mu
// is let go. Its caller holds mu.
func (s *Store) dispose(path string) error {
	trash := filepath.Join(s.Dir, "trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return err
	}
	dst, err := os.MkdirTemp(trash, "")
	if err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dst, "x"))
}

// emptyTrash deletes what dispose put aside, and what a daemon that stopped
// meanwhile left there.
func (s *Store) emptyTrash() {
	trash := filepath.Join(s.Dir, "trash")
	entries, _ := os.ReadDir(trash)
	for _, e := range entries {
		if err := removeAll(filepath.Join(trash, e.Name())); err != nil {
			s.logf("node_modules: deleting %s: %v", filepath.Join(trash, e.Name()), err)
		}
	}
}

// Capture keeps what instance installed for each of installs as the
// project's snapshot of its key, when there's none yet and the install is
// newer than its lockfile, then removes instance's directories. Its machine
// must be gone: nothing may write in them meanwhile.
func (s *Store) Capture(instance, project string, installs []Install) {
	defer s.emptyTrash()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, in := range installs {
		if in.Key == "" || s.HasSnapshot(project, in.Key) {
			continue
		}
		if !installedAfter(s.MountDir(instance, in.Root()), in.LockTime) {
			continue
		}
		if err := s.capture(instance, project, in); err != nil {
			s.logf("node_modules: keeping %s of %s: %v", in.Lockfile, instance, err)
		}
	}
	if err := s.dispose(s.agentDir(instance)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.logf("node_modules: removing %s's: %v", instance, err)
	}
}

func (s *Store) capture(instance, project string, in Install) error {
	parent := filepath.Join(s.Dir, "snapshots", project)
	if err := s.mkdirs(parent); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".new-")
	if err != nil {
		return err
	}
	defer func() {
		if _, err := os.Lstat(tmp); err == nil {
			_ = s.dispose(tmp)
		}
	}()
	for _, dir := range in.Dirs {
		if err := os.Rename(s.MountDir(instance, dir), filepath.Join(tmp, name(dir))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	meta, err := json.Marshal(snapshotMeta{Lockfile: in.Lockfile, Bytes: treeBytes(tmp)})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, metaFile), meta, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.snapshotDir(project, in.Key))
}

// installedAfter says whether node_modules has an install marker newer than
// the lockfile.
func installedAfter(nodeModules string, lockTime time.Time) bool {
	for _, m := range Markers {
		if info, err := os.Stat(filepath.Join(nodeModules, m)); err == nil && !info.ModTime().Before(lockTime) {
			return true
		}
	}
	return false
}

// Remove removes instance's directories without keeping anything: a machine
// that failed to be made.
func (s *Store) Remove(instance string) {
	defer s.emptyTrash()
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.dispose(s.agentDir(instance))
}

// Trim removes agents' directories whose instance isn't in live, snapshots
// of projects not in projects, all but the KeepPerLockfile newest snapshots
// of a lockfile, and then the snapshots used longest ago while they hold more
// than Max or the disk is below its floor. It returns how much it freed.
func (s *Store) Trim(live, projects map[string]bool) int64 {
	defer s.emptyTrash()
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.clock().Add(-recent)
	var freed int64
	agents, _ := os.ReadDir(filepath.Join(s.Dir, "agents"))
	for _, e := range agents {
		if live[e.Name()] || !s.olderThan(filepath.Join(s.Dir, "agents", e.Name()), cutoff) {
			continue
		}
		dir := filepath.Join(s.Dir, "agents", e.Name())
		n := treeBytes(dir)
		if err := s.dispose(dir); err != nil {
			s.logf("node_modules: removing %s: %v", dir, err)
			continue
		}
		freed += n
	}

	type snap struct {
		path, project, lockfile string
		bytes                   int64
		used                    time.Time
	}
	var all []snap
	projectDirs, _ := os.ReadDir(filepath.Join(s.Dir, "snapshots"))
	for _, p := range projectDirs {
		pdir := filepath.Join(s.Dir, "snapshots", p.Name())
		keys, _ := os.ReadDir(pdir)
		for _, k := range keys {
			path := filepath.Join(pdir, k.Name())
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			var meta snapshotMeta
			b, err := os.ReadFile(filepath.Join(path, metaFile))
			if err != nil || json.Unmarshal(b, &meta) != nil {
				// Half made: a capture under way, or one that died.
				if info.ModTime().Before(cutoff) {
					_ = s.dispose(path)
				}
				continue
			}
			if s.restoring[path] > 0 {
				continue
			}
			all = append(all, snap{path, p.Name(), meta.Lockfile, meta.Bytes, info.ModTime()})
		}
	}
	slices.SortFunc(all, func(a, b snap) int { return b.used.Compare(a.used) }) // newest first
	limit := DefaultMax
	if s.Max != nil {
		if m := s.Max(); m > 0 {
			limit = m
		}
	}
	var total int64
	for _, sn := range all {
		total += sn.bytes
	}
	// Below the floor, what's evicted is deleted at once and the disk
	// measured again: a clone's extents are shared with the agents given it,
	// so a snapshot's size isn't what deleting it frees.
	room := func() int64 {
		if s.Room == nil {
			return 0
		}
		s.emptyTrash()
		return s.Room()
	}
	seen := map[[2]string]int{}
	var kept []snap
	for _, sn := range all {
		k := [2]string{sn.project, sn.lockfile}
		seen[k]++
		if (projects[sn.project] && seen[k] <= KeepPerLockfile) || sn.used.After(cutoff) {
			kept = append(kept, sn)
			continue
		}
		if s.evict(sn.path) {
			total -= sn.bytes
			freed += sn.bytes
		}
	}
	// Oldest first now, while over the cap or below the floor.
	short := room() < 0
	for i := len(kept) - 1; i >= 0 && (total > limit || short); i-- {
		sn := kept[i]
		if sn.used.After(cutoff) {
			break
		}
		if s.evict(sn.path) {
			total -= sn.bytes
			freed += sn.bytes
			if short {
				short = room() < 0
			}
		}
	}
	return freed
}

func (s *Store) evict(path string) bool {
	if err := s.dispose(path); err != nil {
		s.logf("node_modules: evicting %s: %v", path, err)
		return false
	}
	return true
}

func (s *Store) olderThan(path string, cutoff time.Time) bool {
	info, err := os.Stat(path)
	return err == nil && info.ModTime().Before(cutoff)
}

// mkdirs makes dir and its parents up to s.Dir, owned by Owner.
func (s *Store) mkdirs(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if s.Owner == nil {
		return nil
	}
	for d := dir; strings.HasPrefix(d, s.Dir) && len(d) >= len(s.Dir); d = filepath.Dir(d) {
		if err := os.Lchown(d, s.Owner[0], s.Owner[1]); err != nil {
			return err
		}
		if d == s.Dir {
			break
		}
	}
	return nil
}

func (s *Store) emptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		errs = append(errs, removeAll(filepath.Join(dir, e.Name())))
	}
	return errors.Join(errs...)
}

func (s *Store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Store) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// copyTree makes dst, an empty directory, a copy of src: clones where the file
// system can (btrfs, XFS), which share data only until either is written, and
// plain copies where it can't (ext4). Never hard links: a file an agent edits
// in place — patch-package, a postinstall, a debugging edit — would change in
// the snapshot and in every agent given it. Files and directories get src's
// modification times back, which npm compares with its hidden lockfile's to
// trust it.
func (s *Store) copyTree(src, dst string) error {
	type dirTime struct {
		path string
		mod  time.Time
		perm fs.FileMode
	}
	var dirs []dirTime
	clone := true
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if rel != "." {
				// Writable while it's filled, its own mode after: a
				// read-only directory would refuse its children.
				if err := os.Mkdir(target, info.Mode().Perm()|0o700); err != nil {
					return err
				}
				if err := s.chown(target); err != nil {
					return err
				}
			}
			dirs = append(dirs, dirTime{target, info.ModTime(), info.Mode().Perm()})
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
			return s.chown(target)
		case d.Type().IsRegular():
			if clone {
				err := cloneFile(p, target, info)
				if err == nil {
					return s.chown(target)
				}
				if !errors.Is(err, errNoClone) {
					return err
				}
				clone = false
			}
			if err := copyFile(p, target, info); err != nil {
				return err
			}
			return s.chown(target)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if i > 0 {
			_ = os.Chmod(dirs[i].path, dirs[i].perm)
		}
		_ = os.Chtimes(dirs[i].path, dirs[i].mod, dirs[i].mod)
	}
	return nil
}

// copyFile copies src to dst, a new file, with src's mode and times.
func copyFile(src, dst string, info fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

func (s *Store) chown(path string) error {
	if s.Owner == nil {
		return nil
	}
	return os.Lchown(path, s.Owner[0], s.Owner[1])
}

// treeBytes is what the files under dir hold, each file once however many
// links it has.
func treeBytes(dir string) int64 {
	var n int64
	seen := map[[2]uint64]bool{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if id, ok := inode(info); ok {
			if seen[id] {
				return nil
			}
			seen[id] = true
		}
		n += diskSize(info)
		return nil
	})
	return n
}

// removeAll removes a tree, read-only directories included.
func removeAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	return os.RemoveAll(path)
}
