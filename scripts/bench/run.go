package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// runner runs one mode's commands. Their output goes to the mode's log file
// rather than the terminal, which only gets one line per timed step, and each
// runs in a process group of its own, so that cancelling its context (Ctrl-C)
// kills the whole tree it started — a build's compilers as well as its shell —
// and not only the shell.
type runner struct {
	mu    sync.Mutex
	log   io.Writer
	env   []string // added to this process's own environment
	unset []string // taken out of it
}

// run runs argv and returns its standard output, which is logged too. The
// error carries the tail of standard error, so a failed step says why in the
// results and not only in the log.
func (r *runner) run(ctx context.Context, stdin string, argv ...string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(r.environ(), r.env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	lw := &lockedWriter{r: r}
	cmd.Stdout = io.MultiWriter(&out, lw)
	cmd.Stderr = io.MultiWriter(&tail{buf: &errb, max: 2000}, lw)
	r.logf("$ %s", strings.Join(quoteAll(argv), " "))
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = lastLine(out.String())
		}
		if msg != "" {
			return out.String(), fmt.Errorf("%s: %w: %s", argv[0], err, lastLines(msg, 3))
		}
		return out.String(), fmt.Errorf("%s: %w", argv[0], err)
	}
	return out.String(), nil
}

// environ is this process's environment, less what the runner unsets.
func (r *runner) environ() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(r.unset, name) {
			out = append(out, kv)
		}
	}
	return out
}

// sh runs a shell command line: the configurable commands (--vm-*) are given
// as one.
func (r *runner) sh(ctx context.Context, line string) (string, error) {
	return r.run(ctx, "", "sh", "-c", line)
}

func (r *runner) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.log != nil {
		_, _ = fmt.Fprintf(r.log, "%s "+format+"\n", append([]any{time.Now().Format("15:04:05.000")}, args...)...)
	}
}

type lockedWriter struct{ r *runner }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.r.mu.Lock()
	defer w.r.mu.Unlock()
	if w.r.log == nil {
		return len(p), nil
	}
	return w.r.log.Write(p)
}

// tail keeps the last max bytes written to it.
type tail struct {
	buf *bytes.Buffer
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if extra := t.buf.Len() - t.max; extra > 0 {
		t.buf.Next(extra)
	}
	return len(p), nil
}

func lastLine(s string) string { return lastLines(s, 1) }

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// shq quotes s for a POSIX shell.
func shq(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,@+%", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func quoteAll(argv []string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = shq(a)
	}
	return out
}

// sleepCtx waits d, or less if ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
