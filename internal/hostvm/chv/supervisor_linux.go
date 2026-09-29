package chv

import (
	"os"
	"strconv"
	"syscall"
)

// childAttr makes a program the supervisor runs die with it (Pdeathsig), so
// a supervisor that crashed can't leave a VM running that nothing controls.
func childAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// processArgs is pid's command line, its arguments each ended by a NUL.
func processArgs(pid int) string {
	b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	return string(b)
}

// hostMemory is the host's physical memory in bytes, or 0 if it won't say.
func hostMemory() int64 {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return 0
	}
	return int64(info.Totalram) * int64(info.Unit)
}
