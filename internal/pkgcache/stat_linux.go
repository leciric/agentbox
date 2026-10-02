package pkgcache

import (
	"io/fs"
	"syscall"
	"time"
)

// diskSize is what a file takes on the disk: a sparse file or a small one
// takes its blocks, not its length.
func diskSize(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return info.Size()
}

// lastUsed is when a file was last read or written. Reads only move the
// access time once a day under relatime, Linux's default, which is plenty to
// tell what was used longest ago; on a noatime disk it's when it was written.
func lastUsed(info fs.FileInfo) time.Time {
	used := info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if atime := time.Unix(st.Atim.Sec, st.Atim.Nsec); atime.After(used) {
			used = atime
		}
	}
	return used
}
