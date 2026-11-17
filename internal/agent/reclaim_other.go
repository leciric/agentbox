//go:build !linux

package agent

import "syscall"

// reclaimProcAttr puts a reclaim writer in a process group of its own.
// There are no cgroups to reclaim off Linux; this only keeps it building.
func reclaimProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
