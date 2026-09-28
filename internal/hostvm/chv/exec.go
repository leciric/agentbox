package chv

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
)

// sshHost is the name ssh is given for the VM. ssh never resolves it: the
// proxy command connects, and it only names the VM's host key in known_hosts.
const sshHost = "agentbox-vm"

// KnownHosts is the known_hosts file of the VM's ssh, its own so the VM's host
// key is kept apart from the user's.
func (l Layout) KnownHosts() string { return filepath.Join(l.Dir(), "ssh", "known_hosts") }

// ControlSocket is ssh's ControlMaster socket, which the VM's commands share
// one connection through: short, since a unix socket's path can't be longer
// than 107 bytes and ssh adds a random suffix while it makes it.
func (l Layout) ControlSocket() string { return filepath.Join(l.Run(), "ssh.ctl") }

// SSHArgs is the command line that runs argv in the VM as its user, in
// workdir, over ssh on vsock (self is this agentbox, which is ssh's
// ProxyCommand: `agentbox vm proxy 22`). tty asks for a terminal.
func SSHArgs(c Config, l Layout, self, workdir string, tty bool, argv []string) []string {
	args := []string{
		"ssh",
		"-i", l.Key(),
		"-o", "IdentitiesOnly=yes",
		"-o", "ProxyCommand=" + sshTokens(quoteSh(self)) + " vm proxy " + strconv.Itoa(PortSSH),
		"-o", "UserKnownHostsFile=" + sshPath(l.KnownHosts()),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		"-o", "LogLevel=ERROR",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + sshPath(l.ControlSocket()),
		"-o", "ControlPersist=600",
		"-o", "ServerAliveInterval=15",
		"-l", c.User,
	}
	if tty {
		args = append(args, "-t")
	} else {
		args = append(args, "-T")
	}
	args = append(args, sshHost)
	if cmd := remoteCommand(workdir, argv); cmd != "" {
		args = append(args, cmd)
	}
	return args
}

// remoteCommand is argv, run in workdir, as the one string ssh hands the VM
// user's shell. It never starts with "-", which ssh would take for an option.
func remoteCommand(workdir string, argv []string) string {
	if len(argv) == 0 {
		if workdir == "" {
			return ""
		}
		return "cd " + quoteSh(workdir) + ` && exec "$SHELL" -l`
	}
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = quoteSh(a)
	}
	cmd := "exec " + strings.Join(quoted, " ")
	if workdir != "" {
		cmd = "cd " + quoteSh(workdir) + " && " + cmd
	}
	return cmd
}

// sshTokens escapes the "%" ssh would expand in an option's value.
func sshTokens(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// sshPath is a path as an ssh option's value: its tokens escaped, and in
// double quotes when it has a space, which ssh would otherwise split it at.
func sshPath(s string) string {
	s = sshTokens(s)
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

// quoteSh quotes s for a POSIX shell, leaving it alone when it's safe as
// it is.
func quoteSh(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./=:,@+") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Proxy is `agentbox vm proxy PORT`: it connects to the VM's vsock port and
// copies stdin to it and it to stdout, until either side closes.
func Proxy(ctx context.Context, l Layout, port uint32, stdin io.Reader, stdout io.Writer) error {
	conn, err := dialVsock(ctx, l.VsockSocket(), port)
	if err != nil {
		return err
	}
	return proxy(ctx, conn, stdin, stdout)
}

// proxy copies stdin to conn and conn to stdout. The end of stdin is passed on
// as a half-close; the end of conn ends it all, since nothing more can be
// said once the VM's side has gone.
func proxy(ctx context.Context, conn net.Conn, stdin io.Reader, stdout io.Writer) error {
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	go func() {
		_, err := io.Copy(conn, stdin)
		if cw, ok := conn.(closeWriter); ok && err == nil {
			_ = cw.CloseWrite()
			return
		}
		_ = conn.Close()
	}()
	_, err := io.Copy(stdout, conn)
	if errors.Is(err, net.ErrClosed) {
		err = ctx.Err()
	}
	return err
}
