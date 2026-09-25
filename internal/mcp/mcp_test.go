package mcp_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agentbox/internal/mcp"
)

// speak sends lines to a server and returns what it answered.
func speak(t *testing.T, s *mcp.Server, requests ...string) []map[string]any {
	t.Helper()
	var out strings.Builder
	if err := s.Serve(strings.NewReader(strings.Join(requests, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var answers []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("answer isn't JSON: %q", line)
		}
		answers = append(answers, m)
	}
	return answers
}

func server() *mcp.Server {
	return &mcp.Server{
		Name: "agentbox", Version: "test",
		Tools: []mcp.Tool{
			{
				Name: "echo", Description: "says it back",
				Schema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
				Run: func(args json.RawMessage) (string, error) {
					var in struct {
						Text string `json:"text"`
					}
					json.Unmarshal(args, &in)
					return "you said " + in.Text, nil
				},
			},
			{
				Name: "fails", Description: "always fails",
				Run: func(json.RawMessage) (string, error) { return "", errors.New("it went wrong") },
			},
		},
	}
}

func TestServeAnswersTheToolProtocol(t *testing.T) {
	answers := speak(t, server(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hello"}}}`,
	)
	// The notification gets no answer, so three requests make three answers.
	if len(answers) != 3 {
		t.Fatalf("got %d answers, want 3: %+v", len(answers), answers)
	}
	init := answers[0]["result"].(map[string]any)
	if init["protocolVersion"] != mcp.ProtocolVersion {
		t.Errorf("protocolVersion = %v", init["protocolVersion"])
	}
	if _, ok := init["capabilities"].(map[string]any)["tools"]; !ok {
		t.Errorf("initialize doesn't offer tools: %+v", init)
	}
	tools := answers[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools/list = %+v, want 2", tools)
	}
	first := tools[0].(map[string]any)
	if first["name"] != "echo" || first["description"] == "" {
		t.Errorf("first tool = %+v", first)
	}
	if _, ok := first["inputSchema"].(map[string]any); !ok {
		t.Errorf("a tool with no schema: %+v", first)
	}
	call := answers[2]["result"].(map[string]any)
	if call["isError"] != false {
		t.Errorf("a working tool reported an error: %+v", call)
	}
	if text := call["content"].([]any)[0].(map[string]any)["text"]; text != "you said hello" {
		t.Errorf("content = %v", text)
	}
}

// A tool that fails is a result the model can read, not a dead connection.
func TestAFailingToolIsAResult(t *testing.T) {
	answers := speak(t, server(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fails","arguments":{}}}`)
	result := answers[0]["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("isError = %v, want true", result["isError"])
	}
	if text := result["content"].([]any)[0].(map[string]any)["text"]; text != "it went wrong" {
		t.Errorf("the model can't see why it failed: %v", text)
	}
	if answers[0]["error"] != nil {
		t.Errorf("a failing tool became a protocol error: %+v", answers[0])
	}
}

func TestUnknownMethodsAndToolsAreRefusedWithoutEndingTheSession(t *testing.T) {
	answers := speak(t, server(),
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`not json at all`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
	)
	if len(answers) != 4 {
		t.Fatalf("got %d answers, want 4: %+v", len(answers), answers)
	}
	if answers[0]["error"] == nil {
		t.Errorf("an unknown method was accepted: %+v", answers[0])
	}
	if answers[1]["error"] == nil {
		t.Errorf("an unknown tool was accepted: %+v", answers[1])
	}
	if answers[2]["error"] == nil {
		t.Errorf("invalid JSON was accepted: %+v", answers[2])
	}
	// The session survived all of it.
	if answers[3]["result"] == nil {
		t.Errorf("the session ended early: %+v", answers[3])
	}
}
