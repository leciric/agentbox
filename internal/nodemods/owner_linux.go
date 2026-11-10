package nodemods

import (
	"io/fs"
	"path/filepath"
	"syscall"
)

// Foreign says whether anything in instance's directories belongs to someone
// other than uid: what container root made there (sudo npm, Docker), which a
// daemon that isn't root can neither keep nor delete.
func (s *Store) Foreign(instance string, uid int) bool {
	foreign := false
	_ = filepath.WalkDir(s.agentDir(instance), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
				foreign = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	return foreign
}
