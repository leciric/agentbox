package hostvm

import "golang.org/x/sys/unix"

// hostMemory is the Mac's physical memory in bytes, or 0 if it won't say.
func hostMemory() int64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(n)
}
