package daemon

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/connectors/connectorstest"
	"agentbox/internal/state"
)

// resumingTool is an AI tool's adapter that resumes sessions, counting the
// times it is started.
type resumingTool struct {
	mu       sync.Mutex
	launches int
}

func (r *resumingTool) launch(context.Context, state.Agent, func(string)) (*chat.Process, error) {
	toTool, fromChat := io.Pipe()
	toChat, fromTool := io.Pipe()
	exited := make(chan struct{})
	var once sync.Once
	acp.NewConn(toTool, fromTool, r)
	r.mu.Lock()
	r.launches++
	r.mu.Unlock()
	return &chat.Process{
		Stdin: fromChat, Stdout: toChat,
		Stop: func() {
			once.Do(func() {
				_ = fromTool.Close()
				_ = toTool.Close()
				close(exited)
			})
		},
		Wait:   func() error { <-exited; return nil },
		Stderr: func() string { return "" },
	}, nil
}

func (r *resumingTool) started() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.launches
}

func (r *resumingTool) Notify(string, json.RawMessage) {}

func (r *resumingTool) Request(method string, _ json.RawMessage, reply func(any, error)) {
	switch method {
	case acp.MethodInitialize:
		reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"sessionCapabilities": map[string]any{"resume": map[string]any{}}}}, nil)
	case acp.MethodSessionNew:
		reply(map[string]any{"sessionId": "s1"}, nil)
	case acp.MethodSessionResume:
		reply(map[string]any{}, nil)
	case acp.MethodSessionPrompt:
		reply(map[string]any{"stopReason": "end_turn"}, nil)
	default:
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
	}
}

// TestWideConnectorChangesReachRunningChats: an AgentBox-wide connector added,
// and a project's override of it, mark the running chats of every project
// they reach, which reload their tools before their next turn.
func TestWideConnectorChangesReachRunningChats(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	capture := filepath.Join(root, "capture")
	if err := os.MkdirAll(capture, 0o755); err != nil {
		t.Fatal(err)
	}
	d := startTestDaemon(t, root, connectorsIncus, testConfig{env: map[string]string{"CAPTURE": capture}})
	ctx := context.Background()
	a := addTestAgent(t, d)
	a.AI = "claude"
	fake := connectorstest.New()
	defer fake.Close()
	fake.Accept("pat-1")

	tool := &resumingTool{}
	d.srv.chat.Launch, d.srv.chat.Prepare = tool.launch, nil
	if _, err := d.srv.chat.Send(a, "hi"); err != nil {
		t.Fatal(err)
	}
	session := func() api.ChatSession {
		th, err := d.srv.chat.Thread(a)
		if err != nil {
			t.Fatal(err)
		}
		return th.Session
	}
	waitFor(t, "the first turn", func() bool { s := session(); return s.State == api.ChatReady && s.TurnStartedAt == nil })

	if _, err := d.client.SetConnector(ctx, "", "notion", api.SetConnectorRequest{URL: fake.MCP(), Secret: "NOTION_TOKEN", SecretValue: "pat-1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the AgentBox-wide connector to mark the chat", func() bool { return session().ToolsChanged })
	if _, err := d.srv.chat.ReloadTools(a); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the reloaded session", func() bool { s := session(); return s.State == api.ChatReady && !s.ToolsChanged })

	if _, err := d.client.SetConnectorOverride(ctx, "hello-stack", "notion", "off"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the project's override to mark the chat", func() bool { return session().ToolsChanged })
	if _, err := d.srv.chat.Send(a, "again"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second turn", func() bool { s := session(); return s.State == api.ChatReady && s.TurnStartedAt == nil && !s.ToolsChanged })
	if n := tool.started(); n != 3 {
		t.Errorf("the tool started %d times, want 3: at first, on reloading, and before the turn after the override", n)
	}
}
