package chat

import (
	"strings"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// idleCompaction is what claude-agent-acp 0.101 sends when it compacts an
// idle session by itself, outside any session/prompt, as read off the wire:
// its tool call opening and completing, its facts, and the usage_update of
// the /compact's result, which carries no cost.
func idleCompaction(f *fakeTool, s string) {
	f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"compact-1","title":"Compact conversation","kind":"think","status":"in_progress","_meta":{"jetbrains":{"air":{"version":1,"contextCompaction":{"version":1}}}}}`)
	f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"compact-1","status":"completed"}`)
	f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"compact-1","_meta":{"jetbrains":{"air":{"contextCompaction":{"version":1,"trigger":"idle","preTokens":237664,"postTokens":3853,"durationMs":4879}}}}}`)
	f.update(s, `{"sessionUpdate":"usage_update","used":3853,"size":1000000}`)
}

func chattyTurn(f *fakeTool, s, text string) acp.PromptResponse {
	f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ok: `+strings.Fields(text)[0]+`"}}`)
	f.update(s, `{"sessionUpdate":"usage_update","used":1000,"size":1000000,"cost":{"amount":0.01,"currency":"USD"}}`)
	return acp.PromptResponse{StopReason: "end_turn"}
}

// The hang of 6 Nov: the adapter compacted an idle session by itself, its
// tool call woke a turn that nothing ended, and the message sent next was
// steered, refused ("no running turn") and held behind it. The compaction is
// a card now, and the chat stays ready for the message, which gets its turn.
func TestTheToolsOwnIdleCompactionStartsNoTurn(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(chattyTurn)
	f.steering, f.steerIdle = true, true // what the real adapter answers with its session idle
	m, _ := newManager(t, store, f)
	fin := finishes{}
	m.Finished = fin.add

	if _, err := m.Send(testAgent, "first"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn to end", turnsEnded(1))

	idleCompaction(f, "session-1")
	th := waitThread(t, m, testAgent, "the compaction's card", func(th api.ChatThread) bool {
		for _, it := range th.Items {
			if it.Kind == "tool" && isCompaction(it.Tool) && it.Tool.Status == "completed" {
				return true
			}
		}
		return false
	})
	if th.Session.State != api.ChatReady {
		t.Fatalf("after the compaction the chat is %s, want ready: %s", th.Session.State, summary(th))
	}
	users := 0
	for _, it := range th.Items {
		if it.Kind == "user" {
			users++
			if it.Woken {
				t.Fatalf("the compaction woke a turn: %s", summary(th))
			}
		}
		if it.Kind == "tool" && isCompaction(it.Tool) && it.Turn != find(th, "user", 0).ID {
			t.Errorf("the card is under turn %q, want the last one's", it.Turn)
		}
	}
	if users != 1 {
		t.Fatalf("%d turns after the compaction, want 1: %s", users, summary(th))
	}

	item, err := m.Send(testAgent, "second message")
	if err != nil {
		t.Fatal(err)
	}
	if item.Kind != "user" {
		t.Fatalf("the message became a %s, want a turn of its own", item.Kind)
	}
	th = waitThread(t, m, testAgent, "the message's answer", turnsEnded(2))
	if find(th, "assistant", 1).Text != "ok: second" {
		t.Fatalf("no answer to the message: %s", summary(th))
	}
	waitFor(t, "both turns reported", func() bool { return len(fin.all()) == 2 })
}

// A turn that compacts and then goes silent — the adapter's prompt never
// answered, no tool or subagent running — is stopped and carried on by
// itself, and a message that waited on it goes to the turn that carries on.
func TestATurnSilentAfterCompactingIsStoppedAndCarriedOn(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	prompts := make(chan string, 4)
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		prompts <- text
		if text == "run the load test" {
			f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Running the load test."}}`)
			f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"compact-1","title":"Compact conversation","kind":"think","status":"in_progress"}`)
			f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"compact-1","status":"completed"}`)
			f.update(s, `{"sessionUpdate":"usage_update","used":3853,"size":1000000}`)
			<-f.cancels // wedged: nothing more until it is stopped
			return acp.PromptResponse{StopReason: "cancelled"}
		}
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Carrying on with the load test."}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	m.compactStuck = 300 * time.Millisecond

	if _, err := m.Send(testAgent, "run the load test"); err != nil {
		t.Fatal(err)
	}
	<-prompts
	waitThread(t, m, testAgent, "the compaction", func(th api.ChatThread) bool {
		for _, it := range th.Items {
			if it.Kind == "tool" && isCompaction(it.Tool) && it.Tool.Status == "completed" {
				return true
			}
		}
		return false
	})
	// Written while it is stuck: no steering here, so it waits on the turn.
	if _, err := m.Send(testAgent, "how is it going?"); err != nil {
		t.Fatal(err)
	}

	select {
	case p := <-prompts:
		if !strings.Contains(p, carryOnPrompt) {
			t.Fatalf("the turn after the stuck one was prompted %q", p)
		}
	case <-time.After(5 * time.Second):
		th, _ := m.Thread(testAgent)
		t.Fatalf("the stuck turn wasn't carried on: %s", summary(th))
	}
	th := waitThread(t, m, testAgent, "the carried-on turn and the message's to end", func(th api.ChatThread) bool {
		return th.Session.State == api.ChatReady && turnsEnded(3)(th)
	})
	stuck := find(th, "user", 0)
	if stuck.Result == nil || stuck.Result.State != "cancelled" {
		t.Errorf("the stuck turn ended %+v, want cancelled", stuck.Result)
	}
	var carried, msg *api.ChatItem
	var noticed bool
	for i, it := range th.Items {
		switch {
		case it.Kind == "user" && it.Text == carryOnPrompt:
			carried = &th.Items[i]
		case it.Kind == "user" && strings.Contains(it.Text, "how is it going?"):
			msg = &th.Items[i]
		case it.Kind == "notice" && it.Text == carryOnNotice:
			noticed = true
		}
	}
	if carried == nil || !carried.Hidden || carried.Result == nil || carried.Result.State != "completed" {
		t.Errorf("the carry-on turn is %+v, want a hidden one that completed", carried)
	}
	if !noticed {
		t.Errorf("the chat doesn't say why the turn was stopped: %s", summary(th))
	}
	// The message that waited on the stuck turn still gets its own, after.
	if msg == nil || msg.Result == nil || msg.Result.State != "completed" {
		t.Errorf("the message that waited went %+v, want a turn of its own", msg)
	}
}

// A turn that goes on after its compaction is left alone.
func TestATurnThatGetsOnAfterCompactingIsLeftAlone(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"compact-1","title":"Compact conversation","kind":"think","status":"in_progress"}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"compact-1","status":"completed"}`)
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"bash-1","title":"sleep 1","kind":"execute","status":"in_progress"}`)
		time.Sleep(600 * time.Millisecond) // a tool running, past the limit
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"bash-1","status":"completed"}`)
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done"}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	m.compactStuck = 200 * time.Millisecond
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	time.Sleep(400 * time.Millisecond)
	th, _ := m.Thread(testAgent)
	if u := find(th, "user", 0); u.Result == nil || u.Result.State != "completed" {
		t.Fatalf("the turn ended %+v, want completed", u.Result)
	}
	for _, it := range th.Items {
		if it.Kind == "user" && it.Hidden {
			t.Fatalf("a turn that got on was carried on: %s", summary(th))
		}
	}
}
