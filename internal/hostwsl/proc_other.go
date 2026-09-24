//go:build !windows

package hostwsl

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// On Linux the front end runs only in tests and demos (AGENTBOX_FRONT_END=wsl),
// with a fake wsl.exe: these are the nearest things to the Windows ones.

func hideWindow(*exec.Cmd) {}

func showWindow(*exec.Cmd) {}

func startDetached(cmd *exec.Cmd) (*exec.Cmd, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, cmd.Start()
}

func runForeground(cmd *exec.Cmd) error {
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	return cmd.Run()
}
