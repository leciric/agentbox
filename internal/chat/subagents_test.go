package chat

import (
	"encoding/json"
	"strings"
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
	t.Parallel()
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
	if string(raw) != `{"jetbrains":{"air":{"capabilities":["nativeSubagentSessions","asyncTasks"],"version":1}}}` {
		t.Errorf("initialize's capability _meta = %s", raw)
	}
}

// TestASubagentStillRunningWhenItsSessionEndsIsStopped: an adapter that goes
// away takes its subagents with it, and their cards say so rather than spin.
func TestASubagentStillRunningWhenItsSessionEndsIsStopped(t *testing.T) {
	t.Parallel()
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

// TestASubagentSentToTheBackgroundIsNotAToolStillRunning drives the frames
// claude-agent-acp 0.85.1 sends, recorded live, when the Agent tool runs a
// subagent in the background: the tool call is never given a status of its
// own, only the async_launched marker of what it returned, and the subagent
// works under its own session. The call is over at the marker; read as
// pending, it showed as running for as long as the turn did, and the stall
// watch counted the machine's CPU as the turn's progress.
func TestASubagentSentToTheBackgroundIsNotAToolStillRunning(t *testing.T) {
	t.Parallel()
	for name, marker := range map[string]string{
		// What a client that declares _meta.jetbrains.air, as AgentBox does, is sent.
		"markers": `{"status":"async_launched","isAsync":true}`,
		// What 0.81.0 sent it, and every client that doesn't declare it still gets.
		"full": `{"isAsync":true,"status":"async_launched","agentId":"a3aed31754869dc54","description":"Sleep, then report","outputFile":"/t/tasks/a3aed31754869dc54.output"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			release := make(chan struct{})
			f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
				f.update(s, `{"sessionUpdate":"subagent_spawned","subagentSessionId":"a3aed31754869dc54","name":"Sleep, then report","task":"Sleep a minute","capabilities":{}}`)
				f.update(s, `{"_meta":{"claudeCode":{"toolResponse":`+marker+`,"toolName":"Agent"}},"toolCallId":"toolu_bg","sessionUpdate":"tool_call_update"}`)
				f.update("a3aed31754869dc54", `{"sessionUpdate":"tool_call","toolCallId":"sub-bash","title":"python3 -c 'import time; time.sleep(60)'","kind":"execute","status":"in_progress"}`)
				<-release
				f.update("a3aed31754869dc54", `{"sessionUpdate":"tool_call_update","toolCallId":"sub-bash","status":"completed"}`)
				f.update(s, `{"sessionUpdate":"subagent_state_update","subagentSessionId":"a3aed31754869dc54","state":"completed"}`)
				return acp.PromptResponse{StopReason: "end_turn"}
			})
			m, _ := newManager(t, openStore(t), f)
			if _, err := m.Send(testAgent, "sleep in the background, then report"); err != nil {
				t.Fatal(err)
			}
			th := waitThread(t, m, testAgent, "the launch", func(th api.ChatThread) bool {
				for _, it := range th.Items {
					if it.Kind == "tool" && it.Tool.CallID == "toolu_bg" && it.Tool.Name == "Agent" {
						return true
					}
				}
				return false
			})
			for _, it := range th.Items {
				if it.Kind == "tool" && it.Tool.CallID == "toolu_bg" && it.Tool.Status != "completed" {
					t.Errorf("the launched Agent call is %q, want completed", it.Tool.Status)
				}
			}
			// The subagent's own tool call is still running, under its card, and
			// isn't the turn's: the turn has no tool call of its own running.
			if p, ok := m.Progress(testAgent.Ref()); !ok || p.ToolRunning {
				t.Errorf("progress = %+v, %t; want a running turn with no tool call running", p, ok)
			}
			close(release)
			th = waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
			for _, it := range th.Items {
				if it.Kind == "tool" && it.Tool.CallID == "toolu_bg" && it.Tool.Status != "completed" {
					t.Errorf("after the turn, the launched Agent call is %q, want completed", it.Tool.Status)
				}
			}
		})
	}
}

// TestASubagentInTheForegroundEndsItsAgentCall is the same for an Agent call
// that waits for its subagent, recorded live from claude-agent-acp 0.85.1:
// running while the subagent works, and completed by the status of what it
// returned, rather than stopped when its turn ended.
func TestASubagentInTheForegroundEndsItsAgentCall(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"subagent_spawned","subagentSessionId":"abf69d567cb7728d8","name":"Read notes.txt","task":"cat notes.txt","capabilities":{}}`)
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"toolu_fg","status":"pending","_meta":{"claudeCode":{"toolName":"Agent"}}}`)
		<-release
		f.update("abf69d567cb7728d8", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"It says two."}}`)
		f.update(s, `{"sessionUpdate":"subagent_state_update","subagentSessionId":"abf69d567cb7728d8","state":"completed"}`)
		f.update(s, `{"_meta":{"claudeCode":{"toolResponse":{"status":"completed"},"toolName":"Agent"}},"toolCallId":"toolu_fg","sessionUpdate":"tool_call_update"}`)
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"The file says two."}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "what does notes.txt say?"); err != nil {
		t.Fatal(err)
	}
	waitProgress(t, m, "the Agent call", func(p Progress, ok bool) bool { return ok && p.ToolRunning })
	close(release)
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	for _, it := range th.Items {
		if it.Kind == "tool" && it.Tool.CallID == "toolu_fg" && it.Tool.Status != "completed" {
			t.Errorf("the Agent call is %q after its turn, want completed", it.Tool.Status)
		}
	}
}

// TestATurnTheAdapterFailsForAnUnfinishedToolEnds follows claude-agent-acp
// 0.85.1 when Claude ends a turn with a tool call it never answered: the
// adapter fails the tool call, then the prompt, where it used to report the
// turn completed. The turn ends failed, saying why, and the chat is ready for
// the next one.
func TestATurnTheAdapterFailsForAnUnfinishedToolEnds(t *testing.T) {
	t.Parallel()
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"unfinished-bash","title":"go test ./...","kind":"execute","status":"pending","_meta":{"claudeCode":{"toolName":"Bash"}}}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"unfinished-bash","status":"failed","content":[{"type":"content","content":{"type":"text","text":"Claude ended the turn without returning a result for this tool."}}]}`)
		return acp.PromptResponse{}
	})
	f.turnErr = &acp.Error{Code: acp.CodeInternalError,
		Message: "Internal error: Claude ended the turn without returning results for tool calls: unfinished-bash",
		Data:    json.RawMessage(`{"errorKind":"incomplete_tool_call"}`)}
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "run the tests"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	var failed, said bool
	for _, it := range th.Items {
		switch {
		case it.Kind == "user" && it.Result.State != "failed":
			t.Errorf("the turn ended %s, want failed", it.Result.State)
		case it.Kind == "tool":
			failed = it.Tool.Status == "failed" && strings.Contains(it.Tool.Output, "without returning a result")
		case it.Kind == "error":
			said = strings.Contains(it.Text, "without returning results for tool calls")
		}
	}
	if !failed || !said {
		t.Errorf("the tool call failed: %t, and the conversation says why: %t: %s", failed, said, summary(th))
	}
	if th.Session.State != api.ChatReady {
		t.Errorf("after the failed turn the chat is %q, want ready", th.Session.State)
	}
}
