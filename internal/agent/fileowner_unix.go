//go:build !windows

package agent

import (
	"io/fs"
	"syscall"
)

// ownedByUser reports whether info describes a file this user owns. The cgroup
// files it is asked about are Linux's, where the owner is in Stat_t.
func ownedByUser(info fs.FileInfo, uid int) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}
