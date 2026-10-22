package daemon

import "golang.org/x/sys/unix"

// sharedFolder says whether dir is in a folder shared from another machine:
// virtiofs (Lima, Cloud Hypervisor), 9p, FUSE, NFS or SMB.
func sharedFolder(dir string) bool {
	var st unix.Statfs_t
	if unix.Statfs(dir, &st) != nil {
		return false
	}
	switch int64(st.Type) {
	case 0x6a656a63, unix.V9FS_MAGIC, unix.FUSE_SUPER_MAGIC, unix.NFS_SUPER_MAGIC, unix.SMB_SUPER_MAGIC, unix.SMB2_SUPER_MAGIC, unix.CIFS_SUPER_MAGIC:
		return true
	}
	return false
}
