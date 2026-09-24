// Package mcp serves the Model Context Protocol over stdin and stdout:
// JSON-RPC 2.0, one message per line. AgentBox uses it to give a project's chat
// its tools, since that chat has no shell and a tool call is the only way it
// can act.
//
// Only what a tool server needs is implemented: initialize, tools/list and
// tools/call.
package mcp

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// ProtocolVersion is the MCP revision this server speaks.
const ProtocolVersion = "2024-11-05"

// Tool is one thing the model can call. Schema is the JSON Schema of its
// arguments, and Run returns the text the model sees. A tool that answers with
// something other than text — a screenshot, say — sets RunContent instead, and
// one of the two must be set.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Run         func(args json.RawMessage) (string, error)
	RunContent  func(args json.RawMessage) ([]Content, error)
}

// Content is one part of a tool's answer. Text carries text; Image carries a
// base64 image, with its media type.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

// Text is a text part of a tool's answer.
func Text(text string) Content { return Content{Type: "text", Text: text} }

// Image is an image part of a tool's answer, from the image's raw bytes.
func Image(data []byte, mime string) Content {
	return Content{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MIMEType: mime}
}

// Server answers MCP requests on a pair of streams.
type Server struct {
	Name    string
	Version string
	Tools   []Tool

	mu  sync.Mutex
	out *bufio.Writer
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Serve reads requests until the input ends. A request that fails is answered
// with an error rather than ending the session: the model reads the error and
// tries something else.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	s.out = bufio.NewWriter(out)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg message
		if err := json.Unmarshal(line, &msg); err != nil {
			s.reply(nil, nil, &rpcError{Code: codeParse, Message: "invalid JSON"})
			continue
		}
		// A notification has no id and takes no answer.
		if len(msg.ID) == 0 {
			continue
		}
		result, rpcErr := s.handle(msg)
		s.reply(msg.ID, result, rpcErr)
	}
	return scanner.Err()
}

func (s *Server) handle(msg message) (any, *rpcError) {
	switch msg.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.Tools))
		for _, t := range s.Tools {
			schema := t.Schema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": schema})
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &call); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
		}
		for _, t := range s.Tools {
			if t.Name != call.Name {
				continue
			}
			content, err := t.run(call.Arguments)
			if err != nil {
				// A failed tool is a result the model can read and act on, not
				// a protocol error.
				return result([]Content{Text(err.Error())}, true), nil
			}
			return result(content, false), nil
		}
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("no tool named %q", call.Name)}
	case "":
		return nil, &rpcError{Code: codeInvalidRequest, Message: "no method"}
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("no method %q", msg.Method)}
}

// run calls whichever of the two shapes the tool was given.
func (t Tool) run(args json.RawMessage) ([]Content, error) {
	if t.RunContent != nil {
		return t.RunContent(args)
	}
	text, err := t.Run(args)
	if err != nil {
		return nil, err
	}
	return []Content{Text(text)}, nil
}

func result(content []Content, isError bool) map[string]any {
	if content == nil {
		content = []Content{}
	}
	return map[string]any{"content": content, "isError": isError}
}

func (s *Server) reply(id json.RawMessage, result any, rpcErr *rpcError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := message{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr}
	if data, err := json.Marshal(out); err == nil {
		s.out.Write(data)
		s.out.WriteByte('\n')
		s.out.Flush()
	}
}
