package chat

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Process is a running ACP adapter.
type Process struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	// Stop ends the process: it closes its input, and kills it if it doesn't exit.
	Stop func()
	// Wait returns once the process has exited.
	Wait func() error
	// Stderr returns the end of what the process wrote to stderr.
	Stderr func() string
}

// StartCommand starts cmd as an ACP adapter, which speaks over its stdin and stdout.
func StartCommand(cmd *exec.Cmd) (*Process, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// An os.Pipe, not StdoutPipe: Wait closes that one as soon as the process
	// exits, possibly before its last messages were read.
	stdout, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = w
	stderr := &tail{max: 16 << 10}
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = w.Close()
		return nil, err
	}
	_ = w.Close()

	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	var once sync.Once
	return &Process{
		Stdin:  stdin,
		Stdout: stdout,
		Stop: func() {
			once.Do(func() {
				_ = stdin.Close()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					_ = cmd.Process.Kill()
					<-done
				}
			})
		},
		Wait: func() error {
			<-done
			return waitErr
		},
		Stderr: stderr.String,
	}, nil
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// lastLine is the last line of s with any text, shortened for an error message.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if r := []rune(line); len(r) > 300 {
		line = string(r[:300]) + "…"
	}
	return line
}
