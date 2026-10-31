package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// fakeGemini speaks ACP the way Gemini CLI 0.64's --acp does: modes and
// models in a session's older fields, changed with session/set_mode and
// session/set_model (it has no set_config_option), a usage_update with the
// context window after each model result, and a turn's tokens in its own
// snake_case _meta.quota. Like Gemini CLI, it answers session/set_mode with
// an agent_message_chunk that reads "[MODE_UPDATE] <mode>". With hold set, a
// turn waits for it to close before it says anything.
type fakeGemini struct {
	mu    sync.Mutex
	conn  *acp.Conn
	mode  string
	model string
	calls []string
	hold  chan struct{}
}

func (g *fakeGemini) launch(_ context.Context, _ state.Agent, _ func(string)) (*Process, error) {
	toTool, fromChat := io.Pipe()
	toChat, fromTool := io.Pipe()
	exited := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = fromTool.Close()
			_ = toTool.Close()
			close(exited)
		})
	}
	g.mu.Lock()
	g.conn = acp.NewConn(toTool, fromTool, g)
	g.mu.Unlock()
	return &Process{Stdin: fromChat, Stdout: toChat, Stop: stop, Wait: func() error { <-exited; return nil }, Stderr: func() string { return "" }}, nil
}

func (g *fakeGemini) session() map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return map[string]any{
		"sessionId": "g-1",
		"modes": map[string]any{"currentModeId": g.mode, "availableModes": []map[string]string{
			{"id": "default", "name": "Default", "description": "Prompts for approval"},
			{"id": "autoEdit", "name": "Auto Edit", "description": "Auto-approves edit tools"},
			{"id": "yolo", "name": "YOLO", "description": "Auto-approves all tools"},
			{"id": "plan", "name": "Plan", "description": "Read-only mode"},
		}},
		"models": map[string]any{"currentModelId": g.model, "availableModels": []map[string]string{
			{"modelId": "auto", "name": "Auto"},
			{"modelId": "gemini-3.1-pro-preview", "name": "gemini-3.1-pro-preview"},
			{"modelId": "gemini-3.8-flash", "name": "gemini-3.8-flash"},
		}},
	}
}

func (g *fakeGemini) Request(method string, params json.RawMessage, reply func(any, error)) {
	var req struct {
		SessionID string `json:"sessionId"`
		ModeID    string `json:"modeId"`
		ModelID   string `json:"modelId"`
	}
	_ = json.Unmarshal(params, &req)
	g.mu.Lock()
	g.calls = append(g.calls, method)
	g.mu.Unlock()
	switch method {
	case acp.MethodInitialize:
		reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true},
			"agentInfo": map[string]any{"name": "gemini-cli", "title": "Gemini CLI", "version": "0.64.0"}}, nil)
	case acp.MethodSessionNew, acp.MethodSessionLoad:
		reply(g.session(), nil)
	case "session/set_mode":
		g.mu.Lock()
		g.mode = req.ModeID
		conn := g.conn
		g.mu.Unlock()
		_ = conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": req.SessionID,
			"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "[MODE_UPDATE] " + req.ModeID}}})
		reply(map[string]any{}, nil)
	case "session/set_model":
		g.mu.Lock()
		g.model = req.ModelID
		g.mu.Unlock()
		reply(map[string]any{}, nil)
	case acp.MethodSessionPrompt:
		go func() {
			g.mu.Lock()
			conn, model, hold := g.conn, g.model, g.hold
			g.mu.Unlock()
			if hold != nil {
				<-hold
			}
			_ = conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": req.SessionID,
				"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "Hello from Gemini"}}})
			_ = conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": req.SessionID,
				"update": map[string]any{"sessionUpdate": "usage_update", "used": 12_800, "size": 1_048_576}})
			reply(map[string]any{
				"stopReason": "end_turn",
				"usage":      map[string]any{"inputTokens": 12_000, "outputTokens": 800, "cachedReadTokens": 9_000, "thoughtTokens": 300, "totalTokens": 12_800},
				"_meta": map[string]any{"quota": map[string]any{
					"token_count": map[string]int{"input_tokens": 12_000, "output_tokens": 800},
					"model_usage": []map[string]any{{"model": model, "token_count": map[string]int{"input_tokens": 12_000, "output_tokens": 800}}},
				}},
			}, nil)
		}()
	default:
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
	}
}

