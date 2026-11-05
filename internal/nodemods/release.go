package nodemods

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// releasedFile, in an agent's directory, maps each lockfile whose install
// Release took away while its machine was stopped to the key it was kept
// under, for Restore to give back: the key at stop, with the Node.js ABI the
// agent ran then.
const releasedFile = "released.json"

// Hold keeps instance's directories to its caller until the returned func is
// called: Stop's Release, and Start's Restore through to its machine booting,
// so a start never mounts what a stop is still taking away.
func (s *Store) Hold(instance string) func() {
	s.mu.Lock()
	if s.held == nil {
		s.held = map[string]*sync.Mutex{}
	}
	m := s.held[instance]
	if m == nil {
		m = &sync.Mutex{}
		s.held[instance] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// Release frees what a stopped agent's node_modules hold on the disk: on one
// that can't clone (ext4), each agent has a whole copy, which a stopped agent
// kept for as long as it wasn't destroyed. Each install that is still the
// install itself (newer than its lockfile, untouched since) is either the
// project's snapshot of its key already, and is deleted, or becomes it.
// Restore gives them back before the machine starts again. Its machine must
// be stopped, and its caller hold it (Hold).
func (s *Store) Release(instance, project string, installs []Install) {
	defer s.emptyTrash()
	start := s.clock()
	// What container root made there, a daemon that isn't root can neither
	// move nor delete.
	if os.Geteuid() != 0 && s.Foreign(instance, os.Geteuid()) {
		s.logf("node_modules: not releasing %s's at stop: some of it isn't yours", instance)
		return
	}
	var keep []Install
	var kept []string
	for _, in := range installs {
		if in.Key == "" {
			continue
		}
		installed, ok := installTime(s.MountDir(instance, in.Root()))
		if !ok {
			continue // never installed, or released already
		}
		if installed.Before(in.LockTime) {
			kept = append(kept, in.Lockfile+" is older than its lockfile")
			continue
		}
		if changed, ok := s.untouched(instance, in, installed); !ok {
			kept = append(kept, changed+" changed after the install")
			continue
		}
		keep = append(keep, in)
	}
	var bytes int64
	for _, in := range keep {
		for _, dir := range in.Dirs {
			bytes += treeBytes(s.MountDir(instance, dir))
		}
	}
	s.mu.Lock()
	released := map[string]string{}
	for _, in := range keep {
		var err error
		if s.HasSnapshot(project, in.Key) {
			// Used now: the agent counts on it, which Trim goes by.
			now := s.clock()
			_ = os.Chtimes(s.snapshotDir(project, in.Key), now, now)
			for _, dir := range in.Dirs {
				if e := s.dispose(s.MountDir(instance, dir)); e != nil && !errors.Is(e, fs.ErrNotExist) {
					err = errors.Join(err, e)
				}
			}
		} else {
			err = s.capture(instance, project, in)
		}
		if err != nil {
			s.logf("node_modules: releasing %s of %s: %v", in.Lockfile, instance, err)
		}
		// Even half released: Restore empties what's left before it copies.
		released[in.Lockfile] = in.Key
	}
	err := s.addReleased(instance, released)
	s.mu.Unlock()
	if err != nil {
		s.logf("node_modules: recording what %s released: %v", instance, err)
	}
	if len(released) == 0 && len(kept) == 0 {
		return
	}
	line := fmt.Sprintf("node_modules: released %s's at stop: %s of %d installs in %s", instance, gib(bytes), len(released), s.clock().Sub(start).Round(100*time.Millisecond))
	if len(kept) > 0 {
		line += fmt.Sprintf("; kept %d: %s", len(kept), strings.Join(kept, ", "))
	}
	s.logf("%s", line)
}

// Restore gives instance back, before its machine starts, the installs
// Release took away, copied from the project's snapshots of the keys they
// were kept under. One whose snapshot is gone stays empty: the agent installs
// it again. Its caller holds it (Hold) until the machine has booted.
func (s *Store) Restore(instance, project string, installs []Install) {
	released := s.readReleased(instance)
	if len(released) == 0 {
		return
	}
	start := s.clock()
	var give []Install
	for _, in := range installs {
		key, ok := released[in.Lockfile]
		if !ok {
			continue
		}
		for _, dir := range in.Dirs {
			_ = s.emptyDir(s.MountDir(instance, dir))
		}
		in.Key = key
		give = append(give, in)
	}
	// Prepare forgets what was released.
	restored, err := s.Prepare(instance, project, give)
	if err != nil {
		s.logf("node_modules: giving %s back what it released: %v", instance, err)
	}
	line := fmt.Sprintf("node_modules: gave %s back %d of %d installs at start in %s", instance, len(restored), len(released), s.clock().Sub(start).Round(100*time.Millisecond))
	var missing []string
	for l := range released {
		if !slices.Contains(restored, l) {
			missing = append(missing, l)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		line += "; no snapshot left of " + strings.Join(missing, ", ")
	}
	s.logf("%s", line)
}

func (s *Store) readReleased(instance string) map[string]string {
	released := map[string]string{}
	if b, err := os.ReadFile(s.AgentFile(instance, releasedFile)); err == nil {
		_ = json.Unmarshal(b, &released)
	}
	return released
}

// addReleased adds lockfiles and their keys to instance's released ones. Its
// caller holds mu.
func (s *Store) addReleased(instance string, lockfiles map[string]string) error {
	if len(lockfiles) == 0 {
		return nil
	}
	released := s.readReleased(instance)
	for l, key := range lockfiles {
		released[l] = key
	}
	b, err := json.Marshal(released)
	if err != nil {
		return err
	}
	return os.WriteFile(s.AgentFile(instance, releasedFile), b, 0o644)
}

func gib(n int64) string { return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30)) }
