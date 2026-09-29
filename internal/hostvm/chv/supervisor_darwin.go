package chv

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// childAttr is nothing on a Mac: the vz driver's VM runs in the supervisor's
// own process, with no programs beside it.
func childAttr() *syscall.SysProcAttr { return nil }

// processArgs is pid's command line, its arguments each ended by a NUL:
// kern.procargs2 is the argument count, the executable's path, then the
// arguments, each ended by a NUL.
func processArgs(pid int) string {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return ""
	}
	return string(b[4:])
}

// hostMemory is the Mac's physical memory in bytes, or 0 if it won't say.
func hostMemory() int64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(n)
}

// lockHolder would be the supervisor holding a flock on file, from
// /proc/locks; there is no such thing on a Mac, so lockFile's own error is
// all there is.
func lockHolder(string) int { return 0 }

// maxOpenFiles is only Cloud Hypervisor's virtiofsd's concern, on Linux.
func maxOpenFiles() uint64 { return 0 }
