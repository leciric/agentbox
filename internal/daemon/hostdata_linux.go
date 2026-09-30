package daemon

import "golang.org/x/sys/unix"

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
