package chat

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// ledger waits for the token ledger to hold n rows, newest first. Rows are
// written off the conversation's lock, so a turn that has ended may not be in
// it for a moment.
func ledger(t *testing.T, store *state.Store, n int) []state.TokenRow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, err := store.TokenRows(context.Background(), state.TokenFilter{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) >= n {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ledger has %d rows, want %d: %+v", len(rows), n, rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// quotaTurn answers a turn the way claude-agent-acp does: the running cost in
// a usage_update, then the turn's tokens split by model in _meta.quota — here
// a main loop on Sonnet and a subagent on Haiku.
func quotaTurn(cost float64) func(f *fakeTool, sessionID, text string) acp.PromptResponse {
	return func(f *fakeTool, sessionID, _ string) acp.PromptResponse {
		f.update(sessionID, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Done"}}`)
		f.update(sessionID, `{"sessionUpdate":"usage_update","used":120000,"size":1000000,"cost":{"amount":`+formatFloat(cost)+`,"currency":"USD"}}`)
		return acp.PromptResponse{
			StopReason: "end_turn",
			// The main loop alone, as the adapter's usage field has it: the
			// subagent's tokens are only in the quota.
			Usage: &acp.Usage{InputTokens: 10, OutputTokens: 500, CachedReadTokens: 90_000, CachedWriteTokens: 2_000, TotalTokens: 92_510},
			Meta: &acp.PromptMeta{Quota: &acp.Quota{ModelUsage: []acp.ModelUsage{
				{Model: "claude-haiku-4-5", TokenCount: acp.TokenCount{InputTokens: 5, OutputTokens: 100, CachedInputTokens: 20_000, CachedWriteTokens: 1_000, TotalTokens: 21_105}},
				{Model: "claude-sonnet-5", TokenCount: acp.TokenCount{InputTokens: 10, OutputTokens: 500, CachedInputTokens: 90_000, CachedWriteTokens: 2_000, TotalTokens: 92_510}},
			}}},
		}
	}
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// TestATurnIsBookedInTheLedger: a turn's tokens come from the quota's per-model
// split, which counts the subagent the usage field leaves out, and its cost is
// how far the adapter's running total moved.
func TestATurnIsBookedInTheLedger(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(quotaTurn(0.50))
	m, _ := newManager(t, store, f)

	user, err := m.Send(testAgent, "build it")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	rows := ledger(t, store, 2)
	// Newest first, and a turn's rows share a time, so find them by model.
	byModel := map[string]state.TokenRow{}
	for _, r := range rows {
		byModel[r.Model] = r
	}
	sonnet, haiku := byModel["claude-sonnet-5"], byModel["claude-haiku-4-5"]
	if sonnet.Kind != state.TokensTurn || sonnet.Turn != user.ID || sonnet.Session != "session-1" || sonnet.AI != "claude" {
		t.Errorf("the turn's row = %+v", sonnet)
	}
	if sonnet.Input != 10 || sonnet.Output != 500 || sonnet.CacheRead != 90_000 || sonnet.CacheWrite != 2_000 {
		t.Errorf("sonnet spent %+v", sonnet)
	}
	if haiku.CacheRead != 20_000 || haiku.Turn != user.ID {
		t.Errorf("the subagent's model wasn't booked with its turn: %+v", haiku)
	}
	// The whole cost rides on the busiest model, so it adds up by agent.
	if !near(sonnet.CostUSD, 0.50) || haiku.CostUSD != 0 {
		t.Errorf("costs = sonnet %v, haiku %v; want the turn's 0.50 on sonnet alone", sonnet.CostUSD, haiku.CostUSD)
	}
	if sonnet.Context != 120_000 {
		t.Errorf("context = %d, want what the last usage_update said", sonnet.Context)
	}

	// The running total went from 0.50 to 0.80: the second turn cost 0.30.
	f.setTurn(quotaTurn(0.80))
	if _, err := m.Send(testAgent, "and the tests"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the second turn to end", turnsEnded(2))
	rows = ledger(t, store, 4)
	total := 0.0
	for _, r := range rows {
		total += r.CostUSD
	}
	if !near(total, 0.80) {
		t.Errorf("the ledger's costs add up to %v, want the running total 0.80", total)
	}
}

// TestATurnIsTimedOnItsMainLoop: what the model streamed is timed, the tool
// it ran isn't, and the timing goes on the main loop's model alone, with its
// own output rather than its subagent's.
func TestATurnIsTimedOnItsMainLoop(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	quota := quotaTurn(0.50)
	f := newFakeTool(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		f.update(sessionID, `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"Thinking"}}`)
		time.Sleep(100 * time.Millisecond)
		f.update(sessionID, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"Bash","kind":"execute","status":"pending"}`)
		f.update(sessionID, `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"in_progress"}`)
		time.Sleep(time.Second) // the tool runs
		f.update(sessionID, `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed"}`)
		return quota(f, sessionID, text)
	})
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "build it"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	byModel := map[string]state.TokenRow{}
	for _, r := range ledger(t, store, 2) {
		byModel[r.Model] = r
	}
	sonnet, haiku := byModel["claude-sonnet-5"], byModel["claude-haiku-4-5"]
	if sonnet.TimedOutput != 500 || sonnet.TimedMS < 100 || sonnet.TimedMS >= 1_000 {
		t.Errorf("the main loop was timed %d over %dms, want its 500 over the ~100ms it streamed for, not the second its tool ran", sonnet.TimedOutput, sonnet.TimedMS)
	}
	if haiku.TimedOutput != 0 || haiku.TimedMS != 0 {
		t.Errorf("the subagent's model was timed: %+v", haiku)
	}
}

// TestWorkBetweenTurnsIsBooked: a session that wakes by itself — a background
// task finished — reports a cost with no turn running, and that goes in the
// ledger at once, under its own kind.
func TestWorkBetweenTurnsIsBooked(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(quotaTurn(0.50))
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "start the build in the background"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	ledger(t, store, 2)

	f.update("session-1", `{"sessionUpdate":"usage_update","used":150000,"size":1000000,"cost":{"amount":0.75,"currency":"USD"}}`)
	rows := ledger(t, store, 3)
	background := rows[0]
	if background.Kind != state.TokensBackground || !near(background.CostUSD, 0.25) || background.Total() != 0 {
		t.Errorf("the background result was booked as %+v, want 0.25 and no tokens", background)
	}
	if background.Model != "sonnet" || background.Context != 150_000 {
		t.Errorf("the background row = %+v, want the session's model and its context", background)
	}
}

func TestTokenRows(t *testing.T) {
	t.Parallel()
	base := state.TokenRow{Project: "p", Agent: "a", Kind: state.TokensTurn}
	// A cost with no tokens is one row under the session's model, carrying
	// the generation time too.
	rows := tokenRows(base, nil, "opus", 0.2, 4_000, timing{})
	if len(rows) != 1 || rows[0].Model != "opus" || !near(rows[0].CostUSD, 0.2) || rows[0].GenerationMS != 4_000 {
		t.Errorf("tokenRows(no tokens) = %+v", rows)
	}
	// Nothing at all is no row.
	if rows := tokenRows(base, nil, "opus", 0, 0, timing{}); rows != nil {
		t.Errorf("tokenRows(nothing) = %+v", rows)
	}
}

// TestTokenRowsTimeTheMainLoopsModel: the main loop's timing goes on the row
// of the model the session ran on, not on the busiest one, which can be a
// subagent's; and with no model to match by name, on the busiest that output
// as much as the main loop did.
func TestTokenRowsTimeTheMainLoopsModel(t *testing.T) {
	t.Parallel()
	base := state.TokenRow{Project: "p", Agent: "a", Kind: state.TokensTurn}
	spent := map[string]acp.Spent{
		"claude-haiku-4-5": {Output: 4_000, CacheRead: 900_000}, // subagents, the busiest
		"claude-opus-5-5":  {Output: 1_200, CacheRead: 100_000},
	}
	timed := timing{output: 1_000, ms: 20_000}
	timedRow := func(rows []state.TokenRow) string {
		got := ""
		for _, r := range rows {
			if r.TimedOutput != 0 || r.TimedMS != 0 {
				if got != "" {
					t.Errorf("more than one row is timed: %+v", rows)
				}
				got = r.Model
				if r.TimedOutput != 1_000 || r.TimedMS != 20_000 {
					t.Errorf("%s is timed %d over %dms, want 1000 over 20000", r.Model, r.TimedOutput, r.TimedMS)
				}
			}
		}
		return got
	}
	rows := tokenRows(base, spent, "opus", 0.4, 60_000, timed)
	if got := timedRow(rows); got != "claude-opus-5-5" {
		t.Errorf("session on opus: timed row = %q, want claude-opus-5-5", got)
	}
	if rows[0].Model != "claude-haiku-4-5" || !near(rows[0].CostUSD, 0.4) {
		t.Errorf("the cost moved off the busiest row: %+v", rows)
	}
	spent["claude-haiku-4-5"] = acp.Spent{Output: 500, CacheRead: 900_000}
	if got := timedRow(tokenRows(base, spent, "default", 0.4, 60_000, timed)); got != "claude-opus-5-5" {
		t.Errorf("session on default: timed row = %q, want claude-opus-5-5, the busiest that output 1000", got)
	}
	if got := timedRow(tokenRows(base, spent, "opus", 0.4, 60_000, timing{output: 1_000})); got != "" {
		t.Errorf("an untimed turn timed %q", got)
	}
}

// TestTurnGenerated: a turn counts only the time its model was generating —
// from when it was asked, or last streamed, to what it streams next — and
// not the time its tools ran or its permissions waited, whatever it streamed
// around them.
func TestTurnGenerated(t *testing.T) {
	t.Parallel()
	start := time.Now()
	at := func(s float64) time.Time { return start.Add(time.Duration(s * float64(time.Second))) }
	title := func(s string) *string { return &s }
	call := func(id string) acp.SessionUpdate {
		return acp.SessionUpdate{SessionUpdate: "tool_call", ToolCallID: id, Title: title("Bash"), Kind: "execute", Status: "pending"}
	}
	input := func(id string) acp.SessionUpdate {
		return acp.SessionUpdate{SessionUpdate: "tool_call_update", ToolCallID: id, RawInput: json.RawMessage(`{"command":"go test"}`)}
	}
	status := func(id, s string) acp.SessionUpdate {
		return acp.SessionUpdate{SessionUpdate: "tool_call_update", ToolCallID: id, Status: s}
	}
	// A progress beat after the first carries neither status nor input.
	beat := acp.SessionUpdate{SessionUpdate: "tool_call_update", ToolCallID: "a", Content: json.RawMessage(`[{"type":"content","content":{"type":"text","text":"ok"}}]`)}

	cases := []struct {
		name string
		run  func(tr *turn)
		want time.Duration
	}{{
		// Ten seconds thinking, two writing a call, a 27-second tool, and a
		// short reply in two chunks: the old first-to-last-text measure saw
		// 50ms of it.
		name: "a short reply after a long call",
		run: func(tr *turn) {
			tr.asked(at(0))
			tr.streamed(at(3)) // the first thought
			tr.streamed(at(10))
			tr.toolUpdate(call("a"), at(10.5))
			tr.toolUpdate(input("a"), at(12))
			tr.toolUpdate(status("a", "in_progress"), at(13))
			tr.toolUpdate(beat, at(25))
			tr.toolUpdate(status("a", "completed"), at(40))
			tr.streamed(at(42))
			tr.streamed(at(42.05))
		},
		want: 14050 * time.Millisecond,
	}, {
		// Text early and late, a minute of permission and tool between.
		name: "a permission and a tool between two replies",
		run: func(tr *turn) {
			tr.asked(at(0))
			tr.streamed(at(2))
			tr.toolUpdate(call("a"), at(4))
			tr.waits() // the permission request
			tr.toolUpdate(status("a", "in_progress"), at(50))
			tr.toolUpdate(status("a", "completed"), at(65))
			tr.streamed(at(66))
			tr.streamed(at(70))
		},
		want: 9 * time.Second,
	}, {
		// The last result asks the model again; earlier ones are overtaken.
		name: "calls in parallel",
		run: func(tr *turn) {
			tr.asked(at(0))
			tr.toolUpdate(call("a"), at(3))
			tr.toolUpdate(call("b"), at(4))
			tr.toolUpdate(status("a", "completed"), at(10))
			tr.toolUpdate(status("b", "completed"), at(30))
			tr.streamed(at(31))
		},
		want: 5 * time.Second,
	}, {
		// codex-acp opens a call already running.
		name: "a call that opens running",
		run: func(tr *turn) {
			tr.asked(at(0))
			tr.streamed(at(4))
			running := call("a")
			running.Status = "in_progress"
			tr.toolUpdate(running, at(5))
			tr.toolUpdate(input("a"), at(6)) // the tool's, not the model's: it runs
			tr.toolUpdate(status("a", "failed"), at(20))
			tr.streamed(at(22))
		},
		want: 7 * time.Second,
	}, {
		// A session that woke by itself was never asked: it starts counting
		// at what it first streams.
		name: "a woken turn",
		run: func(tr *turn) {
			tr.streamed(at(5))
			tr.streamed(at(8))
		},
		want: 3 * time.Second,
	}}
	for _, tc := range cases {
		tr := &turn{}
		tc.run(tr)
		if tr.generated != tc.want {
			t.Errorf("%s: generated %v, want %v", tc.name, tr.generated, tc.want)
		}
	}
}

// TestGenerationMS: a turn that streamed more than one chunk is timed from
// its first to its last, and one that streamed nothing (or only one, at the
// same instant) falls back to its own start.
func TestGenerationMS(t *testing.T) {
	now := time.Now()
	streamed := &turn{startedAt: now, firstOutputAt: now, lastOutputAt: now.Add(3 * time.Second)}
	if got := generationMS(streamed); got != 3_000 {
		t.Errorf("streamed turn = %dms, want 3000", got)
	}
	silent := &turn{startedAt: now.Add(-2 * time.Second)}
	if got := generationMS(silent); got < 1_900 || got > 2_500 {
		t.Errorf("silent turn = %dms, want ~2000 (its own start to now)", got)
	}
	if got := generationMS(&turn{}); got != 0 {
		t.Errorf("a turn timed by nothing = %dms, want 0", got)
	}
}

func TestSpendTake(t *testing.T) {
	t.Parallel()
	var s spend
	s.observe(&acp.Cost{Amount: 1})
	if got := s.take(); !near(got, 1) {
		t.Errorf("first take = %v", got)
	}
	if got := s.take(); got != 0 {
		t.Errorf("a second take with nothing new = %v", got)
	}
	s.observe(nil) // an update that carries no cost changes nothing
	s.observe(&acp.Cost{Amount: 1.5})
	if got := s.take(); !near(got, 0.5) {
		t.Errorf("take after the total moved = %v", got)
	}
	// The total going down is the tool counting again from nothing (a /clear):
	// all of the new total is new.
	s.observe(&acp.Cost{Amount: 0.2})
	if got := s.take(); !near(got, 0.2) {
		t.Errorf("take after a reset = %v", got)
	}
}

// TestLimitsReachTheDaemon: a usage_update that carries the account's limits
// is handed on, parsed, with the agent it came from.
func TestLimitsReachTheDaemon(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, sessionID, _ string) acp.PromptResponse {
		f.update(sessionID, `{"sessionUpdate":"usage_update","used":20589,"size":200000,"_meta":{"_claude/rateLimit":{"status":"allowed_warning","unifiedWindows":{"five_hour":{"utilization":0.91,"resetsAt":1790092800}}}}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	got := make(chan struct {
		agent   string
		reading acp.RateLimit
	}, 1)
	m.Limits = func(a state.Agent, reading acp.RateLimit) {
		got <- struct {
			agent   string
			reading acp.RateLimit
		}{a.Ref(), reading}
	}
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	select {
	case g := <-got:
		if g.agent != testAgent.Ref() || g.reading.Status != "allowed_warning" || g.reading.UnifiedWindows["five_hour"].Utilization != 0.91 {
			t.Errorf("limits handed on = %+v", g)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reading never reached Limits")
	}
}

// TestLimitsStayWithTheSessionsAccount: an agent moved to another Claude Code
// account between turns — a project's chat follows its project's — goes on
// running on the token its session started with until the session restarts,
// so what that session reports is that account's, not the new one's. Once it
// restarts it is the new account's, and the conversation is still there.
func TestLimitsStayWithTheSessionsAccount(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, sessionID, _ string) acp.PromptResponse {
		f.update(sessionID, `{"sessionUpdate":"usage_update","used":20589,"size":200000,"_meta":{"_claude/rateLimit":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.1,"resetsAt":1790092800}}}}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	got := make(chan string, 1)
	m.Limits = func(a state.Agent, _ acp.RateLimit) { got <- a.ClaudeAccount }
	turn := func(a state.Agent, text, want string) {
		t.Helper()
		if _, err := m.Send(a, text); err != nil {
			t.Fatal(err)
		}
		select {
		case account := <-got:
			if account != want {
				t.Errorf("after %q, the limits went to %q, want %q", text, account, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the reading after %q never reached Limits", text)
		}
		waitThread(t, m, a, "the turn to end", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	}

	before, after := testAgent, testAgent
	before.ClaudeAccount, after.ClaudeAccount = "default", "personal"
	turn(before, "first", "default")
	turn(after, "second", "default")
	m.Stop(after.Ref(), "")
	turn(after, "third", "personal")

	th, err := m.Thread(after)
	if err != nil {
		t.Fatal(err)
	}
	var users int
	for _, it := range th.Items {
		if it.Kind == "user" {
			users++
		}
	}
	if users != 3 {
		t.Errorf("the thread has %d messages of yours after the restart, want all 3", users)
	}
}
