package state_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/state"
)

// TestTokenLedger: rows go in a turn at a time, add up by agent and model,
// list newest first, and sum into buckets — for every project, one project or
// one agent, over any stretch of time. Removing an agent leaves its rows.
func TestTokenLedger(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	base := time.Date(2026, 9, 21, 18, 0, 0, 0, time.UTC)
	row := func(project, agent, turn, model string, minutes int, cacheRead int64, cost float64, context int64) state.TokenRow {
		return state.TokenRow{Project: project, Agent: agent, AI: "claude", Session: "s", Turn: turn, Kind: state.TokensTurn,
			Model: model, At: base.Add(time.Duration(minutes) * time.Minute), Input: 10, Output: 100, CacheRead: cacheRead,
			CacheWrite: 1_000, CostUSD: cost, Context: context}
	}
	for _, turn := range [][]state.TokenRow{
		{row("acme", "agent-24", "t1", "claude-sonnet-5", 0, 400_000, 1.5, 450_000), row("acme", "agent-24", "t1", "claude-haiku-4-5", 0, 20_000, 0, 450_000)},
		{row("acme", "agent-24", "t2", "claude-sonnet-5", 30, 900_000, 3, 950_000)},
		{row("acme", "lead", "t3", "claude-opus-5", 45, 300_000, 2, 310_000)},
		{row("agentbox", "agent-96", "t4", "claude-sonnet-5", 90, 100_000, 0.5, 120_000)},
	} {
		if err := st.AddTokenRows(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}

	totals, err := st.TokenTotals(ctx, state.TokenFilter{Project: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(totals) != 3 {
		t.Fatalf("acme's totals = %+v, want agent-24 on two models and the lead on one", totals)
	}
	var sonnet state.TokenTotal
	for _, tt := range totals {
		if tt.Agent == "agent-24" && tt.Model == "claude-sonnet-5" {
			sonnet = tt
		}
	}
	if sonnet.Turns != 2 || sonnet.CacheRead != 1_300_000 || sonnet.CostUSD != 4.5 || sonnet.MaxContext != 950_000 {
		t.Errorf("agent-24 on sonnet = %+v", sonnet)
	}
	if !sonnet.LastAt.Equal(base.Add(30 * time.Minute)) {
		t.Errorf("last at = %v", sonnet.LastAt)
	}

	// One agent, and a stretch of time.
	one, err := st.TokenTotals(ctx, state.TokenFilter{Project: "acme", Agent: "agent-24", Since: base.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].CacheRead != 900_000 {
		t.Errorf("agent-24 since 18:10 = %+v, want only its second turn", one)
	}

	rows, err := st.TokenRows(ctx, state.TokenFilter{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Agent != "agent-96" || rows[1].Agent != "lead" {
		t.Errorf("newest two rows = %+v", rows)
	}
	if rows[0].Total() != 10+100+100_000+1_000 {
		t.Errorf("a row's total = %d", rows[0].Total())
	}

	buckets, err := st.TokenBuckets(ctx, state.TokenFilter{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 || !buckets[0].Start.Equal(base) || buckets[0].CacheRead != 1_620_000 || buckets[1].CacheRead != 100_000 {
		t.Errorf("hourly buckets = %+v", buckets)
	}

	// The ledger outlives the agent it describes.
	if err := st.RemoveAgent(ctx, "acme", "agent-24"); err != nil {
		t.Fatal(err)
	}
	after, err := st.TokenTotals(ctx, state.TokenFilter{Project: "acme", Agent: "agent-24"})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Errorf("removing the agent took its ledger with it: %+v", after)
	}
}

func TestClaudeCompactWindow(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if got, err := st.ClaudeCompactWindow(ctx); err != nil || got != state.DefaultClaudeCompactWindow {
		t.Errorf("unset = %d, %v; want the default", got, err)
	}
	for raw, want := range map[string]int64{"0": 0, "500000": 500_000, "nonsense": state.DefaultClaudeCompactWindow, "-5": state.DefaultClaudeCompactWindow} {
		if err := st.SetSetting(ctx, state.SettingClaudeCompactWindow, raw); err != nil {
			t.Fatal(err)
		}
		if got, err := st.ClaudeCompactWindow(ctx); err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", raw, got, err, want)
		}
	}
	for n, ok := range map[int64]bool{0: true, 100_000: true, 1_000_000: true, 99_999: false, 1_000_001: false} {
		if err := state.ValidClaudeCompactWindow(n); (err == nil) != ok {
			t.Errorf("ValidClaudeCompactWindow(%d) = %v", n, err)
		}
	}
}

// TestClaudeLimits keeps the newest reading per account: a later one replaces
// it, and one that arrives late with an older time doesn't.
func TestClaudeLimits(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	now := time.Now()
	for _, r := range []struct {
		account, reading string
		at               time.Time
	}{
		{"default", `{"status":"allowed"}`, now.Add(-time.Hour)},
		{"default", `{"status":"allowed_warning"}`, now},
		{"default", `{"status":"late"}`, now.Add(-2 * time.Hour)},
		{"work", `{"status":"allowed"}`, now},
	} {
		if err := st.SetClaudeLimit(ctx, r.account, []byte(r.reading), r.at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.ClaudeLimits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Account != "default" || string(got[0].Reading) != `{"status":"allowed_warning"}` || got[1].Account != "work" {
		t.Errorf("limits = %+v", got)
	}
}
