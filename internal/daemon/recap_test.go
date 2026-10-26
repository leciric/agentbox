package daemon

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/pricing"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// TestRecap adds up a week the way its page reads it: work from the turns
// workers generated, in the user's time zone, the part of it no verdict was
// near, tokens by model (a lead's too), the busiest project, the agent that

// TestRecapCountsTheWeeksAgents counts the pull requests and changed lines of
// the week's agents, live and past, a past agent's from its kept branch, and
// leaves out a past agent of another week.
func TestRecapCountsTheWeeksAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	live := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	d.srv.record(ctx, finishedEvent(live, api.AgentChanges{Files: 1, Insertions: 10, Deletions: 1}, &api.PullRequest{Number: 7, State: "open"}, "Done.", now.Add(-time.Minute)))

	base := strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "HEAD"))
	head := strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	testutil.Git(t, repo, "checkout", "-q", "-b", "agentbox/dark-mode")
	if err := os.WriteFile(filepath.Join(repo, "added.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "-A")
	testutil.Git(t, repo, "commit", "-q", "-m", "past work")
	testutil.Git(t, repo, "checkout", "-q", head)
	archive := func(name string, at time.Time, pr state.ArchivedPR) {
		t.Helper()
		a := state.Agent{
			Project: "hello-stack", Name: name, Instance: "ab-hello-stack-" + name, AI: "claude", Title: name,
			Branch: "agentbox/dark-mode", BaseRef: head, BaseCommit: base, Worktree: "/gone/" + name,
			Status: state.AgentReady, CreatedAt: at.Add(-time.Hour), Interface: state.InterfaceChat,
		}
		if err := d.srv.store.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := d.srv.store.ArchiveAgent(ctx, a, "", pr, at); err != nil {
			t.Fatal(err)
		}
	}
	archive("agent-02", now.Add(-time.Hour), state.ArchivedPR{Number: 5, State: state.PRMerged})
	archive("agent-03", now.AddDate(0, 0, -21), state.ArchivedPR{Number: 3, State: state.PRMerged})

	for range 2 { // the second time, the past agent's diff is the one kept
		r, err := d.srv.buildRecap(ctx, now, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if r.Agents != 2 || r.PRsOpened != 2 || r.PRsMerged != 1 {
			t.Fatalf("agents %d, pull requests %d (%d merged), want 2, 2 and 1", r.Agents, r.PRsOpened, r.PRsMerged)
		}
		if r.Insertions != 12 || r.Deletions != 1 || r.Files != 2 {
			t.Fatalf("changes +%d -%d in %d files, want +12 -1 in 2", r.Insertions, r.Deletions, r.Files)
		}
	}
	// Three weeks back, only the agent of that week.
	r, err := d.srv.buildRecap(ctx, now, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Agents != 1 || r.PRsMerged != 1 || r.Insertions != 2 {
		t.Fatalf("three weeks back: agents %d, merged %d, +%d, want 1, 1 and +2", r.Agents, r.PRsMerged, r.Insertions)
	}
}

// worked longest and the streak.
func TestRecap(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	utc := func(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC) }
	add := func(project, agent, turn, model string, end time.Time, took time.Duration, output int64) {
		t.Helper()
		if err := d.srv.store.AddTokenRows(ctx, []state.TokenRow{{
			Project: project, Agent: agent, AI: "claude", Turn: turn, Kind: state.TokensTurn,
			Model: model, At: end, Output: output, Input: 10, GenerationMS: took.Milliseconds(),
		}}); err != nil {
			t.Fatal(err)
		}
	}
	// One turn, two models: its time counts once.
	add("acme", "agent-1", "t1", "claude-opus-5", utc(22, 10, 0), 2*time.Hour, 1000)
	add("acme", "agent-1", "t1", "claude-haiku-5", utc(22, 10, 0), 2*time.Hour, 200)
	// 23:30 to 01:00 UTC is 00:30 to 02:00 on the 24th in UTC+1.
	add("zeta", "agent-2", "t2", "claude-opus-5", utc(24, 1, 0), 90*time.Minute, 500)
	add("zeta", "agent-2", "t3", "claude-opus-5", utc(23, 12, 0), 10*time.Minute, 50)
	// A lead's turn spends tokens but isn't an agent's work.
	add("acme", state.LeadName, "t4", "claude-opus-5", utc(22, 12, 0), time.Hour, 70)
	// Before the week: only the streak sees it, and it isn't joined to this one.
	add("acme", "agent-0", "t5", "claude-opus-5", utc(10, 10, 0), time.Hour, 5)
	// The user gave a verdict at 08:30 UTC: there until 09:00.
	if err := d.srv.store.SetAgentReview(ctx, state.AgentReview{Project: "acme", Agent: "agent-9", Verdict: "reviewed", At: utc(22, 8, 30)}); err != nil {
		t.Fatal(err)
	}

	r, err := d.srv.buildRecap(ctx, utc(24, 15, 0), time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 18, 0, 0, 0, 0, time.FixedZone("", 3600)); !r.Start.Equal(want) || r.Days[6].Date != "2026-10-24" {
		t.Fatalf("week starts %v and ends on %s, want %v to 2026-10-24", r.Start, r.Days[6].Date, want)
	}
	if want := int64((2*time.Hour + 90*time.Minute + 10*time.Minute).Seconds()); r.WorkSeconds != want {
		t.Errorf("work = %ds, want %ds", r.WorkSeconds, want)
	}
	if want := r.WorkSeconds - 30*60; r.AwaySeconds != want {
		t.Errorf("away = %ds, want %ds: all but the half hour after the verdict", r.AwaySeconds, want)
	}
	if r.Days[4].WorkSeconds != 7200 || r.Days[5].WorkSeconds != 600 || r.Days[6].WorkSeconds != 5400 || r.Days[6].Agents != 1 {
		t.Errorf("days = %+v", r.Days)
	}
	if r.Hours[9] != 3600 || r.Hours[10] != 3600 || r.Hours[0] != 1800 || r.Hours[1] != 3600 || r.Hours[12] != 600 {
		t.Errorf("hours = %v", r.Hours)
	}
	if r.Agents != 2 || r.Projects != 2 {
		t.Errorf("agents = %d in %d projects, want 2 in 2", r.Agents, r.Projects)
	}
	if r.Busiest == nil || r.Busiest.Project != "acme" || r.Longest == nil || r.Longest.Agent != "agent-1" {
		t.Errorf("busiest = %+v, longest = %+v, want acme's agent-1", r.Busiest, r.Longest)
	}
	if len(r.Models) != 2 || r.Models[0].Model != "claude-opus-5" || r.Models[0].Output != 1000+500+50+70 {
		t.Errorf("models = %+v, want opus first with the lead's tokens in it", r.Models)
	}
	// No turn reported a cost: Opus 5's are priced, and claude-haiku-5 has
	// no price.
	opus, _ := pricing.Lookup("claude-opus-5")
	if want := opus.Cost(40, 1620, 0, 0); math.Abs(r.CostUSD-want) > 1e-12 || r.EstimatedUSD != r.CostUSD || r.UnpricedTokens != 210 {
		t.Errorf("cost = $%v, $%v of it estimated, %d tokens unpriced; want $%v, all estimated, 210 unpriced", r.CostUSD, r.EstimatedUSD, r.UnpricedTokens, want)
	}
	if r.Streak != 3 || r.BestStreak != 3 {
		t.Errorf("streak = %d, best %d, want 3 and 3", r.Streak, r.BestStreak)
	}
	// A week ago there was nothing.
	last, err := d.srv.buildRecap(ctx, utc(24, 15, 0), time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	if last.WorkSeconds != 0 || last.Busiest != nil || last.Longest != nil || len(last.Models) != 0 {
		t.Errorf("last week = %+v, want nothing", last)
	}
}

func TestStreaks(t *testing.T) {
	for _, c := range []struct {
		days          []int64
		today         int64
		current, best int
	}{
		{nil, 10, 0, 0},
		{[]int64{8, 9}, 10, 2, 2},              // today has nothing yet
		{[]int64{1, 2, 3, 4, 9, 10}, 10, 2, 4}, // the best was earlier
		{[]int64{7, 8}, 10, 0, 2},              // broken yesterday
	} {
		if cur, best := streaks(c.days, c.today); cur != c.current || best != c.best {
			t.Errorf("streaks(%v, %d) = %d, %d, want %d, %d", c.days, c.today, cur, best, c.current, c.best)
		}
	}
}

func TestRecapSpend(t *testing.T) {
	t.Parallel()
	price := func(model string) pricing.Price {
		p, ok := pricing.Lookup(model)
		if !ok {
			t.Fatalf("%s has no price", model)
		}
		return p
	}
	opus, haiku, codex := price("claude-opus-4-5"), price("claude-haiku-4-5"), price("gpt-5-codex")
	rows := []state.TokenRow{
		// A Claude Code turn with a subagent on Haiku: the turn's $3 is all on
		// its busiest row, and is shared by what each model would cost.
		{AI: "claude", Session: "s1", Turn: "t1", Model: "claude-opus-4-5", Input: 1000, Output: 4000, CacheRead: 100_000, CostUSD: 3},
		{AI: "claude", Session: "s1", Turn: "t1", Model: "claude-haiku-4-5", Input: 500, Output: 2000},
		// A result the session made by itself: a cost and no tokens.
		{AI: "claude", Session: "s1", Turn: "t2", Model: "opus", CostUSD: 0.5},
		// Codex reports no cost: priced from the table.
		{AI: "codex", Session: "s2", Turn: "t1", Model: "gpt-5-codex", Input: 20_000, Output: 3000, CacheRead: 400_000},
		// Same turn id in another session is another turn.
		{AI: "codex", Session: "s3", Turn: "t1", Model: "gpt-5-codex/high", Input: 1000, Output: 100},
		// Cursor on a model with no price: counted, not priced.
		{AI: "cursor", Session: "s4", Turn: "t1", Model: "acme-preview", Input: 7000, Output: 700},
		// A reported turn with a model that has no price keeps its cost where
		// it was reported.
		{AI: "opencode", Session: "s5", Turn: "t1", Model: "acme-preview", Input: 10, Output: 10, CostUSD: 0.25},
		{AI: "opencode", Session: "s5", Turn: "t1", Model: "claude-haiku-4-5", Input: 10, Output: 10},
	}
	var r api.Recap
	recapSpend(&r, rows)

	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	by := map[string]api.RecapModel{}
	for _, m := range r.Models {
		by[m.Model] = m
	}
	if len(r.Models) != 5 || r.Models[0].Model != "gpt-5-codex" {
		t.Fatalf("models = %+v, want five, the most tokens first, and no row for the cost-only result", r.Models)
	}
	o, h := opus.Cost(1000, 4000, 100_000, 0), haiku.Cost(500, 2000, 0, 0)
	if m := by["claude-opus-4-5"]; !near(m.CostUSD, 3*o/(o+h)) || m.EstimatedUSD != 0 || m.UnpricedTokens != 0 {
		t.Errorf("opus = %+v, want $%v of the reported $3", m, 3*o/(o+h))
	}
	if m := by["claude-haiku-4-5"]; !near(m.CostUSD, 3*h/(o+h)) || m.EstimatedUSD != 0 || m.Tokens != 2520 {
		t.Errorf("haiku = %+v, want $%v of the reported $3 and none of OpenCode's", m, 3*h/(o+h))
	}
	codexCost := codex.Cost(20_000, 3000, 400_000, 0) + codex.Cost(1000, 100, 0, 0)
	if m := by["gpt-5-codex"]; !near(m.CostUSD, codex.Cost(20_000, 3000, 400_000, 0)) || m.CostUSD != m.EstimatedUSD {
		t.Errorf("gpt-5-codex = %+v, want an estimate", m)
	}
	if m := by["gpt-5-codex/high"]; !near(m.EstimatedUSD, codex.Cost(1000, 100, 0, 0)) {
		t.Errorf("gpt-5-codex/high = %+v, want an estimate", m)
	}
	if m := by["acme-preview"]; m.UnpricedTokens != 7700 || !near(m.CostUSD, 0.25) || m.EstimatedUSD != 0 {
		t.Errorf("acme-preview = %+v, want Cursor's 7700 tokens unpriced and OpenCode's $0.25", m)
	}
	if want := 3 + 0.5 + 0.25 + codexCost; !near(r.CostUSD, want) || !near(r.EstimatedUSD, codexCost) || r.UnpricedTokens != 7700 {
		t.Errorf("week = $%v, $%v estimated, %d unpriced; want $%v, $%v, 7700", r.CostUSD, r.EstimatedUSD, r.UnpricedTokens, want, codexCost)
	}
	var tokens int64
	for _, m := range r.Models {
		tokens += m.Tokens
	}
	if r.Tokens != tokens {
		t.Errorf("tokens = %d, want the models' %d", r.Tokens, tokens)
	}
}
