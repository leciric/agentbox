package hostvm

import "golang.org/x/sys/unix"

// hostMemory is the machine's physical memory in bytes, or 0 if it won't say.
// A Linux host only runs the front end to test it (AGENTBOX_FRONT_END=vm).
func hostMemory() int64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0
	}
	return int64(info.Totalram) * int64(info.Unit)
}