func (g *fakeGemini) Notify(string, json.RawMessage) {}

func (g *fakeGemini) called(method string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Contains(g.calls, method)
}

// TestAGeminiCLITurn runs an autonomous Gemini CLI agent's chat against a fake
// that speaks Gemini CLI's ACP: its modes and models read as the chat's
// options, the model chosen for it goes out as session/set_model and full
// access as YOLO, the context window comes from its usage_update, its menu is
// remembered for Settings → Models, and the turn lands in the token ledger
// under the model, with the cache reads and thinking where they belong.
func TestAGeminiCLITurn(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	g := &fakeGemini{mode: "default", model: "auto"}
	a := state.Agent{Project: "hello", Name: "agent-01", AI: "gemini", Autonomous: true, Worktree: t.TempDir()}
	if err := store.SaveChat(context.Background(), a.Project, a.Name, state.Chat{Options: map[string]string{"model": "gemini-3.1-pro-preview"}}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Store: store, Version: "test", Publish: func(api.ChatEvent) {}, Launch: g.launch}
	t.Cleanup(m.Close)
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	option := func(th api.ChatThread, id string) api.ChatOption {
		for _, o := range th.Session.Options {
			if o.ID == id {
				return o
			}
		}
		return api.ChatOption{}
	}
	th := waitThread(t, m, a, "a session on the chosen model with full access", func(th api.ChatThread) bool {
		return th.Session.State == api.ChatReady && option(th, "model").Value == "gemini-3.1-pro-preview" && option(th, "mode").Value == "yolo"
	})
	if !g.called("session/set_model") || !g.called("session/set_mode") || g.called(acp.MethodSetConfigOption) {
		t.Errorf("Gemini CLI was asked %v", g.calls)
	}
	if got := len(option(th, "model").Choices); got != 3 {
		t.Errorf("the model menu has %d choices, want Gemini CLI's 3", got)
	}

	if _, err := m.Send(a, "Say hello"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, a, "the turn finishing", turnsEnded(1))
	if th.Session.ContextSize != 1_048_576 {
		t.Errorf("context size = %d, want the usage_update's", th.Session.ContextSize)
	}
	var rows []state.TokenRow
	var err error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if rows, err = store.TokenRows(context.Background(), state.TokenFilter{Project: a.Project, Agent: a.Name}, 10); err == nil && len(rows) > 0 {
			break
		}
	}
	if len(rows) != 1 {
		t.Fatalf("the token ledger: %+v, %v", rows, err)
	}
	if r := rows[0]; r.AI != "gemini" || r.Model != "gemini-3.1-pro-preview" || r.Input != 3_000 || r.CacheRead != 9_000 || r.Output != 1_100 {
		t.Errorf("the ledger row = %+v", fmt.Sprintf("%s %s in=%d cache=%d out=%d", r.AI, r.Model, r.Input, r.CacheRead, r.Output))
	}
	raw, err := store.Setting(context.Background(), state.SettingGeminiModelChoices)
	if err != nil || len(state.ChoiceValues(raw)) != 3 {
		t.Errorf("remembered menu = %s, %v", raw, err)
	}
}

// TestGeminiCLIModeNoticeStaysOutOfTheReply changes a Gemini CLI chat's mode
// while a turn runs: Gemini CLI's "[MODE_UPDATE] plan" notice is for its own
// interface, and the reply must not gain it.
func TestGeminiCLIModeNoticeStaysOutOfTheReply(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	g := &fakeGemini{mode: "default", model: "auto", hold: make(chan struct{})}
	a := state.Agent{Project: "hello", Name: "agent-01", AI: "gemini", Worktree: t.TempDir()}
	m := &Manager{Store: store, Version: "test", Publish: func(api.ChatEvent) {}, Launch: g.launch}
	t.Cleanup(m.Close)
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "a ready session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	if _, err := m.Send(a, "Say hello"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the turn running", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	if _, err := m.SetOption(context.Background(), a, "mode", "autoEdit"); err != nil {
		t.Fatal(err)
	}
	close(g.hold)
	th := waitThread(t, m, a, "the turn finishing", turnsEnded(1))
	for _, it := range th.Items {
		if strings.Contains(it.Text, "MODE_UPDATE") {
			t.Errorf("a %s item reads %q", it.Kind, it.Text)
		}
	}
}
