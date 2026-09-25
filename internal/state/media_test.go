package state_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/state"
)

func addMedia(t *testing.T, st *state.Store, id, project, agent string, createdAt time.Time) {
	t.Helper()
	if err := st.AddMedia(context.Background(), state.Media{
		ID: id, Project: project, Agent: agent, Kind: "note", Name: id, Source: "agent", Meta: "{}", CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
}

// A live agent's media never expires, however old, until OrphanAgentMedia
// starts its clock; ExpiredMedia counts from then, not from created_at.
func TestExpiredMediaCountsFromWhenItWasOrphaned(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 30, 12, 0, 0, 0, time.UTC)
	// Already 60 days old by created_at: expiring from there would vanish the
	// instant it's kept, which isn't the rule this store implements.
	addMedia(t, st, "old-item", "pawly", "agent-01", now.Add(-60*24*time.Hour))
	addMedia(t, st, "live-item", "pawly", "agent-02", now.Add(-90*24*time.Hour))

	if expired, err := st.ExpiredMedia(ctx, now); err != nil || len(expired) != 0 {
		t.Fatalf("ExpiredMedia() before any agent is orphaned = %+v, %v; want none", expired, err)
	}

	if err := st.OrphanAgentMedia(ctx, "pawly", "agent-01", now); err != nil {
		t.Fatal(err)
	}
	if expired, err := st.ExpiredMedia(ctx, now); err != nil || len(expired) != 0 {
		t.Fatalf("ExpiredMedia() right after orphaning = %+v, %v; want none yet (30 day default)", expired, err)
	}
	// agent-02's media is never orphaned: it must never show up, however old.
	stillLive, err := st.ExpiredMedia(ctx, now.Add(365*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range stillLive {
		if item.ID == "live-item" {
			t.Error("a live agent's media expired")
		}
	}

	justBefore, err := st.ExpiredMedia(ctx, now.Add(30*24*time.Hour-time.Minute))
	if err != nil || len(justBefore) != 0 {
		t.Fatalf("ExpiredMedia() just before 30 days = %+v, %v; want none", justBefore, err)
	}
	afterward, err := st.ExpiredMedia(ctx, now.Add(30*24*time.Hour+time.Minute))
	if err != nil || len(afterward) != 1 || afterward[0].ID != "old-item" {
		t.Fatalf("ExpiredMedia() just after 30 days = %+v, %v; want [old-item]", afterward, err)
	}
}

// A project's own retention period overrides the 30-day default.
func TestExpiredMediaUsesProjectRetention(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectMediaRetentionDays(ctx, "pawly", 5); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	addMedia(t, st, "item", "pawly", "agent-01", now)
	if err := st.OrphanAgentMedia(ctx, "pawly", "agent-01", now); err != nil {
		t.Fatal(err)
	}
	if expired, err := st.ExpiredMedia(ctx, now.Add(4*24*time.Hour)); err != nil || len(expired) != 0 {
		t.Fatalf("ExpiredMedia() before the project's 5-day period = %+v, %v; want none", expired, err)
	}
	if expired, err := st.ExpiredMedia(ctx, now.Add(6*24*time.Hour)); err != nil || len(expired) != 1 {
		t.Fatalf("ExpiredMedia() after the project's 5-day period = %+v, %v; want one item", expired, err)
	}
}

// Media can outlive its project too (the project was removed once its agents
// were all gone). It still isn't immortal: it falls back to the default period.
func TestExpiredMediaFallsBackWhenProjectIsGone(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	addMedia(t, st, "item", "pawly", "agent-01", now)
	if err := st.OrphanAgentMedia(ctx, "pawly", "agent-01", now); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveProject(ctx, "pawly"); err != nil {
		t.Fatal(err)
	}
	if expired, err := st.ExpiredMedia(ctx, now.Add(state.DefaultMediaRetentionDays*24*time.Hour+time.Minute)); err != nil || len(expired) != 1 {
		t.Fatalf("ExpiredMedia() past the default period, project gone = %+v, %v; want one item", expired, err)
	}
}

// A destroyed agent's name can be reused: OrphanAgentMedia must not reset the
// clock on media already orphaned by an earlier agent of the same name.
func TestOrphanAgentMediaKeepsAnAlreadyStartedClock(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	t0 := time.UnixMilli(time.Now().UnixMilli()) // truncated, so it round-trips exactly
	addMedia(t, st, "item", "pawly", "agent-01", t0)
	if err := st.OrphanAgentMedia(ctx, "pawly", "agent-01", t0); err != nil {
		t.Fatal(err)
	}
	// A new agent-01 exists now (reused name); it orphans again, much later.
	if err := st.OrphanAgentMedia(ctx, "pawly", "agent-01", t0.Add(365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	item, err := st.MediaItem(ctx, "item")
	if err != nil {
		t.Fatal(err)
	}
	if !item.OrphanedAt.Equal(t0) {
		t.Errorf("OrphanedAt = %v, want the original %v (unaffected by the later reuse)", item.OrphanedAt, t0)
	}
}
