package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/state"
)

// What a project's chat may run in its agents' machines (D89). The lead's own
// shell is the user's, on the host; a command that needs what an agent has set
// up — its toolchain, its services, its branch — runs in that agent's machine,
// through the same `incus exec` as `agentbox exec`. Everything here is bounded: what comes back stays in the
// chat's context for the rest of its session.

const (
	// DefaultLeadRunTimeout is how long a command runs when the chat doesn't say.
	DefaultLeadRunTimeout = time.Minute
	// MaxLeadRunTimeout is the longest the chat may ask for. Longer work is an
	// agent's task, not a command the chat waits on.
	MaxLeadRunTimeout = 10 * time.Minute
	// LeadRunTail is how much of a command's output the chat gets back: the
	// end of it, which is where a failure says what went wrong.
	LeadRunTail = 8 << 10
	// MaxLeadCopy is the most one copy between agents may move.
	MaxLeadCopy = 256 << 20
)

// RunResult is a command the chat ran in an agent's machine.
type RunResult struct {
	ExitCode int
	Output   string // the last LeadRunTail bytes of stdout and stderr, interleaved
	Bytes    int64  // how much output there was in all
	TimedOut bool
}

// ReachableForLead checks that the chat can act in an agent's machine: it is
// one of the project's agents, not the chat itself, and its machine is running.
// The errors are written for the chat, which can't start a machine itself.
func (m *Manager) ReachableForLead(ctx context.Context, a state.Agent) error {
	if a.IsLead() {
		return errors.New("that is the project's chat, which has no machine: name one of its agents")
	}
	if a.Status != state.AgentReady {
		return fmt.Errorf("%s is %s: its machine isn't ready yet", a.Name, a.Status)
	}
	inst, err := m.Incus.Instance(ctx, a.Instance)
	if err != nil {
		return fmt.Errorf("%s has no machine to run in: %w", a.Name, err)
	}
	switch inst.Status {
	case "Running":
		return nil
	case "Frozen":
		return fmt.Errorf("%s is paused (retired, or paused by the user): its machine can't run anything until it is resumed. "+
			"Create an agent for this instead, or ask the user to resume it", a.Name)
	default:
		return fmt.Errorf("%s's machine is %s (retired, or stopped by the user): it can't run anything until it is started. "+
			"Create an agent for this instead, or ask the user to start it", a.Name, strings.ToLower(inst.Status))
	}
}

// RunForLead runs a shell command in an agent's worktree, as the agent's user,
// with no stdin, for at most timeout. Only the tail of its output comes back.
func (m *Manager) RunForLead(ctx context.Context, a state.Agent, command string, timeout time.Duration) (RunResult, error) {
	if strings.TrimSpace(command) == "" {
		return RunResult{}, errors.New("no command to run")
	}
	switch {
	case timeout <= 0:
		timeout = DefaultLeadRunTimeout
	case timeout > MaxLeadRunTimeout:
		return RunResult{}, fmt.Errorf("a timeout of %s is longer than the %s a command may take: work that long is a task for an agent", timeout, MaxLeadRunTimeout)
	}
	if err := m.ReachableForLead(ctx, a); err != nil {
		return RunResult{}, err
	}
	// timeout(1) inside the machine is what stops the command: killing the
	// incus client on the host doesn't reliably kill what it started there. The
	// host's own deadline is only a backstop for a machine that stops answering.
	secs := strconv.Itoa(int(timeout.Round(time.Second) / time.Second))
	script := ExecCommand(a.Worktree, "exec timeout --kill-after=5 "+secs+" bash -c "+shellQuote(command)+" </dev/null")
	run, cancel := context.WithTimeout(ctx, timeout+30*time.Second)
	defer cancel()
	out := &tailBuffer{max: LeadRunTail}
	err := m.Incus.UserExec(run, a.Instance, m.User.Name, script, nil, out, out)
	result := RunResult{Output: out.String(), Bytes: out.total}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit) && run.Err() == nil:
		result.ExitCode = exit.ExitCode()
		// 124 is timeout(1) saying it stopped the command; 137 is its KILL
		// after --kill-after, for a command that ignored the TERM.
		result.TimedOut = result.ExitCode == 124 || result.ExitCode == 137
	case run.Err() != nil && ctx.Err() == nil:
		result.ExitCode, result.TimedOut = -1, true
	default:
		return result, fmt.Errorf("running in %s: %w", a.Name, err)
	}
	return result, nil
}

