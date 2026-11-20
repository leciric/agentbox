package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/state"
)

// timings waits for n timing rows of kind, flushing the store's queue.
func timings(t *testing.T, store *state.Store, kind string, n int) []state.TimingRow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := store.FlushTiming(context.Background()); err != nil {
			t.Fatal(err)
		}
		rows, err := store.SlowestTimings(context.Background(), kind, state.TimingFilter{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) >= n {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d %s timing rows, want %d: %+v", len(rows), kind, n, rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTurnModelWaits: each time the model is asked — the prompt, the last
// result of the calls it made — the wait for what it streams first is one
// request's time to its first token, and the turn's first is kept apart.
// What it streams with nothing asked (a woken turn, a call the tool opens
// running) is no request.
func TestTurnModelWaits(t *testing.T) {
	t.Parallel()
	start := time.Now()
	at := func(s float64) time.Time { return start.Add(time.Duration(s * float64(time.Second))) }
	title := func(s string) *string { return &s }
	call := func(id string) acp.SessionUpdate {
		return acp.SessionUpdate{SessionUpdate: "tool_call", ToolCallID: id, Title: title("Bash"), Kind: "execute", Status: "pending"}
	}
	status := func(id, s string) acp.SessionUpdate {
		return acp.SessionUpdate{SessionUpdate: "tool_call_update", ToolCallID: id, Status: s}
	}
	input := func(id string) acp.SessionUpdate {
		return acp.SessionUpdate{SessionUpdate: "tool_call_update", ToolCallID: id, RawInput: json.RawMessage(`{"command":"go test"}`)}
	}
	for _, tc := range []struct {
		name        string
		run         func(tr *turn)
		calls       int
		first, ttft time.Duration
	}{{
		name: "a reply after a call",
		run: func(tr *turn) {
			tr.asked(at(0))
			tr.streamed(at(3))
			tr.toolUpdate(call("a"), at(4))
			tr.toolUpdate(status("a", "in_progress"), at(5))
			tr.toolUpdate(status("a", "completed"), at(40))
			tr.streamed(at(42))
			tr.streamed(at(43))
		},
		calls: 2, first: 3 * time.Second, ttft: 5 * time.Second,
	}, {
		name: "calls in parallel: the last result asks",
		run: func(tr *turn) {
			tr.asked(at(0))
			tr.toolUpdate(call("a"), at(3))
			tr.toolUpdate(call("b"), at(4))
			tr.toolUpdate(status("a", "completed"), at(10))
			tr.toolUpdate(status("b", "completed"), at(30))
			tr.streamed(at(31))
		},
		calls: 2, first: 3 * time.Second, ttft: 4 * time.Second,
	}, {
		name: "a permission between",
		run: func(tr *turn) {
			tr.asked(at(0))
			tr.streamed(at(2))
			tr.waits()
			tr.streamed(at(50)) // nothing asked it
		},
		calls: 1, first: 2 * time.Second, ttft: 2 * time.Second,
	}, {
		name: "a call that opens running",
		run: func(tr *turn) {
			tr.asked(at(0))
			running := call("a")
			running.Status = "in_progress"
			tr.toolUpdate(running, at(5))
			tr.toolUpdate(input("a"), at(6))
			tr.toolUpdate(status("a", "failed"), at(20))
			tr.streamed(at(22))
		},
		calls: 2, first: 5 * time.Second, ttft: 7 * time.Second,
	}, {
		name: "a woken turn",
		run: func(tr *turn) {
			tr.streamed(at(5))
			tr.streamed(at(8))
		},
	}} {
		tr := &turn{}
		tc.run(tr)
		if tr.modelCalls != tc.calls || tr.firstToken != tc.first || tr.ttft != tc.ttft {
			t.Errorf("%s: %d calls, first token %v, all %v; want %d, %v, %v", tc.name, tr.modelCalls, tr.firstToken, tr.ttft, tc.calls, tc.first, tc.ttft)
		}
	}
}

// TestBusy: calls in parallel count once, and one inside another not at all.
func TestBusy(t *testing.T) {
	t.Parallel()
	start := time.Now()
	s := func(from, to float64) span {
		return span{start.Add(time.Duration(from * float64(time.Second))), start.Add(time.Duration(to * float64(time.Second)))}
	}
	for _, tc := range []struct {
		spans []span
		want  time.Duration
	}{
		{nil, 0},
		{[]span{s(0, 2)}, 2 * time.Second},
		{[]span{s(5, 6), s(0, 2)}, 3 * time.Second},
		{[]span{s(0, 4), s(1, 2), s(3, 6)}, 6 * time.Second},
		{[]span{s(0, 1), s(1, 2)}, 2 * time.Second},
		{[]span{s(3, 3), s(4, 2)}, 0},
	} {
		if got := busy(tc.spans); got != tc.want {
			t.Errorf("busy(%v) = %v, want %v", tc.spans, got, tc.want)
		}
	}
}

// TestTurnStart: a turn's start is the wait for its prompt to reach the
// tool; one whose prompt never did waited all along, and a woken one had no
// prompt to wait for.
func TestTurnStart(t *testing.T) {
	t.Parallel()
	start := time.Now()
	end := start.Add(10 * time.Second)
	prompted := &turn{startedAt: start, promptAt: start.Add(2 * time.Second)}
	never := &turn{startedAt: start}
	woken := &turn{startedAt: start, woken: true}
	if got := turnTiming(prompted, end); got.Start != 2*time.Second || got.Wall != 10*time.Second {
		t.Errorf("prompted: start %v of %v", got.Start, got.Wall)
	}
	if got := turnTiming(never, end); got.Start != 10*time.Second {
		t.Errorf("never prompted: start %v", got.Start)
	}
	if got := turnTiming(woken, end); got.Start != 0 {
		t.Errorf("woken: start %v", got.Start)
	}
}

// TestATurnAndItsToolsAreTimed: a turn's row says how long it took, how
// long its model and its tools did, and each tool call has a row of its
// own, a subagent's too, under the subagent's name.
func TestATurnAndItsToolsAreTimed(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"Thinking"}}`)
		time.Sleep(50 * time.Millisecond)
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"go test ./...","kind":"execute","status":"pending","_meta":{"claudeCode":{"toolName":"Bash"}}}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"in_progress"}`)
		time.Sleep(300 * time.Millisecond) // the tool runs
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed"}`)
		f.update(s, `{"sessionUpdate":"subagent_spawned","subagentSessionId":"task-1","name":"Explore","task":"find it"}`)
		f.update("task-1", `{"sessionUpdate":"tool_call","toolCallId":"sub-1","title":"grep limit","kind":"search","status":"in_progress","_meta":{"claudeCode":{"toolName":"Grep"}}}`)
		time.Sleep(20 * time.Millisecond)
		f.update("task-1", `{"sessionUpdate":"tool_call_update","toolCallId":"sub-1","status":"failed"}`)
		f.update(s, `{"sessionUpdate":"subagent_state_update","subagentSessionId":"task-1","state":"completed"}`)
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Done"}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "run the tests"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))

	turns := timings(t, store, state.TimingTurn, 1)
	tr := turns[0]
	if tr.Project != testAgent.Project || tr.Agent != testAgent.Name || tr.Status != "completed" || tr.Turn == "" {
		t.Errorf("the turn's row = %+v", tr)
	}
	if tr.ToolCalls != 1 || tr.Tools < 300*time.Millisecond || tr.Tools >= tr.Wall {
		t.Errorf("tools: %d calls over %v of %v, want the main loop's one over its 300ms", tr.ToolCalls, tr.Tools, tr.Wall)
	}
	if tr.ModelCalls != 2 || tr.Model < 50*time.Millisecond || tr.Model >= 300*time.Millisecond {
		t.Errorf("model: %d calls over %v, want 2 over the ~50ms it thought, not the tool's 300", tr.ModelCalls, tr.Model)
	}
	if tr.Wall < tr.Model+tr.Tools || tr.TTFT < tr.FirstToken || tr.Start < 0 || tr.Start > tr.Wall {
		t.Errorf("the turn's row doesn't add up: %+v", tr)
	}

	tools := timings(t, store, state.TimingTool, 2)
	byName := map[string]state.TimingRow{}
	for _, r := range tools {
		byName[r.Name] = r
	}
	bash, grep := byName["Bash"], byName["Grep"]
	if bash.Sub != "" || bash.Status != "completed" || bash.Detail != "go test ./..." || bash.Wall < 300*time.Millisecond || bash.Turn != tr.Turn {
		t.Errorf("the main loop's call = %+v", bash)
	}
	if grep.Sub != "Explore" || grep.Status != "failed" || grep.Wall < 20*time.Millisecond || grep.Turn != tr.Turn {
		t.Errorf("the subagent's call = %+v", grep)
	}
	if len(tools) != 2 {
		t.Errorf("%d tool rows, want 2: %+v", len(tools), tools)
	}
}

// TestACallStillRunningAsItsTurnEndsIsStopped: a call the turn left running
// is timed to the turn's end, as stopped.
func TestACallStillRunningAsItsTurnEndsIsStopped(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"sleep 100","kind":"execute","status":"in_progress"}`)
		time.Sleep(30 * time.Millisecond)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "wait"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	tools := timings(t, store, state.TimingTool, 1)
	if r := tools[0]; r.Status != "stopped" || r.Name != "execute" || r.Wall < 30*time.Millisecond {
		t.Errorf("the call left running = %+v", r)
	}
	if tr := timings(t, store, state.TimingTurn, 1)[0]; tr.ToolCalls != 1 || tr.Tools < 30*time.Millisecond {
		t.Errorf("the turn's tools: %d over %v", tr.ToolCalls, tr.Tools)
	}
}
