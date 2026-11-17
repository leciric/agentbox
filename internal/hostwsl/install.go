package hostwsl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// InstallWSLArgs is what installs WSL itself: the Store's WSL, and the
// Virtual Machine Platform feature WSL 2 needs, with no distro of the user's
// (AgentBox imports its own).
var InstallWSLArgs = []string{"--install", "--no-distribution"}

// ErrRestartWindows is an install of WSL that went through, and counts after
// Windows restarts: the Virtual Machine Platform is only on from the next
// boot. Its exit code is restartExit, for the apps.
var ErrRestartWindows = errors.New("WSL is installed, and Windows has to restart to finish: restart it, then open AgentBox again")

// restartExit is what `agentbox wsl install` exits with when Windows has to
// restart before WSL can be used: the apps tell it from a failure by it.
const restartExit = 3

// errRefused is the administrator prompt answered no, or closed.
var errRefused = errors.New("installing WSL needs an administrator: answer yes to Windows' prompt (or, in PowerShell as administrator: wsl --install --no-distribution)")

// elevated runs a program as administrator, behind Windows' prompt, waits
// for it and returns how it exited. A variable for tests.
//
// It is PowerShell's Start-Process -Verb RunAs, since only ShellExecute can
// ask for elevation and Go has no wait for what it starts. The program gets
// a console window of its own, which is where wsl.exe shows its progress: an
// elevated program's output can't be read from one that isn't.
var elevated = func(ctx context.Context, exe string, args ...string) (int, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = psQuote(a)
	}
	script := fmt.Sprintf("$ErrorActionPreference = 'Stop'; try { $p = Start-Process -FilePath %s -ArgumentList %s -Verb RunAs -Wait -PassThru } catch { exit 1223 }; exit $p.ExitCode",
		psQuote(exe), strings.Join(quoted, ","))
	cmd := exec.CommandContext(ctx, powershell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	hideWindow(cmd)
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// cancelled is ERROR_CANCELLED, what ShellExecute fails with when the
// administrator prompt is refused; elevated's script exits with it.
const cancelled = 1223

// psQuote quotes s for PowerShell: in single quotes, where only a single
// quote means anything.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// powershell is Windows PowerShell, which every Windows has, by its full
// path when Windows says where it is.
func powershell() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		p := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "powershell.exe"
}

// InstallWSL installs WSL (`wsl --install --no-distribution`) as
// administrator, with one prompt from Windows, and says whether it can be
// used at once: ErrRestartWindows when it can't until Windows restarts. It is
// what the apps' Setup runs where it used to show the command, and is safe
// to run again: with WSL already installed, wsl.exe says so and changes
// nothing.
func (d *Distro) InstallWSL(ctx context.Context) error {
	if d.WSL == "" {
		// Windows 10 from 2004, and 11, have a wsl.exe that can install WSL
		// even before any of it is there.
		return errors.New("this Windows has no wsl.exe to install WSL with: AgentBox needs Windows 10 version 2004 or later, or Windows 11")
	}
	d.progress("Installing WSL (wsl --install --no-distribution): Windows asks for an administrator, then shows its progress in a window of its own")
	code, err := elevated(ctx, d.WSL, InstallWSLArgs...)
	if err != nil {
		return fmt.Errorf("couldn't ask Windows for an administrator: %w", err)
	}
	if code == cancelled {
		return errRefused
	}
	if code != 0 {
		return fmt.Errorf("wsl --install --no-distribution failed (exit status %d): run it in PowerShell as administrator to see why", code)
	}
	// WSL that answers and can start a WSL 2 distro needs no restart. The
	// feature it turns on, when it was off, counts from the next boot.
	if _, err := d.Version(ctx); err != nil {
		return ErrRestartWindows
	}
	if err := os.MkdirAll(d.Dir, 0o755); err == nil {
		if err := d.checkVirtualization(ctx); err != nil {
			return ErrRestartWindows
		}
	}
	return nil
}