// CopyForLead copies a file or directory from one agent's machine to
// another's. from is resolved in the source agent's worktree unless it is
// absolute; into is the directory it lands in on the other side, the same
// place relative to the destination's worktree when empty. It streams a tar
// from one machine into the other, as each machine's own user, so nothing
// passes through the host's filesystem and a symlink arrives as a symlink, not
// as whatever it pointed to. It returns how many bytes of tar went across.
func (m *Manager) CopyForLead(ctx context.Context, src state.Agent, from string, dst state.Agent, into string, timeout time.Duration) (int64, error) {
	from = strings.TrimRight(strings.TrimSpace(from), "/")
	if from == "" || from == "." {
		return 0, errors.New("name the file or directory to copy: copying a whole worktree is a job for git, not a copy")
	}
	switch {
	case timeout <= 0:
		timeout = DefaultLeadRunTimeout
	case timeout > MaxLeadRunTimeout:
		return 0, fmt.Errorf("a timeout of %s is longer than the %s a copy may take", timeout, MaxLeadRunTimeout)
	}
	if src.Name == dst.Name {
		return 0, fmt.Errorf("%s is both ends of the copy: run cp there instead", src.Name)
	}
	for _, a := range []state.Agent{src, dst} {
		if err := m.ReachableForLead(ctx, a); err != nil {
			return 0, err
		}
	}
	if into = strings.TrimSpace(into); into == "" {
		into = path.Dir(from)
	}
	copyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pr, pw := io.Pipe()
	sent := &limitWriter{w: pw, max: MaxLeadCopy}
	srcErr := &tailBuffer{max: 1 << 10}
	dstErr := &tailBuffer{max: 1 << 10}
	pack := ExecCommand(src.Worktree, "tar -C "+shellQuote(path.Dir(from))+" -cf - -- "+shellQuote(path.Base(from)))
	unpack := ExecCommand(dst.Worktree, "mkdir -p -- "+shellQuote(into)+" && tar -C "+shellQuote(into)+" -xf -")
	packed := make(chan error, 1)
	go func() {
		err := m.Incus.UserExec(copyCtx, src.Instance, m.User.Name, pack, nil, sent, srcErr)
		pw.CloseWithError(err)
		packed <- err
	}()
	unpackErr := m.Incus.UserExec(copyCtx, dst.Instance, m.User.Name, unpack, pr, io.Discard, dstErr)
	pr.CloseWithError(errors.New("the destination stopped reading"))
	packErr := <-packed
	switch {
	case sent.over:
		return sent.n, fmt.Errorf("%s is more than %d MiB: moving that much is a job for git or an agent, not a copy", from, MaxLeadCopy>>20)
	case copyCtx.Err() != nil && ctx.Err() == nil:
		return sent.n, fmt.Errorf("the copy took longer than %s and was stopped", timeout)
	case packErr != nil:
		return sent.n, fmt.Errorf("reading %s in %s: %s", from, src.Name, firstLine(srcErr.String(), packErr))
	case unpackErr != nil:
		return sent.n, fmt.Errorf("writing into %s in %s: %s", into, dst.Name, firstLine(dstErr.String(), unpackErr))
	}
	return sent.n, nil
}

func firstLine(stderr string, err error) string {
	if line, _, _ := strings.Cut(strings.TrimSpace(stderr), "\n"); line != "" {
		return line
	}
	return err.Error()
}

// tailBuffer keeps the last max bytes written to it, and counts them all.
type tailBuffer struct {
	max   int
	buf   []byte
	total int64
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.total += int64(len(p))
	t.buf = append(t.buf, p...)
	// Trimmed once it is twice the size, so a chatty command isn't a copy per write.
	if len(t.buf) > 2*t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

// String is the tail, starting at a line when the cut fell inside one.
func (t *tailBuffer) String() string {
	tail := t.buf
	if len(tail) > t.max {
		tail = tail[len(tail)-t.max:]
	}
	if int64(len(tail)) < t.total {
		if i := strings.IndexByte(string(tail), '\n'); i >= 0 && i < len(tail)-1 {
			tail = tail[i+1:]
		}
	}
	return strings.ToValidUTF8(string(tail), "")
}

// limitWriter passes writes on until max bytes, then fails them.
type limitWriter struct {
	w    io.Writer
	max  int64
	n    int64
	over bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n+int64(len(p)) > l.max {
		l.over = true
		return 0, errors.New("copy too large")
	}
	n, err := l.w.Write(p)
	l.n += int64(n)
	return n, err
}
