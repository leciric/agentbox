package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// A connector from the shell: `agentbox connector tools` and `connector call`
// inside an agent. Every AI tool reads its MCP servers only when its session
// starts, so a connector the user connects while an agent is working — on its
// request_connector — reaches the agent's native tools only with its next
// session. These reach it at once, through the same relay, as one short MCP
// session each: initialize, one request, and the session ended.

// protocolVersion is the MCP version a shell session asks for.
const protocolVersion = "2025-06-18"

// Tool is one of a connector's tools, as tools/list describes it.
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ToolResult is what tools/call answers with.
type ToolResult struct {
	Content           []json.RawMessage `json:"content"`
	StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError,omitempty"`
}

// Text is a result's text content, one block to a paragraph, with anything
// that isn't text — an image, a resource — as its JSON.
func (t ToolResult) Text() string {
	var parts []string
	for _, c := range t.Content {
		var block struct{ Type, Text string }
		if json.Unmarshal(c, &block) == nil && block.Type == "text" {
			parts = append(parts, block.Text)
			continue
		}
		parts = append(parts, string(c))
	}
	if len(parts) == 0 && len(t.StructuredContent) > 0 {
		return string(t.StructuredContent)
	}
	return strings.Join(parts, "\n\n")
}

// Tools lists a connector's tools, every page of them.
func (r *Relay) Tools(ctx context.Context) ([]Tool, error) {
	var tools []Tool
	err := r.oneSession(ctx, func(call func(method string, params any) (json.RawMessage, error)) error {
		cursor := ""
		for {
			params := map[string]any{}
			if cursor != "" {
				params["cursor"] = cursor
			}
			raw, err := call("tools/list", params)
			if err != nil {
				return err
			}
			var page struct {
				Tools      []Tool `json:"tools"`
				NextCursor string `json:"nextCursor"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return fmt.Errorf("tools/list answered with something else: %w", err)
			}
			tools = append(tools, page.Tools...)
			if page.NextCursor == "" || page.NextCursor == cursor {
				return nil
			}
			cursor = page.NextCursor
		}
	})
	return tools, err
}

// CallTool calls one of a connector's tools with arguments, a JSON object
// (nil for none).
func (r *Relay) CallTool(ctx context.Context, name string, arguments json.RawMessage) (ToolResult, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	var result ToolResult
	err := r.oneSession(ctx, func(call func(method string, params any) (json.RawMessage, error)) error {
		raw, err := call("tools/call", map[string]any{"name": name, "arguments": arguments})
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &result)
	})
	return result, err
}

// rpcMessage is a JSON-RPC message read back from the relay.
type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// oneSession runs one MCP session through the relay, the way an AI tool would
// over its stdio, and ends it when fn returns. The relay's stdin stays open
// until then: it cancels what is in flight when stdin ends.
func (r *Relay) oneSession(ctx context.Context, fn func(call func(method string, params any) (json.RawMessage, error)) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan error, 1)
	go func() {
		err := r.Serve(ctx, inR, outW)
		_ = outW.Close()
		served <- err
	}()
	defer func() {
		_ = inW.Close()
		_, _ = io.Copy(io.Discard, outR) // whatever is left, until the relay is done
		<-served
	}()

	messages := json.NewDecoder(outR)
	send := func(v any) error {
		line, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = inW.Write(append(line, '\n'))
		return err
	}
	next := 0
	call := func(method string, params any) (json.RawMessage, error) {
		next++
		id := fmt.Sprint(next)
		if err := send(map[string]any{"jsonrpc": "2.0", "id": next, "method": method, "params": params}); err != nil {
			return nil, err
		}
		for {
			var m rpcMessage
			if err := messages.Decode(&m); err != nil {
				if errors.Is(err, io.EOF) {
					return nil, fmt.Errorf("%s: the relay ended without an answer", method)
				}
				return nil, err
			}
			switch {
			case m.Method != "" && len(m.ID) > 0:
				// The server asking something of the client, which a shell
				// can't answer but a ping.
				answer := map[string]any{"jsonrpc": "2.0", "id": m.ID}
				if m.Method == "ping" {
					answer["result"] = map[string]any{}
				} else {
					answer["error"] = map[string]any{"code": -32601, "message": "agentbox connector call can't answer " + m.Method}
				}
				if err := send(answer); err != nil {
					return nil, err
				}
			case m.Method != "":
				// A notification: progress, a log line. Nothing to do.
			case string(m.ID) != id:
				// Not this call's.
			case m.Error != nil:
				return nil, errors.New(m.Error.Message)
			default:
				return m.Result, nil
			}
		}
	}
	if _, err := call("initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agentbox-connector-call", "version": "1"},
	}); err != nil {
		return err
	}
	if err := send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return err
	}
	return fn(call)
}
