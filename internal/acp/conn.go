// Package acp is a client for the Agent Client Protocol
// (https://agentclientprotocol.com): JSON-RPC 2.0 over an agent's stdin and
// stdout, one message per line. AgentBox uses it to drive Claude Code and Codex
// through their ACP adapters, running inside agents.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// ErrClosed means the agent's side of the connection ended, usually because its
// process exited.
var ErrClosed = errors.New("the agent closed the connection")

// Handler receives what the agent sends on its own: notifications, and requests
// the client answers.
type Handler interface {
	// Notify is called for each notification, in the order they arrive, on the
	// goroutine that reads the connection. It must not block.
	Notify(method string, params json.RawMessage)
	// Request is called for each request, in the same order and on the same
	// goroutine, so it must not block either. It answers through reply, now or
	// later from another goroutine; only the first answer is sent.
	Request(method string, params json.RawMessage, reply func(result any, err error))
}

// Error is a JSON-RPC error, from the agent or for it.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return fmt.Sprintf("%s: %s", e.Message, e.Data)
	}
	return e.Message
}

// JSON-RPC error codes, and ACP's own.
const (
	CodeMethodNotFound = -32601
	CodeInternalError  = -32603
	CodeAuthRequired   = -32000
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type response struct {
	result json.RawMessage
	err    error
}

// Conn is one connection to an agent.
type Conn struct {
	w       io.Writer
	handler Handler
	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan response
	err     error // why the connection ended, once done is closed
	done    chan struct{}
}

// NewConn reads messages from r until it ends, and writes to w.
func NewConn(r io.Reader, w io.Writer, h Handler) *Conn {
	c := &Conn{w: w, handler: h, pending: map[int64]chan response{}, done: make(chan struct{})}
	go c.read(r)
	return c
}

// Done is closed when the connection has ended; Err then says why.
func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Call sends a request and waits for its response, which it decodes into
// result unless result is nil.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, c.err)
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.write(message{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: raw}); err != nil {
		c.forget(id)
		return fmt.Errorf("%s: %w", method, err)
	}
	select {
	case res := <-ch:
		if res.err != nil {
			return fmt.Errorf("%s: %w", method, res.err)
		}
		if result != nil && len(res.result) > 0 {
			if err := json.Unmarshal(res.result, result); err != nil {
				return fmt.Errorf("%s: reading the response: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

// Notify sends a notification, which has no response.
func (c *Conn) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(message{JSONRPC: "2.0", Method: method, Params: raw})
}

func (c *Conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Conn) write(m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}

func (c *Conn) read(r io.Reader) {
	// A message can be large (a tool call carrying a whole file), so read whole
	// lines rather than scan with a size limit.
	br := bufio.NewReaderSize(r, 1<<16)
	var err error
	for err == nil {
		var line []byte
		line, err = br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			c.dispatch(line)
		}
	}
	if errors.Is(err, io.EOF) {
		err = ErrClosed
	}
	c.mu.Lock()
	c.err = err
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- response{err: err}
	}
	close(c.done)
}

func (c *Conn) dispatch(line []byte) {
	var m message
	if json.Unmarshal(line, &m) != nil {
		return // not a message: ignore stray output
	}
	hasID := len(m.ID) > 0 && string(m.ID) != "null"
	switch {
	case m.Method != "" && hasID:
		var once sync.Once
		id := m.ID
		c.handler.Request(m.Method, m.Params, func(result any, err error) {
			once.Do(func() { c.reply(id, result, err) })
		})
	case m.Method != "":
		c.handler.Notify(m.Method, m.Params)
	case hasID:
		id, err := strconv.ParseInt(string(m.ID), 10, 64)
		if err != nil {
			return // not one of ours
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		switch {
		case !ok:
		case m.Error != nil:
			ch <- response{err: m.Error}
		default:
			ch <- response{result: m.Result}
		}
	}
}

func (c *Conn) reply(id json.RawMessage, result any, err error) {
	m := message{JSONRPC: "2.0", ID: id}
	if err == nil {
		raw, merr := json.Marshal(result)
		if merr == nil {
			m.Result = raw
		}
		err = merr
	}
	if err != nil {
		var rpcErr *Error
		if !errors.As(err, &rpcErr) {
			rpcErr = &Error{Code: CodeInternalError, Message: err.Error()}
		}
		m.Result, m.Error = nil, rpcErr
	}
	_ = c.write(m)
}
