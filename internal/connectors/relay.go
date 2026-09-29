package connectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
)

// Relay is the agent's side: an MCP server on stdin and stdout, as every AI
// tool starts one, that sends each message on to the daemon — which is the
// streamable HTTP transport's client role, with the daemon standing in for the
// server. It holds no credential: the daemon adds it.
type Relay struct {
	// HTTP reaches the daemon (over the agent's socket), and URL is the
	// connector's relay there: http://agentbox/v1/self/connectors/<name>/mcp.
	HTTP *http.Client
	URL  string
	// Log gets what the AI tool doesn't need to see: the GET stream's
	// troubles, a session the server ended. Stderr, from the command.
	Log io.Writer

	out     sync.Mutex
	w       io.Writer
	mu      sync.Mutex
	session string
	version string
	stream  bool // the GET stream has been started
}

// message is the part of a JSON-RPC message the relay looks at.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
}

func (m message) isRequest() bool { return m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null" }

// Serve relays until stdin ends, then ends the session.
func (r *Relay) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	r.w = out
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
		r.end()
	}()
	reader := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var m message
			if json.Unmarshal(line, &m) != nil {
				r.logf("not a JSON-RPC message, dropped: %.200s", line)
			} else if m.isRequest() && m.Method != "initialize" {
				// Requests run side by side: a slow tool call mustn't hold up
				// a cancellation, or a quick one behind it.
				wg.Add(1)
				go func(line []byte, m message) {
					defer wg.Done()
					r.post(ctx, line, m)
				}(line, m)
			} else {
				// initialize, notifications and responses go in order: the
				// session starts with the first, and the others are quick.
				r.post(ctx, line, m)
			}
			if m.Method == "notifications/initialized" {
				r.startStream(ctx, &wg)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// post sends one message and relays whatever answers it.
func (r *Relay) post(ctx context.Context, line []byte, m message) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(line))
	if err != nil {
		r.fail(m, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	r.sessionHeaders(req)
	resp, err := r.HTTP.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			r.fail(m, "couldn't reach AgentBox: "+err.Error())
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" && m.Method == "initialize" {
		r.mu.Lock()
		r.session = id
		r.mu.Unlock()
	}
	if resp.StatusCode >= 300 {
		r.fail(m, failure(resp))
		return
	}
	r.relay(resp, m.Method == "initialize")
}

// relay writes what a response carries to stdout: one JSON message or a batch,
// or an event stream's messages as they come.
func (r *Relay) relay(resp *http.Response, initialize bool) {
	switch ct := resp.Header.Get("Content-Type"); {
	case strings.HasPrefix(ct, "text/event-stream"):
		readEvents(resp.Body, func(data []byte) {
			if initialize {
				r.noteVersion(data)
			}
			r.write(data)
		})
	case strings.HasPrefix(ct, "application/json"):
		body, err := io.ReadAll(resp.Body)
		if err != nil || len(bytes.TrimSpace(body)) == 0 {
			return
		}
		var batch []json.RawMessage
		if json.Unmarshal(body, &batch) == nil {
			for _, m := range batch {
				r.write(m)
			}
			return
		}
		if initialize {
			r.noteVersion(body)
		}
		r.write(body)
	default:
		_, _ = io.Copy(io.Discard, resp.Body) // a 202's empty body
	}
}

// noteVersion keeps the protocol version initialize settled on, which every
// later request carries (MCP-Protocol-Version).
func (r *Relay) noteVersion(data []byte) {
	var res struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &res) == nil && res.Result.ProtocolVersion != "" {
		r.mu.Lock()
		r.version = res.Result.ProtocolVersion
		r.mu.Unlock()
	}
}

func (r *Relay) sessionHeaders(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session != "" {
		req.Header.Set("Mcp-Session-Id", r.session)
	}
	if r.version != "" {
		req.Header.Set("Mcp-Protocol-Version", r.version)
	}
}

// startStream opens the GET stream a server may send messages on outside any
// request, once: a server without one answers 405, and that is the end of it.
func (r *Relay) startStream(ctx context.Context, wg *sync.WaitGroup) {
	r.mu.Lock()
	started := r.stream
	r.stream = true
	r.mu.Unlock()
	if started {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for delay := time.Second; ctx.Err() == nil; {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
			if err != nil {
				return
			}
			req.Header.Set("Accept", "text/event-stream")
			r.sessionHeaders(req)
			began := time.Now()
			resp, err := r.HTTP.Do(req)
			if err == nil {
				ok := resp.StatusCode == http.StatusOK && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")
				if ok {
					readEvents(resp.Body, r.write)
				}
				_ = resp.Body.Close()
				if !ok {
					return
				}
			}
			if time.Since(began) > time.Minute {
				delay = time.Second // it was up for a while: this is a fresh drop
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			delay = min(2*delay, time.Minute)
		}
	}()
}

// end tells the server the session is over, as the transport asks a client
// that knows it is done to.
func (r *Relay) end() {
	r.mu.Lock()
	session := r.session
	r.mu.Unlock()
	if session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, r.URL, nil)
	if err != nil {
		return
	}
	r.sessionHeaders(req)
	if resp, err := r.HTTP.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// fail answers a request that couldn't be relayed with a JSON-RPC error, so the
// AI tool shows why rather than waiting forever. A notification has nobody to
// answer, and is only logged.
func (r *Relay) fail(m message, why string) {
	if !m.isRequest() {
		r.logf("%s: %s", cmpMethod(m.Method), why)
		return
	}
	answer, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      m.ID,
		"error":   map[string]any{"code": -32000, "message": why},
	})
	r.write(answer)
}

func cmpMethod(method string) string {
	if method == "" {
		return "a response"
	}
	return method
}

// failure says why a relayed request failed: the daemon's reason when it was
// the daemon's, or what the server answered.
func failure(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e api.Error
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		if resp.Header.Get(RelayHeader) != "" {
			return e.Error
		}
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	if resp.StatusCode == http.StatusNotFound && resp.Request != nil && resp.Request.Header.Get("Mcp-Session-Id") != "" {
		return "the server ended this session: restart the MCP server to start a new one"
	}
	if text == "" {
		return fmt.Sprintf("the server answered HTTP %d", resp.StatusCode)
	}
	return fmt.Sprintf("the server answered HTTP %d: %s", resp.StatusCode, text)
}

// write puts one message on stdout, whole and on one line.
func (r *Relay) write(data []byte) {
	var compact bytes.Buffer
	if json.Compact(&compact, data) != nil {
		return
	}
	compact.WriteByte('\n')
	r.out.Lock()
	defer r.out.Unlock()
	_, _ = r.w.Write(compact.Bytes())
}

func (r *Relay) logf(format string, args ...any) {
	if r.Log != nil {
		_, _ = fmt.Fprintf(r.Log, "agentbox connector: "+format+"\n", args...)
	}
}

// readEvents reads a server-sent event stream, handing each message event's
// data on. Events of another type — none are defined for MCP — are skipped.
func readEvents(body io.Reader, onData func([]byte)) {
	reader := bufio.NewReaderSize(body, 1<<20)
	var data bytes.Buffer
	event := ""
	dispatch := func() {
		if data.Len() > 0 && (event == "" || event == "message") {
			onData(bytes.TrimSuffix(data.Bytes(), []byte("\n")))
		}
		data.Reset()
		event = ""
	}
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "" && err == nil:
			dispatch()
		case strings.HasPrefix(line, ":"):
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "data":
				data.WriteString(value)
				data.WriteByte('\n')
			case "event":
				event = value
			}
		}
		if err != nil {
			dispatch()
			return
		}
	}
}
