//go:build unix

package mcp

import (
	"os/exec"
	"syscall"
)

// ownGroup starts the server in a process group of its own, so that killing
// it kills what it started too: a mise shim and the Node process behind it.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
