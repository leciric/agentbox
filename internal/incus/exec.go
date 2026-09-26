package incus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	incusclient "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// ExitError is a command run in an instance through the API that exited with
// a status other than 0. It reads like the *exec.ExitError the incus command
// gave, "exit status 2", and has the same ExitCode.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "exit status " + strconv.Itoa(e.Code) }
func (e *ExitError) ExitCode() int { return e.Code }

// ExitCode returns the status a command in an instance exited with, whether
// it ran through the API (ExitError) or the incus command (*exec.ExitError);
// ok is false when err is not a command's exit.
func ExitCode(err error) (code int, ok bool) {
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		return exit.ExitCode(), true
	}
	return 0, false
}

// Exec runs a command in an instance as root and returns its stdout:
// `incus exec name -- command...`. It fails with the command's stderr.
func (c Client) Exec(ctx context.Context, name string, command ...string) (string, error) {
	return c.ExecInput(ctx, nil, name, command...)
}

// ExecInput is Exec with stdin, `incus exec name -T -- command...` when there
// is some: the command reads it, with no terminal in the way.
func (c Client) ExecInput(ctx context.Context, stdin io.Reader, name string, command ...string) (string, error) {
	args := []string{"exec", name}
	if stdin != nil {
		args = append(args, "-T")
	}
	args = append(append(args, "--"), command...)
	if c.cli() {
		return c.runInput(ctx, stdin, args...)
	}
	var stdout, stderr bytes.Buffer
	code, err := c.exec(ctx, name, command, stdin, &stdout, &stderr)
	switch {
	case err != nil:
		return stdout.String(), c.fail(args, err)
	case code == 0:
		return stdout.String(), nil
	}
	if msg := strings.TrimPrefix(strings.TrimSpace(stderr.String()), "Error: "); msg != "" {
		return stdout.String(), fmt.Errorf("incus %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), c.fail(args, &ExitError{Code: code})
}

// UserCommand prepares, without running it, a login shell of user running a
// bash command inside the instance, through the incus command: for a caller
// that hands it its own terminal, which the command gives a pty.
func (c Client) UserCommand(ctx context.Context, name, user, command string) *exec.Cmd {
	return c.Command(ctx, "exec", name, "--", "runuser", "-l", user, "-c", command)
}

// UserExec runs a bash command in a login shell of user inside the instance.
// A command that fails returns its exit status as an error, which ExitCode
// reads; when Incus itself fails, what it says goes to stderr, as the incus
// command wrote it there, and the status is 1.
func (c Client) UserExec(ctx context.Context, name, user, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	if c.cli() {
		cmd := c.UserCommand(ctx, name, user, command)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
		return cmd.Run()
	}
	code, err := c.exec(ctx, name, []string{"runuser", "-l", user, "-c", command}, stdin, stdout, stderr)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		if stderr != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %s\n", err)
		}
		code = max(code, 1)
	}
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

// exec runs a command in an instance through the API, the way `incus exec`
// does without a terminal: TERM passed on, the output copied until the command
// has closed it. It returns the command's exit status, and an error when Incus
// couldn't run it — with the status too, when it has one. If ctx ends first,
// it returns ctx's error without waiting for the command, which goes on
// running, as it did when the incus command was killed; nothing is written to
// stdout or stderr after it returns.
func (c Client) exec(ctx context.Context, name string, command []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	s, err := server(ctx)
	if err != nil {
		return -1, err
	}
	if stdin == nil {
		stdin = bytes.NewReader(nil) // what /dev/null gave the command
	}
	env := map[string]string{}
	if term, ok := os.LookupEnv("TERM"); ok {
		env["TERM"] = term
	}
	out := &outputs{}
	defer out.close()
	args := &incusclient.InstanceExecArgs{
		Stdin:    stdin,
		Stdout:   out.to(stdout),
		Stderr:   out.to(stderr),
		DataDone: make(chan bool),
	}
	op, err := s.ExecInstance(name, api.InstanceExecPost{Command: command, WaitForWS: true, Environment: env}, args)
	if err != nil {
		return -1, err
	}
	err = wait(ctx, op, nil)
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	code := -1
	if status, ok := op.Get().Metadata["return"].(float64); ok {
		code = int(status)
	}
	if err != nil {
		return code, err
	}
	select {
	case <-args.DataDone: // the output has all been copied
	case <-ctx.Done():
		return -1, ctx.Err()
	}
	return code, nil
}

// outputs guards a command's stdout and stderr: one write at a time, since
// callers pass the same buffer for both, as exec.Cmd allows, and none once
// exec has returned, since the buffer is its caller's again by then.
type outputs struct {
	mu     sync.Mutex
	closed bool
}

func (o *outputs) to(w io.Writer) io.Writer {
	if w == nil {
		return nil
	}
	return writerFunc(func(p []byte) (int, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.closed {
			return len(p), nil
		}
		return w.Write(p)
	})
}

func (o *outputs) close() {
	o.mu.Lock()
	o.closed = true
	o.mu.Unlock()
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// WriteFile writes content to path inside the instance, creating parent
// directories. The content goes through stdin, never the command line. It is
// read with cat: reopening /dev/stdin fails in unprivileged containers, because
// the pipe belongs to host root.
func (c Client) WriteFile(ctx context.Context, name, path string, content []byte, uid, gid int, mode os.FileMode) error {
	const script = `set -e; mkdir -p "$(dirname "$1")"; (umask 077 && cat >"$1"); chown "$2" "$1"; chmod "$3" "$1"`
	_, err := c.ExecInput(ctx, bytes.NewReader(content), name,
		"sh", "-c", script, "sh", path, fmt.Sprintf("%d:%d", uid, gid), strconv.FormatUint(uint64(mode.Perm()), 8))
	return err
}
