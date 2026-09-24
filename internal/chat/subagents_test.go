package chat

import (
	"encoding/json"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// TestASubagentIsACardWithItsWorkNestedUnderIt drives a turn the way
// claude-agent-acp does when told the client shows subagents: the subagent is
// announced on the agent's session, works under a session of its own, and is
// said to have finished. Its words and tool calls land under its card, and the
// agent's own last word is still what LastMessage says the turn ended on.
func TestASubagentIsACardWithItsWorkNestedUnderIt(t *testing.T) {
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Let me look."}}`)
		f.update(s, `{"sessionUpdate":"subagent_spawned","subagentSessionId":"task-1","name":"Explore","task":"Find where limits are parsed","capabilities":{}}`)
		f.update("task-1", `{"sessionUpdate":"tool_call","toolCallId":"sub-tool","title":"grep rateLimit","kind":"search","status":"in_progress"}`)
		f.update("task-1", `{"sessionUpdate":"tool_call_update","toolCallId":"sub-tool","status":"completed"}`)
		f.update("task-1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"It is in usage.go:130."}}`)
		f.update(s, `{"sessionUpdate":"subagent_state_update","subagentSessionId":"task-1","state":"completed"}`)
		// An update for a session nobody announced is nothing to do with us.
		f.update("stranger", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"not ours"}}`)
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Found it: usage.go."}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "where are limits parsed?"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))

	var card api.ChatItem
	var nested, top []api.ChatItem
	for _, it := range th.Items {
		switch {
		case it.Kind == "subagent":
			card = it
		case it.Parent != "":
			nested = append(nested, it)
		default:
			top = append(top, it)
		}
	}
	if card.Subagent == nil || card.Subagent.Name != "Explore" || card.Subagent.Task != "Find where limits are parsed" || card.Subagent.State != "completed" {
		t.Fatalf("the subagent's card = %+v", card)
	}
	if len(nested) != 2 || nested[0].Kind != "tool" || nested[0].Tool.Status != "completed" || nested[1].Text != "It is in usage.go:130." {
		t.Errorf("under the card = %+v", nested)
	}
	for _, it := range nested {
		if it.Parent != card.ID || it.Turn != card.Turn {
			t.Errorf("%s item isn't under the card, in its turn: %+v", it.Kind, it)
		}
	}
	// The agent's text before and after the card are two messages, with the
	// card between them.
	if got := kinds(api.ChatThread{Items: top}); len(got) != 3 || got[1] != "assistant" || got[2] != "assistant" {
		t.Errorf("the conversation itself = %v", got)
	}
	for _, it := range th.Items {
		if it.Text == "not ours" {
			t.Error("an update for an unknown session reached the conversation")
		}
	}
	if got := m.LastMessage(testAgent); got != "Found it: usage.go." {
		t.Errorf("LastMessage = %q, want the agent's own last word, not its subagent's", got)
	}

	// The chat said it can show subagents.
	var init acp.InitializeRequest
	if err := json.Unmarshal(f.called(acp.MethodInitialize)[0], &init); err != nil {
		t.Fatal(err)
	}
	if init.ClientCapabilities.Subagents == nil {
		t.Error("initialize didn't advertise subagents")
	}
	// ...in the form claude-agent-acp 0.76.0 really reads: its SDK drops the
	// field, and passes _meta through.
	raw, _ := json.Marshal(init.ClientCapabilities.Meta)
	if string(raw) != `{"jetbrains":{"air":{"capabilities":["nativeSubagentSessions"],"version":1}}}` {
		t.Errorf("initialize's capability _meta = %s", raw)
	}
}

// TestASubagentStillRunningWhenItsSessionEndsIsStopped: an adapter that goes
// away takes its subagents with it, and their cards say so rather than spin.
func TestASubagentStillRunningWhenItsSessionEndsIsStopped(t *testing.T) {
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"subagent_spawned","subagentSessionId":"bg-1","name":"general-purpose","task":"run the suite"}`)
		f.update("bg-1", `{"sessionUpdate":"tool_call","toolCallId":"t","title":"go test ./...","kind":"execute","status":"in_progress"}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "test it in the background"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	// The subagent goes on after the turn: what it does is still recorded.
	f.update("bg-1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Still going."}}`)
	waitThread(t, m, testAgent, "the background subagent's words", func(th api.ChatThread) bool {
		for _, it := range th.Items {
			if it.Text == "Still going." && it.Parent != "" {
				return true
			}
		}
		return false
	})
	m.Stop(testAgent.Ref(), "the agent stopped")
	th := waitThread(t, m, testAgent, "the card to stop", func(th api.ChatThread) bool {
		for _, it := range th.Items {
			if it.Kind == "subagent" && it.Subagent.State == "stopped" {
				return true
			}
		}
		return false
	})
	for _, it := range th.Items {
		if it.Kind == "tool" && it.Tool.Status != "stopped" {
			t.Errorf("the subagent's unfinished tool call is %q, want stopped", it.Tool.Status)
		}
	}
}
