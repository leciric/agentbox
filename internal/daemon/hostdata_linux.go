package daemon

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// onSharedFS reports whether dir is on a file system the host shares with the
// VM: virtiofs, which is FUSE, as AgentBox's own VM shares the home directory,
// or 9p.
func onSharedFS(dir string) bool {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return false
	}
	return st.Type == unix.FUSE_SUPER_MAGIC || st.Type == unix.V9FS_MAGIC
}

// diskSpace is what's free on the file system that holds dir, to an
// unprivileged user, and its size, with what tells that file system from
// another, so one holding several of AgentBox's directories is measured once.
func diskSpace(dir string) (free, total int64, id string, ok bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, "", false
	}
	id = fmt.Sprintf("%x:%d:%d:%d", st.Type, st.Fsid.Val[0], st.Fsid.Val[1], st.Blocks)
	return int64(st.Bavail) * st.Bsize, int64(st.Blocks) * st.Bsize, id, true
}

// isMountpoint reports whether dir is a mount's root: on another device than
// its parent.
func isMountpoint(dir string) bool {
	var st, up unix.Stat_t
	if unix.Stat(dir, &st) != nil || unix.Stat(filepath.Dir(dir), &up) != nil {
		return false
	}
	return st.Dev != up.Dev
}
