package hostwsl

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideWindow keeps a wsl.exe the front end runs for itself from opening a
// console window, which it would when the front end has none: the app starts
// it without one.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// showWindow undoes hideWindow for a command that shares the front end's
// console.
func showWindow(cmd *exec.Cmd) { cmd.SysProcAttr = nil }

// startDetached starts cmd so that it outlives the front end: in a process
// group of its own, so the console's Ctrl-C doesn't reach it, with no console,
// and out of the job the front end is in when that job allows it, so closing
// whatever started the front end doesn't end it.
//
// It returns the command that started, which is a copy of cmd when the first
// try failed: an exec.Cmd can't be started twice.
func startDetached(cmd *exec.Cmd) (*exec.Cmd, error) {
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP)
	retry := &exec.Cmd{Path: cmd.Path, Args: cmd.Args, Env: cmd.Env, Dir: cmd.Dir, Stdin: cmd.Stdin, Stdout: cmd.Stdout, Stderr: cmd.Stderr}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags | windows.CREATE_BREAKAWAY_FROM_JOB}
	if err := cmd.Start(); err == nil {
		return cmd, nil
	}
	// A job that doesn't allow breaking away, as CI's doesn't, refuses the
	// whole start.
	retry.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
	return retry, retry.Start()
}

// runForeground runs a command that shares the console. Ctrl-C reaches every
// process on a console, so wsl.exe gets it and passes it to Linux; the front
// end ignores it, and waits for the command to finish or give up.
func runForeground(cmd *exec.Cmd) error {
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	return cmd.Run()
}
