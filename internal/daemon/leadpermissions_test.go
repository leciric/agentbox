package daemon

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// askingTool is an AI tool whose turns ask permission to run `npm test`
// before they end, when the prompt is "Run the tests", and end at once
// otherwise: the lead's prompts quote the request, and must not ask again.
type askingTool struct {
	promptTool
}

type askingSession struct {
	tool *askingTool
	conn *acp.Conn
}

func (a *askingTool) launch(context.Context, state.Agent, func(string)) (*chat.Process, error) {
	toTool, fromChat := io.Pipe()
	toChat, fromTool := io.Pipe()
	exited := make(chan struct{})
	var once sync.Once
	s := &askingSession{tool: a}
	s.conn = acp.NewConn(toTool, fromTool, s)
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

func (s *askingSession) Notify(string, json.RawMessage) {}

func (s *askingSession) Request(method string, params json.RawMessage, reply func(any, error)) {
	switch method {
	case acp.MethodInitialize:
		reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}, nil)
	case acp.MethodSessionNew:
		reply(map[string]any{"sessionId": "s1"}, nil)
	case acp.MethodSessionPrompt:
		s.tool.mu.Lock()
		s.tool.prompts = append(s.tool.prompts, string(params))
		s.tool.mu.Unlock()
		if !strings.Contains(string(params), `"text":"Run the tests"`) {
			reply(map[string]any{"stopReason": "end_turn"}, nil)
			return
		}
		go func() {
			_ = s.conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": "s1", "update": json.RawMessage(
				`{"sessionUpdate":"tool_call","toolCallId":"t1","title":"Run the tests","kind":"execute","status":"pending","rawInput":{"command":["bash","-lc","npm test"]}}`)})
			var out acp.RequestPermissionResponse
			_ = s.conn.Call(context.Background(), acp.MethodRequestPermission, map[string]any{
				"sessionId": "s1",
				"toolCall":  map[string]any{"toolCallId": "t1"},
				"options": []map[string]string{
					{"optionId": "allow", "name": "Yes", "kind": "allow_once"},
					{"optionId": "always", "name": "Yes, and don't ask again for npm test", "kind": "allow_always"},
					{"optionId": "reject", "name": "No", "kind": "reject_once"},
				},
			}, &out)
			// It ends with the call done, saying what it did: a finish. A call
			// left open would be work it waits on, and nudged.
			_ = s.conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": "s1", "update": json.RawMessage(
				`{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed"}`)})
			_ = s.conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": "s1", "update": json.RawMessage(
				`{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"The tests pass, and the change is done."}}`)})
			reply(map[string]any{"stopReason": "end_turn"}, nil)
		}()
	default:
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
	}
}

// waitChat reads ref's chat until cond holds.
func waitChat(t *testing.T, client *api.Client, ref, what string, cond func(api.ChatThread) bool) api.ChatThread {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		th, err := client.Chat(context.Background(), ref)
		if err != nil {
			t.Fatal(err)
		}
		if cond(th) {
			return th
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func leadCards(th api.ChatThread) []api.ChatAgentPermission {
	var out []api.ChatAgentPermission
	for _, it := range th.Items {
		if it.AgentPermission != nil {
			out = append(out, *it.AgentPermission)
		}
	}
	return out
}

// An agent's permission request shows in the project's chat, and answering
// it there, on the agent's own route, answers the agent: both say so.
func TestAnsweringAnAgentsPermissionFromTheLead(t *testing.T) {
	t.Parallel()
	w := newWakeTest(t)
	tool := &askingTool{}
	w.srv.chat.Launch = tool.launch
	w.srv.chat.Prepare = nil
	// The agent's turn ends a finish, without asking a model whether it is
	// one.
	w.srv.askAside = func(context.Context, state.Agent, string, string) (string, string, error) { return "DONE", "", nil }
	ctx := context.Background()
	if err := (credentials.Store{Dir: w.paths.Credentials()}).SaveClaudeToken("default", "sk-ant-oat01-default"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.srv.manager(nil).EnsureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	// The agent finishing doesn't start a turn of the lead's, at whatever
	// moment: the lead's only turn is the one this test starts.
	if err := w.srv.store.SetProjectFinishNotices(ctx, "hello-stack", state.FinishNoticesOff); err != nil {
		t.Fatal(err)
	}
	ref := "hello-stack/agent-02"
	if _, err := w.client.SendChat(ctx, ref, "Run the tests"); err != nil {
		t.Fatal(err)
	}
	waitChat(t, w.client, ref, "the agent to ask", func(th api.ChatThread) bool { return th.Session.State == api.ChatWaiting })
	lead := waitChat(t, w.client, "hello-stack", "the card in the lead", func(th api.ChatThread) bool { return len(leadCards(th)) == 1 })
	card := leadCards(lead)[0]
	if card.Ref != ref || card.Title != "Settings page" || card.Permission.Outcome != "" || card.Tool == nil || card.Tool.Command != "npm test" {
		t.Fatalf("the card is %+v", card)
	}

	// The card's Approve always.
	if _, err := w.client.AnswerChat(ctx, card.Ref, card.Item, "always"); err != nil {
		t.Fatal(err)
	}
	waitChat(t, w.client, "hello-stack", "the card to say so", func(th api.ChatThread) bool {
		return leadCards(th)[0].Permission.Outcome == "always"
	})
	agent := waitChat(t, w.client, ref, "the agent's turn to end", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	for _, it := range agent.Items {
		if it.Permission != nil && it.Permission.Outcome != "always" {
			t.Errorf("the agent's request is %+v", it.Permission)
		}
	}
	// And it can't be answered twice, from either chat.
	if _, err := w.client.AnswerChat(ctx, card.Ref, card.Item, "reject"); err == nil {
		t.Error("the request was answered again")
	}

	// The lead's next prompt is told what the request was and how it was
	// answered.
	if _, err := w.client.SendChat(ctx, "hello-stack", "What did agent-02 want?"); err != nil {
		t.Fatal(err)
	}
	waitChat(t, w.client, "hello-stack", "the lead's turn", func(th api.ChatThread) bool {
		for _, it := range th.Items {
			if it.Kind == "user" && it.Result != nil {
				return th.Session.State == api.ChatReady
			}
		}
		return false
	})
	if !tool.got("Run the tests: npm test — the user allowed it") {
		t.Error("the lead wasn't told of the request")
	}
}
