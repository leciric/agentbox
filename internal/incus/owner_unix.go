//go:build unix

package incus

import (
	"os"
	"syscall"
)

// owner is who owns a host file, which PushFile gives the file it makes.
func owner(info os.FileInfo) (uid, gid int64) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Uid), int64(st.Gid)
	}
	return 0, 0
}
