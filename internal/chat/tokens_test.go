package chat

import (
	"context"
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
	// A cost with no tokens is one row under the session's model.
	rows := tokenRows(base, nil, "opus", 0.2)
	if len(rows) != 1 || rows[0].Model != "opus" || !near(rows[0].CostUSD, 0.2) {
		t.Errorf("tokenRows(no tokens) = %+v", rows)
	}
	// Nothing at all is no row.
	if rows := tokenRows(base, nil, "opus", 0); rows != nil {
		t.Errorf("tokenRows(nothing) = %+v", rows)
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
