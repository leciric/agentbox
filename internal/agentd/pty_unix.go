//go:build unix

package agentd

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/creack/pty"
)

// ptyTerminal is a program on a fresh pseudo-terminal in the pod. Its process
// is started in a new session by pty.StartWithSize, which makes it the leader
// of a new process group, so killGroup's negative pid reaches the program and
// everything it started.
type ptyTerminal struct {
	// file is the master side: what the program prints is read from it, and
	// what is typed is written to it.
	file *os.File
	cmd  *exec.Cmd
}

// startTerminal starts argv on a terminal of the given size.
func startTerminal(argv []string, dir string, env []string, cols, rows uint16) (terminal, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &ptyTerminal{file: file, cmd: cmd}, nil
}

func (t *ptyTerminal) Read(p []byte) (int, error) { return t.file.Read(p) }

func (t *ptyTerminal) Write(p []byte) (int, error) { return t.file.Write(p) }

// Resize changes the window the program draws in, which is what makes tmux
// redraw at the app's size.
func (t *ptyTerminal) Resize(cols, rows uint16) error {
	return pty.Setsize(t.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (t *ptyTerminal) Wait() error { return t.cmd.Wait() }

func (t *ptyTerminal) Kill() error { return killGroup(t.cmd) }

func (t *ptyTerminal) Close() error { return t.file.Close() }

// passwdShell is the shell field of this pod user's passwd entry, which is
// where an image records the shell its user is meant to get. It is empty when
// there is no entry for this user.
func passwdShell() string {
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	uid := os.Getuid()
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		if id, err := strconv.Atoi(fields[2]); err != nil || id != uid {
			continue
		}
		return strings.TrimSpace(fields[6])
	}
	return ""
}
