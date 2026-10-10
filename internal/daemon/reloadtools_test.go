package daemon

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
	waitFor(t, "the second turn", func() bool {
		s := session()
		return s.State == api.ChatReady && s.TurnStartedAt == nil && !s.ToolsChanged
	})
	if n := tool.started(); n != 3 {
		t.Errorf("the tool started %d times, want 3: at first, on reloading, and before the turn after the override", n)
	}
}

// TestOnlyChangedToolsReloadChats: a chat restarts its tool only when the MCP
// servers that tool reads changed. A connector added turned off, and every
// change after a daemon restart that leaves the agent's servers as they were
// (a token refresh, a status change), mark no chat; turning it on marks the
// chat once.
func TestOnlyChangedToolsReloadChats(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	capture := filepath.Join(root, "capture")
	if err := os.MkdirAll(capture, 0o755); err != nil {
		t.Fatal(err)
	}
	d := startTestDaemon(t, root, connectorsIncus, testConfig{env: map[string]string{"CAPTURE": capture}})
	ctx := context.Background()
	// The agent's chat runs Claude Code, whose ~/.claude.json is what the
	// daemon compares.
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", AI: "claude",
		Branch: "agentbox/agent-01", Worktree: "/worktrees/agent-01", Status: state.AgentReady, CreatedAt: time.Now(),
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	// What configure wrote as the agent was made.
	if _, err := d.srv.manager(nil).SyncConnectors(ctx, a.Project, ""); err != nil {
		t.Fatal(err)
	}

	tool := &resumingTool{}
	d.srv.chat.Launch, d.srv.chat.Prepare = tool.launch, nil
	session := func() api.ChatSession {
		th, err := d.srv.chat.Thread(a)
		if err != nil {
			t.Fatal(err)
		}
		return th.Session
	}
	turn := func(text string) {
		t.Helper()
		if _, err := d.srv.chat.Send(a, text); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the turn", func() bool { s := session(); return s.State == api.ChatReady && s.TurnStartedAt == nil })
	}
	turn("hi")

	off, on := false, true
	if _, err := d.client.SetConnector(ctx, a.Project, "notion", api.SetConnectorRequest{URL: "https://mcp.notion.com/mcp", Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	d.srv.connectorSyncs.Wait()
	if session().ToolsChanged {
		t.Error("a connector added turned off marked the chat")
	}

	if _, err := d.client.SetConnector(ctx, a.Project, "notion", api.SetConnectorRequest{URL: "https://mcp.notion.com/mcp", Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	d.srv.connectorSyncs.Wait()
	if !session().ToolsChanged {
		t.Fatal("turning the connector on didn't mark the chat")
	}
	turn("again")
	if n := tool.started(); n != 2 {
		t.Errorf("the tool started %d times, want 2: at first, and once before the turn after the connector was turned on", n)
	}

	// A daemon that has just started knows of no connector, and the first it
	// hears of one is whatever changes it next: here, what a token refresh
	// or a failed one tells it.
	d.srv.mu.Lock()
	d.srv.connectorsGiven = map[string]bool{}
	d.srv.mu.Unlock()
	c, err := d.srv.store.Connector(ctx, a.Project, "", "notion")
	if err != nil {
		t.Fatal(err)
	}
	d.srv.connectorChanged(c, false)
	d.srv.connectorSyncs.Wait()
	if session().ToolsChanged {
		t.Error("a token refresh after a restart marked the chat, whose MCP servers it left as they were")
	}
	turn("once more")
	if n := tool.started(); n != 2 {
		t.Errorf("the tool started %d times after the refresh, want still 2", n)
	}
}
