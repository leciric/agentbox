package agent

import "syscall"

// reclaimProcAttr puts a reclaim writer in a process group of its own,
// killed with the daemon.
func reclaimProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
