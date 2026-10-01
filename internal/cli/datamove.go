package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/daemon"
	"agentbox/internal/datamove"
	"agentbox/internal/hostos"
	"agentbox/internal/hostvm"
	"agentbox/internal/paths"
)

// MoveDataArgs is the command the app runs first, as it starts, so the move
// happens with nothing else waiting on it: `agentbox data move`. Any other
// command moves the data too; this one does only that.
var MoveDataArgs = []string{"data", "move"}

// MoveData moves AgentBox's data from where an earlier version kept it,
// ~/.local/share/agentbox, to ~/.agentbox (package datamove), before args, a
// command line, runs. It is a stat when there is nothing to move. Help and
// --version move nothing, so asking which version is installed never stops
// the VM.
//
// It runs wherever AgentBox's data is: on a VM's front end (the VM's disks,
// the worktrees, the forwarded socket), in the VM (the daemon's own), and on
// a machine running AgentBox itself. Not in an agent's machine, whose home
// has worktrees at the old paths, mounted; nor as root; nor in the VM on the
// host's home, which the VM sees shared, and whose data is the front end's
// to move.
func MoveData(args []string, log io.Writer) error {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil
	}
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v" || args[0] == "help" || args[0] == "--help" || args[0] == "-h" || args[0] == "completion") {
		return nil
	}
	if os.Geteuid() == 0 || inAgent() {
		return nil
	}
	m, err := dataMove(log)
	if err != nil || !m.Pending() {
		return err
	}
	if hostos.InVM() && daemon.CheckDataDir(m.From) != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	moved, err := m.Run(ctx)
	if moved {
		reloadUnit(ctx)
	}
	return err
}

func inAgent() bool {
	_, err := os.Stat(api.InAgentSocket)
	return err == nil
}

// dataMove is the move for this user: what to stop first, and what else
// names the old paths.
func dataMove(log io.Writer) (datamove.Move, error) {
	p, err := paths.Default()
	if err != nil {
		return datamove.Move{}, err
	}
	legacy, err := paths.Legacy()
	if err != nil {
		return datamove.Move{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return datamove.Move{}, err
	}
	old := paths.Paths{Config: p.Config, Data: legacy}
	m := datamove.Move{From: legacy, To: p.Data, Home: home, Log: log}
	if unit, err := unitPath(); err == nil {
		m.Files = append(m.Files, unit)
	}
	if hostos.InVM() {
		// The agents' worktrees are the host's, moved by its front end
		// from its own home's ~/.local/share/agentbox.
		if host := hostos.Home(); host != "" {
			m.Also = append(m.Also, [2]string{filepath.Join(host, ".local", "share", "agentbox"), filepath.Join(host, ".agentbox")})
		}
	} else {
		files, also := hostvm.MoveFiles(old)
		m.Files, m.Also = append(m.Files, files...), append(m.Also, also...)
	}
	m.Stop = func(ctx context.Context) error {
		if !hostos.InVM() {
			if err := hostvm.StopForMove(ctx, old, log); err != nil {
				return err
			}
		}
		// Its own path, whatever AGENTBOX_SOCKET says (Paths.Socket).
		return stopDaemonAt(ctx, filepath.Join(old.Data, "run", "agentbox.sock"))
	}
	return m, nil
}

// stopDaemonAt stops the daemon serving socket, if one does, and waits until
// it is gone.
func stopDaemonAt(ctx context.Context, socket string) error {
	c := api.NewClient(socket)
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	answers := c.Ping(pingCtx) == nil
	cancel()
	if answers {
		if err := c.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("stopping the daemon: %w", err)
		}
	}
	if !daemon.WaitStopped(socket, daemon.StopWait) {
		return fmt.Errorf("the daemon%s is still running after %s: stop it, then run agentbox again", daemon.StillRunning(socket), daemon.StopWait)
	}
	return nil
}

// reloadUnit has systemd read the daemon's unit again, which the move
// rewrote when it ran the app's copy of agentbox.
func reloadUnit(ctx context.Context) {
	unit, err := unitPath()
	if err != nil {
		return
	}
	if _, err := os.Stat(unit); err != nil {
		return
	}
	_ = exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()
}
