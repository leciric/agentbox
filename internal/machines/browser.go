package machines

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"agentbox/internal/mcp"
)

// The browser tools are Playwright's MCP server's, run in the machine against
// its Chromium (DevTools on 127.0.0.1:9222, browser.sh) and relayed: a few of
// its tools, with short descriptions of their own, since every tool's
// description is resent with each model call. Their arguments are
// Playwright's, pinned with it in internal/image/tools.txt.

// browserTools are the relayed tools: the name, a description, the schema of
// Playwright's arguments the tool offers, and what it gets for an argument
// Playwright requires and the schema leaves out.
var browserTools = []struct {
	name, description string
	schema            map[string]any
	defaults          map[string]any
}{
	{"browser_navigate", "Open a URL in the machine's Chromium.",
		object([]string{"url"}, map[string]any{"url": str("the URL")}), nil},
	{"browser_snapshot", "The page's accessibility tree, with a ref for each element.",
		object(nil, map[string]any{}), nil},
	{"browser_click", "Click an element of the page.",
		object([]string{"target"}, map[string]any{"target": target, "element": element}), nil},
	{"browser_type", "Type into an editable element of the page.",
		object([]string{"target", "text"}, map[string]any{"target": target, "element": element,
			"text": str("the text"), "submit": map[string]any{"type": "boolean", "description": "press Enter after"}}), nil},
	{"browser_evaluate", "Run a JavaScript function on the page and answer with its result.",
		object([]string{"function"}, map[string]any{"function": str("() => { … }")}), nil},
	{"browser_wait_for", "Wait for text to appear or disappear, or for a number of seconds.",
		object(nil, map[string]any{"text": str("text to wait for"), "textGone": str("text to wait to disappear"),
			"time": map[string]any{"type": "number", "description": "seconds"}}), nil},
	{"browser_console_messages", "The page's console messages.",
		object(nil, map[string]any{"level": map[string]any{"type": "string", "enum": []string{"error", "warning", "info", "debug"},
			"description": "the least severe to include; info by default"}}),
		map[string]any{"level": "info"}},
	{"browser_network_requests", "The requests the page has made, but static files.",
		object(nil, map[string]any{"filter": str("a regexp their URL matches")}),
		map[string]any{"static": false}},
}

var (
	target  = str("the element's ref from browser_snapshot, or a selector")
	element = str("what it is, in a few words")
)

// withDefaults is args with defaults for what they don't give.
func withDefaults(args json.RawMessage, defaults map[string]any) (json.RawMessage, error) {
	if len(defaults) == 0 {
		return args, nil
	}
	m := map[string]any{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &m); err != nil {
			return nil, err
		}
	}
	for k, v := range defaults {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

func object(required []string, props map[string]any) map[string]any {
	o := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// playwrightCommand is Playwright's MCP server in the machine. Its pictures
// stay out of the conversation, as in an agent (agentMCPServers): screenshot
// is the tool for those.
var playwrightCommand = []string{"playwright-mcp", "--cdp-endpoint", "http://127.0.0.1:9222",
	"--output-dir", "/tmp/playwright-mcp", "--image-responses", "omit"}

// mcpClient is one MCP session with a server on a pair of streams.
type mcpClient struct {
	mu     sync.Mutex
	in     io.WriteCloser
	out    *json.Decoder
	cmd    *exec.Cmd
	nextID int
}

// startClient starts cmd and opens an MCP session with it.
func startClient(cmd *exec.Cmd) (*mcpClient, error) {
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := newClient(in, out)
	c.cmd = cmd
	if err := c.initialize(); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func newClient(in io.WriteCloser, out io.Reader) *mcpClient {
	return &mcpClient{in: in, out: json.NewDecoder(bufio.NewReader(out))}
}

func (c *mcpClient) initialize() error {
	if _, err := c.call("initialize", map[string]any{
		"protocolVersion": mcp.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agentbox-machines", "version": "1"},
	}); err != nil {
		return err
	}
	return c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func (c *mcpClient) send(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.in.Write(append(line, '\n'))
	return err
}

// call sends one request and reads until its answer, answering a ping on
// the way and skipping notifications.
func (c *mcpClient) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := fmt.Sprint(c.nextID)
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := c.out.Decode(&m); err != nil {
			return nil, fmt.Errorf("the browser's tools stopped: %w", err)
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			answer := map[string]any{"jsonrpc": "2.0", "id": m.ID}
			if m.Method == "ping" {
				answer["result"] = map[string]any{}
			} else {
				answer["error"] = map[string]any{"code": -32601, "message": "not supported: " + m.Method}
			}
			if err := c.send(answer); err != nil {
				return nil, err
			}
		case m.Method != "", string(m.ID) != id:
		case m.Error != nil:
			return nil, errors.New(m.Error.Message)
		default:
			return m.Result, nil
		}
	}
}

// callTool calls a tool and returns its content as this server's.
func (c *mcpClient) callTool(name string, args json.RawMessage) ([]mcp.Content, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	raw, err := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, err
	}
	var res struct {
		Content []mcp.Content `json:"content"`
		IsError bool          `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if res.IsError {
		var parts []string
		for _, p := range res.Content {
			parts = append(parts, p.Text)
		}
		return nil, errors.New(strings.Join(parts, "\n"))
	}
	return res.Content, nil
}

func (c *mcpClient) close() {
	_ = c.in.Close()
	if c.cmd != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
}

// browserTool relays one tool to Playwright, starting it in the machine the
// first time and again after it stopped: with the machine, say.
func (s *Session) browserTool(ctx context.Context, name string, defaults map[string]any) func(json.RawMessage) ([]mcp.Content, error) {
	return func(args json.RawMessage) ([]mcp.Content, error) {
		args, err := withDefaults(args, defaults)
		if err != nil {
			return nil, err
		}
		if err := s.requireRunning(ctx); err != nil {
			return nil, err
		}
		for attempt := 0; ; attempt++ {
			c, err := s.browser(ctx)
			if err != nil {
				return nil, err
			}
			content, err := c.callTool(name, args)
			if err != nil && strings.HasPrefix(err.Error(), "the browser's tools stopped") && attempt == 0 {
				s.closeBrowser()
				continue
			}
			return content, err
		}
	}
}

func (s *Session) browser(ctx context.Context) (*mcpClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pw != nil {
		return s.pw, nil
	}
	// Not the call's context: the server outlives the call.
	cmd := s.Backend.Command(context.WithoutCancel(ctx), s.Worktree, playwrightCommand[0], playwrightCommand[1:]...)
	c, err := startClient(cmd)
	if err != nil {
		return nil, fmt.Errorf("starting the browser's tools: %w", err)
	}
	s.pw = c
	return c, nil
}

func (s *Session) closeBrowser() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pw != nil {
		s.pw.close()
		s.pw = nil
	}
}
