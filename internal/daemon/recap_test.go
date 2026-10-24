package daemon

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/state"
)

// TestRecap adds up a week the way its page reads it: work from the turns
// workers generated, in the user's time zone, the part of it no verdict was
// near, tokens by model (a lead's too), the busiest project, the agent that
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
