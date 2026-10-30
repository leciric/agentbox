package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/coder/websocket"
)

// Terminal opens a terminal on an agent: the WebSocket the app's terminal pane
// uses, which `agentbox shell` attaches to when the agent's machine is a pod.
// Binary frames are the terminal's bytes in both directions, and a text frame
// is a TerminalResize.
func (c *Client) Terminal(ctx context.Context, ref string, cols, rows uint16) (*websocket.Conn, error) {
	path, err := AgentPath(ref)
	if err != nil {
		return nil, err
	}
	q := url.Values{"cols": {strconv.Itoa(int(cols))}, "rows": {strconv.Itoa(int(rows))}}
	return c.dialStream(ctx, path+"/terminal?"+q.Encode())
}

// ExecStream opens one command on an agent: the daemon runs it in the agent's
// worktree, as the agent's user, and streams back what it prints. The command
// is a shell command line, which is what `agentbox exec` joins its words into.
func (c *Client) ExecStream(ctx context.Context, ref, command string) (*websocket.Conn, error) {
	path, err := AgentPath(ref)
	if err != nil {
		return nil, err
	}
	q := url.Values{"command": {command}}
	return c.dialStream(ctx, path+"/exec?"+q.Encode())
}

// dialStream opens a WebSocket on the daemon. A refusal before the upgrade
// becomes the same StatusError the rest of the API returns, so a caller shows
// the daemon's own sentence (an agent that is stopped, say) rather than a
// handshake failure.
func (c *Client) dialStream(ctx context.Context, path string) (*websocket.Conn, error) {
	conn, resp, err := websocket.Dial(ctx, c.streamURL(path), &websocket.DialOptions{HTTPClient: c.http})
	if err != nil {
		return nil, streamRefusal(resp, err)
	}
	// A frame is as big as the daemon's own buffer, and the standard limit is
	// one byte short of a full gateway frame once the stream tag is on it.
	conn.SetReadLimit(1 << 20)
	return conn, nil
}

// streamURL is where a WebSocket path is dialed on this client's daemon: ws://
// over a plain-HTTP socket or a local listener, wss:// through a hub.
func (c *Client) streamURL(path string) string {
	base := strings.TrimSuffix(c.base, "/")
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://") + path
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://") + path
	}
	return base + path
}

// streamRefusal reads the daemon's error out of a handshake that never
// upgraded. The WebSocket client keeps the first kilobyte of the body for
// this, which is more than an API error takes.
func streamRefusal(resp *http.Response, err error) error {
	if resp == nil || resp.Body == nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	var e Error
	if json.Unmarshal(data, &e) != nil || e.Error == "" {
		e.Error = strings.TrimSpace(string(data))
	}
	if e.Error == "" {
		return err
	}
	return &StatusError{Code: resp.StatusCode, Message: e.Error, Reason: e.Code}
}
