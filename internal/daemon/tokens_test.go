package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

func getTokens(t *testing.T, d testDaemon, query string) (api.TokenReport, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/tokens?"+query, nil)
	w := httptest.NewRecorder()
	if err := d.srv.tokenReport(w, r); err != nil {
		return api.TokenReport{}, err
	}
	var out api.TokenReport
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// TestTokenReport adds the ledger up the way the Tokens views read it: per
// agent, most expensive first, with each agent's models, and says which agents
// are gone — the ledger keeps them.
func TestTokenReport(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	now := time.Now()
	add := func(agent, model string, ago time.Duration, cacheRead int64, cost float64) {
		t.Helper()
		if err := d.srv.store.AddTokenRows(ctx, []state.TokenRow{{
			Project: "acme", Agent: agent, AI: "claude", Turn: agent + model + ago.String(), Kind: state.TokensTurn,
			Model: model, At: now.Add(-ago), Output: 100, CacheRead: cacheRead, CostUSD: cost, Context: cacheRead,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	add("agent-24", "claude-sonnet-5", time.Hour, 900_000, 3)
	add("agent-24", "claude-haiku-4-5", time.Hour, 50_000, 0.1)
	add("agent-29", "claude-sonnet-5", 2*time.Hour, 300_000, 1)
	add("agent-29", "claude-sonnet-5", 30*time.Hour, 5_000_000, 20)

	report, err := getTokens(t, d, "project=acme&since=5h")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Agents) != 2 || report.Agents[0].Agent != "agent-24" {
		t.Fatalf("agents = %+v, want agent-24 first: it cost the most in the last five hours", report.Agents)
	}
	top := report.Agents[0]
	if top.Total != 950_200 || top.CacheRead != 950_000 || top.MaxContext != 900_000 || top.Turns != 2 || len(top.Models) != 2 {
		t.Errorf("agent-24 = %+v", top)
	}
	if top.Models[0].Model != "claude-sonnet-5" {
		t.Errorf("models = %+v, want the most expensive first", top.Models)
	}
	// Neither agent exists in this daemon's store: the ledger still names them.
	if top.Exists || top.Ref != "acme/agent-24" {
		t.Errorf("agent-24 exists = %v, ref %q", top.Exists, top.Ref)
	}
	if report.Total != 950_200+300_100 || report.Since == nil || report.BucketSeconds != int64((10*time.Minute).Seconds()) {
		t.Errorf("report = total %d, since %v, bucket %ds", report.Total, report.Since, report.BucketSeconds)
	}

	// Since the ledger began, the old turn is in too, and puts agent-29 first.
	all, err := getTokens(t, d, "project=acme")
	if err != nil {
		t.Fatal(err)
	}
	if all.Since != nil || all.Agents[0].Agent != "agent-29" || all.BucketSeconds != int64((24*time.Hour).Seconds()) {
		t.Errorf("all time = since %v, first %s, bucket %d", all.Since, all.Agents[0].Agent, all.BucketSeconds)
	}

	for _, bad := range []string{"agent=agent-24", "since=yesterday", "since=-5h"} {
		if _, err := getTokens(t, d, bad); err == nil {
			t.Errorf("?%s was accepted", bad)
		}
	}
}

func TestParseSince(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for raw, want := range map[string]time.Time{
		"5h":                   now.Add(-5 * time.Hour),
		"90m":                  now.Add(-90 * time.Minute),
		"7d":                   now.AddDate(0, 0, -7),
		"2026-09-21T18:00:00Z": time.Date(2026, 9, 21, 18, 0, 0, 0, time.UTC),
	} {
		got, err := parseSince(raw, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := parseSince("0d", now); err == nil || !strings.Contains(err.Error(), "stretch back from now") {
		t.Errorf("parseSince(0d) = %v", err)
	}
}

func TestCompactWindowSetting(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	out, err := patchSettings(t, d, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.ClaudeCompactWindow != state.DefaultClaudeCompactWindow || out.DefaultClaudeCompactWindow != state.DefaultClaudeCompactWindow {
		t.Errorf("unset = %d (default %d)", out.ClaudeCompactWindow, out.DefaultClaudeCompactWindow)
	}
	if out, err = patchSettings(t, d, `{"claudeCompactWindow":400000}`); err != nil || out.ClaudeCompactWindow != 400_000 {
		t.Errorf("set to 400000 = %d, %v", out.ClaudeCompactWindow, err)
	}
	if _, err := patchSettings(t, d, `{"claudeCompactWindow":5000}`); err == nil {
		t.Error("a window Claude Code would clamp was accepted")
	}
	if out, err = patchSettings(t, d, `{"claudeCompactWindow":0}`); err != nil || out.ClaudeCompactWindow != 0 {
		t.Errorf("set to 0 (the whole window) = %d, %v", out.ClaudeCompactWindow, err)
	}
}

// TestClaudeLimitsRoute: the newest reading of each stored account, the
// default first, its windows in the order a person reads them; an account
// removed since is left out.
func TestClaudeLimitsRoute(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	creds := d.srv.manager(nil).Creds
	for _, name := range []string{"default", "work"} {
		if err := creds.SaveClaudeToken(name, "sk-ant-oat01-"+name); err != nil {
			t.Fatal(err)
		}
	}
	lead := state.Agent{Project: "acme", Name: state.LeadName, AI: "claude", ClaudeAccount: "work"}
	d.srv.claudeLimited(lead, acp.RateLimit{Status: "allowed", UnifiedWindows: map[string]acp.LimitWindow{
		"seven_day":        {Utilization: 0.42, ResetsAt: 1790308800},
		"five_hour":        {Utilization: 0.28, ResetsAt: 1790092800},
		"seven_day_sonnet": {Utilization: 0.1, ResetsAt: 1790308800},
	}})
	d.srv.claudeLimited(state.Agent{Project: "acme", Name: "agent-24", AI: "claude"}, acp.RateLimit{Status: "allowed_warning",
		UnifiedWindows: map[string]acp.LimitWindow{"five_hour": {Utilization: 0.9, ResetsAt: 1790092800}}})
	// A reading for an account that has since been removed from the store.
	if err := d.srv.store.SetClaudeLimit(context.Background(), "gone", []byte(`{"status":"allowed"}`), time.Now()); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	if err := d.srv.claudeLimits(w, httptest.NewRequest(http.MethodGet, "/v1/limits", nil)); err != nil {
		t.Fatal(err)
	}
	var got []api.ClaudeLimit
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Account != "default" || !got[0].Default || got[0].Status != "allowed_warning" || got[1].Account != "work" {
		t.Fatalf("limits = %+v", got)
	}
	var labels []string
	for _, win := range got[1].Windows {
		labels = append(labels, win.Label)
	}
	if strings.Join(labels, ", ") != "5-hour, Weekly, Weekly (Sonnet)" {
		t.Errorf("work's windows = %v", labels)
	}
	if got[1].Windows[0].Utilization != 0.28 || !got[1].Windows[0].ResetsAt.Equal(time.Unix(1790092800, 0)) {
		t.Errorf("the five-hour window = %+v", got[1].Windows[0])
	}
}
