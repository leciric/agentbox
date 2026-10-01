package agent

import (
	"os"
	"path/filepath"
)

// legacyMediaDir is where media was kept before paths.Media named another
// directory: the data directory's media, when it isn't Media's. In a VM that
// is the VM's own disk, which the host's app can't open anything from, and
// what MoveMedia empties onto the share; "" when the two are the same.
func (m *Manager) legacyMediaDir() string {
	old := filepath.Join(m.Paths.Data, "media")
	if filepath.Clean(m.Paths.Media()) == old {
		return ""
	}
	return old
}

// MoveMedia moves the media kept in legacyMediaDir into paths.Media, one item
// at a time, so the app on the host can open it: a rename when both are on
// one file system, a copy and then a removal when they aren't (the VM's disk
// and the host's home). An item already there is kept and its old copy
// dropped. MediaPath finds an item where it was until it has moved, so this
// runs in the background; what it couldn't move stays, for the next start.
func (m *Manager) MoveMedia() (moved int, err error) {
	old := m.legacyMediaDir()
	if old == "" {
		return 0, nil
	}
	items, err := filepath.Glob(filepath.Join(old, "*", "*", "*"))
	if err != nil || len(items) == 0 {
		return 0, err
	}
	var firstErr error
	for _, src := range items {
		rel, err := filepath.Rel(old, src)
		if err != nil {
			continue
		}
		if err := moveMediaItem(src, filepath.Join(m.Paths.Media(), rel)); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		moved++
	}
	// What's left are the agents' and projects' directories, empty now.
	agents, _ := filepath.Glob(filepath.Join(old, "*", "*"))
	for _, dir := range agents {
		_ = os.Remove(dir)
	}
	projects, _ := filepath.Glob(filepath.Join(old, "*"))
	for _, dir := range projects {
		_ = os.Remove(dir)
	}
	_ = os.Remove(old)
	return moved, firstErr
}

// moveMediaItem moves one item's directory from src to dst. A copy goes to a
// name beside dst first, so dst is never half there for MediaPath to find.
func moveMediaItem(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return os.RemoveAll(src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	tmp := dst + ".moving"
	_ = os.RemoveAll(tmp)
	if err := copyPath(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return os.RemoveAll(src)
}
