//go:build unix

package agentd

import (
	"os"
	"os/exec"
	"syscall"
)

// ownGroup gives a command a process group of its own, so that a cancelled
// stream takes the shell and whatever it started with it rather than only the
// shell.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup kills the command's whole process group. A negative pid names the
// group, and the kernel does not check that a pid exists before the signal, so
// the kill reaches the group rather than the one process.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
