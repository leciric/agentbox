package cli

// The two commands that talk to an agent's machine rather than to the API: the
// terminal `shell` opens and the command line `exec` runs. On this host both
// are the incus command, which owns the terminal itself; a cluster agent has no
// Incus here, so both go over the daemon, to the same WebSocket routes the app
// uses. The protocol is api's: binary frames carry bytes both ways and the text
// frames are the ends of the conversation.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"agentbox/internal/api"
)

// defaultTermSize is what a terminal is opened at when neither stream is one,
// which is the daemon's own default too.
const (
	defaultTermCols uint16 = 120
	defaultTermRows uint16 = 32
)

// clusterTerminal attaches this terminal to a cluster agent's, over the
// daemon's terminal WebSocket. It is the only way to reach a pod's terminal:
// there is no Incus on this host to open one in it.
func clusterTerminal(ctx context.Context, cmd *cobra.Command, c *api.Client, ag api.Agent) error {
	in, out := cmd.InOrStdin(), cmd.OutOrStdout()
	inFile, inTTY := ttyFile(in)
	outFile, outTTY := ttyFile(out)
	fd := -1
	switch {
	case outTTY:
		fd = int(outFile.Fd())
	case inTTY:
		fd = int(inFile.Fd())
	}
	cols, rows := defaultTermCols, defaultTermRows
	if fd >= 0 {
		if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
			cols, rows = uint16(w), uint16(h)
		}
	}

	conn, err := c.Terminal(ctx, ag.Ref, cols, rows)
	if err != nil {
		return err
	}
	defer func() { _ = conn.CloseNow() }()

	// Raw mode is what makes the keys the pod's: without it the local terminal
	// keeps line-buffering and echoing what is typed, over the pty doing the
	// same. A stream that is not a terminal, a pipe for instance, is left as
	// it is.
	if inTTY {
		state, err := term.MakeRaw(int(inFile.Fd()))
		if err != nil {
			return err
		}
		defer func() { _ = term.Restore(int(inFile.Fd()), state) }()
	}

	// Typing goes to the pod in the background, so the output keeps coming
	// while the local terminal waits for a key. The end of the input closes the
	// connection, which is what detaching is.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return
			}
		}
	}()

	if fd >= 0 {
		resize := make(chan os.Signal, 1)
		signal.Notify(resize, syscall.SIGWINCH)
		defer signal.Stop(resize)
		go func() {
			for range resize {
				w, h, err := term.GetSize(fd)
				if err != nil || w <= 0 || h <= 0 {
					continue
				}
				msg, err := json.Marshal(api.TerminalResize{Cols: uint16(w), Rows: uint16(h)})
				if err != nil {
					continue
				}
				if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
					return
				}
			}
		}()
	}

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			// Detaching from tmux, or the pod ending the session, closes the
			// connection the way a terminal ends: not an error.
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil
			}
			if ctx.Err() != nil {
				return exitCodeError(130)
			}
			return fmt.Errorf("the terminal ended: %w", err)
		}
		if typ != websocket.MessageBinary {
			continue
		}
		if _, err := out.Write(data); err != nil {
			return err
		}
	}
}

// clusterExec runs one command on a cluster agent, over the daemon's exec
// stream: the daemon runs it in the agent's worktree as its user, and this
// side is the command's streams. The last frame carries the status it exited
// with, which becomes the same exit code the incus path returns.
func clusterExec(ctx context.Context, cmd *cobra.Command, c *api.Client, ag api.Agent, command string) error {
	conn, err := c.ExecStream(ctx, ag.Ref, command)
	if err != nil {
		return err
	}
	defer func() { _ = conn.CloseNow() }()

	// stdin is copied in the background: a command that never reads it must not
	// hold its output, and a terminal that never ends must not hold the
	// command's own end. Its end is a text frame, because the stream stays open
	// for what the command still has to print.
	go func() {
		buf := make([]byte, 32*1024)
		eof, _ := json.Marshal(api.ExecStdin{Stdin: api.ExecStdinEOF})
		in := cmd.InOrStdin()
		for {
			n, err := in.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = conn.Write(ctx, websocket.MessageText, eof)
				return
			}
		}
	}()

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return exitCodeError(130)
			}
			return fmt.Errorf("the command's stream ended: %w", err)
		}
		if typ == websocket.MessageBinary {
			stream, payload, err := api.SplitExecOutput(data)
			if err != nil {
				return err
			}
			if stream == api.ExecStderr {
				_, _ = cmd.ErrOrStderr().Write(payload)
			} else {
				_, _ = cmd.OutOrStdout().Write(payload)
			}
			continue
		}
		var exit api.ExecExit
		if err := json.Unmarshal(data, &exit); err != nil {
			return fmt.Errorf("the command's last frame: %w", err)
		}
		switch {
		case exit.Error != "":
			// The command never ran: the daemon's reason is the error, and the
			// status it sends is the one to leave with.
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "error:", exit.Error)
			return exitCodeError(max(exit.Exit, 1))
		case exit.Exit != 0:
			return exitCodeError(exit.Exit)
		}
		return nil
	}
}

// ttyFile is the *os.File behind a stream, when the stream is one and it is a
// terminal: a command run with its streams on pipes has neither.
func ttyFile(v any) (*os.File, bool) {
	f, ok := v.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil, false
	}
	return f, true
}
