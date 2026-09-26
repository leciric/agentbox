package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"agentbox/internal/api"
)

// terminal bridges a WebSocket to the agent's tmux session through a local
// pseudo-terminal. Every connection is its own tmux client, so several viewers
// see and type into the same session. Binary frames carry terminal bytes;
// text frames carry api.TerminalResize messages.
func (s *Server) terminal(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	m := s.manager(nil)
	if err := m.PrepareShell(r.Context(), a); err != nil {
		return err
	}
	size := &pty.Winsize{Cols: queryUint16(r, "cols", 120), Rows: queryUint16(r, "rows", 32)}

	// The socket is only reachable by this user, so any origin (the desktop app's) is fine.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil // Accept has already written the response
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	argv := m.ShellArgs(a)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return nil
	}
	defer func() {
		cancel()
		_ = ptmx.Close()
		_ = cmd.Wait()
	}()

	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if typ == websocket.MessageText {
			var resize api.TerminalResize
			if json.Unmarshal(data, &resize) == nil && resize.Cols > 0 && resize.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: resize.Cols, Rows: resize.Rows})
			}
			continue
		}
		if _, err := ptmx.Write(data); err != nil {
			break
		}
		// A keystroke, for "auto-stop idle agents": a viewer with nothing
		// typed into it isn't reason enough on its own to keep an agent
		// running, but somebody actually using the terminal is.
		s.touchTerminal(a.Ref())
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	return nil
}

// touchTerminal and lastTerminalInput track the last time anyone typed into
// an agent's terminal: in memory only, so a daemon restart forgets it, the
// same as it forgets who is connected. "auto-stop idle agents"
// (autostopidle.go) reads it as one of the things that count as activity.
func (s *Server) touchTerminal(ref string) {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	s.terminalActivity[ref] = time.Now()
}

func (s *Server) lastTerminalInput(ref string) (time.Time, bool) {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	at, ok := s.terminalActivity[ref]
	return at, ok
}

func queryUint16(r *http.Request, key string, fallback uint16) uint16 {
	v, err := strconv.ParseUint(r.URL.Query().Get(key), 10, 16)
	if err != nil || v == 0 {
		return fallback
	}
	return uint16(v)
}
