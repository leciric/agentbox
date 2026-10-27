package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"agentbox/internal/api"
	"agentbox/internal/incus"
)

// execStream runs one shell command in an agent's worktree over a WebSocket,
// and is what `agentbox exec` uses on a cluster agent: there the CLI has no
// Incus to run a command in a pod with, so the daemon runs it through the same
// manager and the same machine seam as every other pod operation.
//
// Binary frames are the command's bytes both ways: the client's to its stdin,
// and its stdout and stderr back, tagged in the frame's first byte
// (api.ExecStdout, api.ExecStderr) so the two keep their own order. A text
// frame from the client says its stdin ended, and a text frame {"exit":N} with
// the status the command exited with is the last thing this side sends. A
// local agent never gets here: `agentbox exec` runs the incus command directly
// on the host, as it always has.
func (s *Server) execStream(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	command := r.URL.Query().Get("command")
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("exec needs a command: GET /v1/agents/{project}/{agent}/exec?command=<command>")
	}
	m := s.manager(nil)
	// Refuse a stopped agent before the upgrade, as the terminal route does,
	// so the client is told what state its agent is in rather than watching a
	// socket close under it.
	if err := m.RequireRunning(r.Context(), a); err != nil {
		return err
	}

	// The socket is only reachable by this user, so any origin is fine.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil // Accept has already written the response
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// The command's stdin is fed from the client's frames. Closing the writer
	// is how the command sees the end of its input; closing the reader is how
	// a frame that arrives after the command ended stops blocking here.
	stdin, feed := io.Pipe()
	defer func() { _ = feed.Close() }()

	var writeMu sync.Mutex
	write := func(typ websocket.MessageType, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.Write(ctx, typ, data)
	}
	stdout := streamWriter{stream: api.ExecStdout, write: write}
	stderr := streamWriter{stream: api.ExecStderr, write: write}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = stdin.CloseWithError(io.ErrClosedPipe) }()
		err := m.Exec(ctx, a, command, stdin, stdout, stderr)
		if frame, ok := execExit(ctx, err); ok {
			_ = write(websocket.MessageText, frame)
		}
		// Closing is what ends the stream for a client still waiting on its
		// own stdin, which a command that never read it leaves untouched.
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if typ == websocket.MessageText {
			var in api.ExecStdin
			if json.Unmarshal(data, &in) == nil && in.Stdin == api.ExecStdinEOF {
				_ = feed.Close()
			}
			continue
		}
		// A write that fails is a command that has already ended: going on
		// reading is what lets its exit frame through.
		_, _ = feed.Write(data)
	}
	cancel()
	<-done
	return nil
}

// execExit is the last text frame of a stream: the status its command exited
// with, or the daemon's reason when the command never ran. ok is false when
// the client is gone and there is no one to tell.
func execExit(ctx context.Context, err error) (frame []byte, ok bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	exit := api.ExecExit{}
	if code, exited := incus.ExitCode(err); exited {
		exit.Exit = code
	} else if err != nil {
		exit.Exit, exit.Error = 1, err.Error()
	}
	frame, merr := json.Marshal(exit)
	if merr != nil {
		return nil, false
	}
	return frame, true
}

// streamWriter turns one of a command's output streams into tagged binary
// frames: the stream's tag first, then the bytes themselves.
type streamWriter struct {
	stream byte
	write  func(websocket.MessageType, []byte) error
}

func (w streamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := w.write(websocket.MessageBinary, api.ExecOutput(w.stream, p)); err != nil {
		return 0, err
	}
	return len(p), nil
}
