package chat

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/state"
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

	// Set for an adapter running somewhere the agent's paths don't reach —
	// the Home chat's on the user's computer (package homehost): its sessions
	// start in Cwd rather than the agent's worktree, with MCPServers rather
	// than none, as its tool's configuration there doesn't name AgentBox's.
	Cwd        string
	MCPServers []acp.McpServer
	// Notice, when set, is said in the chat as the adapter starts: where it
	// runs, when that changed.
	Notice string
	// Moved says it runs on another machine than the adapter before it, which
	// keeps the session: it starts a new one, told the conversation.
	Moved bool
	// Auth, when set, is sent as authenticate once the adapter has answered
	// initialize, before any session: how a Gemini CLI API key reaches the
	// tool without being in its environment.
	Auth *acp.AuthenticateRequest
}

// sessionCwd is where a session of a's on proc starts.
func sessionCwd(proc *Process, a state.Agent) string {
	if proc != nil && proc.Cwd != "" {
		return proc.Cwd
	}
	return a.Worktree
}

// sessionMCP is the MCP servers a session on proc starts with.
func sessionMCP(proc *Process) []acp.McpServer {
	if proc != nil && proc.MCPServers != nil {
		return proc.MCPServers
	}
	return []acp.McpServer{}
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

// failureLine is the line of a failed adapter's stderr that says what went
// wrong: usually its last line, but a version manager ends its error with a
// line that only says how to get more ("mise ERROR Run with --verbose or
// MISE_VERBOSE=1 for more information"), and the line that names the failure
// comes before it.
func failureLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	var last string
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || moreInfo(line) {
			continue
		}
		if last == "" {
			last = line
		}
		// mise's own error, which may be followed by advice on how to fix it.
		if strings.HasPrefix(line, "mise ERROR") {
			return lastLine(line)
		}
	}
	if last == "" {
		return lastLine(s)
	}
	return lastLine(last)
}

// moreInfo is a line that only points at more output.
func moreInfo(line string) bool {
	return strings.Contains(line, "Run with --verbose") || strings.Contains(line, "MISE_VERBOSE=1")
}

// Remote is an adapter running somewhere StartCommand can't reach as a local
// process: in an agent's machine, through Incus' API (incus.Proc).
type Remote interface {
	Wait() error
	Kill()
}

// NewTail is a writer for a Remote's stderr that keeps what StartRemote's
// Process.Stderr gives back: the end of it.
func NewTail() io.Writer { return &tail{max: 16 << 10} }

// StartRemote is StartCommand for a Remote that is already running, with its
// stdin and stdout, and its stderr going to stderr, from NewTail.
func StartRemote(stdin io.WriteCloser, stdout io.ReadCloser, r Remote, stderr io.Writer) *Process {
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = r.Wait()
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
					r.Kill()
					<-done
				}
			})
		},
		Wait: func() error {
			<-done
			return waitErr
		},
		Stderr: stderr.(*tail).String,
	}
}
