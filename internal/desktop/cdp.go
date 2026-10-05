package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// A DevTools protocol client for the agent's Chromium, just enough of one for
// a browser recording: listing its pages, calling a method and taking the
// events a screencast sends.

// DevTools is where the agents' Chromium serves the DevTools protocol.
const DevTools = "127.0.0.1:9222"

type cdpPage struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	URL       string `json:"url"`
	WebSocket string `json:"webSocketDebuggerUrl"`
}

// listPages lists the browser's tabs, the one shown last first.
func listPages(ctx context.Context, devtools string) ([]cdpPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+devtools+"/json/list", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var all []cdpPage
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, err
	}
	var pages []cdpPage
	for _, p := range all {
		if p.Type == "page" && p.WebSocket != "" {
			pages = append(pages, p)
		}
	}
	return pages, nil
}

type cdpConn struct {
	ws      *websocket.Conn
	next    atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan cdpMessage
	// event gets every event, on the connection's reading goroutine.
	event func(method string, params json.RawMessage)
	done  chan struct{}
}

type cdpMessage struct {
	ID     int64           `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func dialCDP(ctx context.Context, url string, event func(string, json.RawMessage)) (*cdpConn, error) {
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(64 << 20) // a screencast frame is a whole JPEG
	c := &cdpConn{ws: ws, pending: map[int64]chan cdpMessage{}, event: event, done: make(chan struct{})}
	go c.read()
	return c, nil
}

func (c *cdpConn) read() {
	defer close(c.done)
	for {
		_, data, err := c.ws.Read(context.Background())
		if err != nil {
			return
		}
		var m cdpMessage
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID == 0 {
			if c.event != nil {
				c.event(m.Method, m.Params)
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[m.ID]
		delete(c.pending, m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

func (c *cdpConn) call(ctx context.Context, method string, params, result any) error {
	id := c.next.Add(1)
	ch := make(chan cdpMessage, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if params == nil {
		params = struct{}{}
	}
	msg, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := c.ws.Write(ctx, websocket.MessageText, msg); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		if result != nil {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	case <-c.done:
		return errors.New("the browser closed the connection")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// send calls method without waiting for its answer.
func (c *cdpConn) send(ctx context.Context, method string, params any) {
	msg, _ := json.Marshal(map[string]any{"id": c.next.Add(1), "method": method, "params": params})
	_ = c.ws.Write(ctx, websocket.MessageText, msg)
}

func (c *cdpConn) Close() { _ = c.ws.CloseNow() }
