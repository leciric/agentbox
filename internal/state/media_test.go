package state_test

import (
	"context"
	"errors"
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
		t.Fatalf("ExpiredMedia() right after orphaning = %+v, %v; want none yet (1 day default)", expired, err)
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

	justBefore, err := st.ExpiredMedia(ctx, now.Add(24*time.Hour-time.Minute))
	if err != nil || len(justBefore) != 0 {
		t.Fatalf("ExpiredMedia() just before a day = %+v, %v; want none", justBefore, err)
	}
	afterward, err := st.ExpiredMedia(ctx, now.Add(24*time.Hour+time.Minute))
	if err != nil || len(afterward) != 1 || afterward[0].ID != "old-item" {
		t.Fatalf("ExpiredMedia() just after a day = %+v, %v; want [old-item]", afterward, err)
	}
}

// The installation's retention decides, whatever the project once said:
// seven days keeps an item past the default's one, "immediately" makes it due
// at once, and "forever" never does. Media that outlived its project follows
// the same rule.
func TestExpiredMediaFollowsTheInstallationsRetention(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectMediaRetentionDays(ctx, "pawly", 90); err != nil { // no longer read
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
	for _, tc := range []struct {
		retention string
		after     time.Duration
		want      int
	}{
		{"", 2 * 24 * time.Hour, 1},         // the default, a day
		{"nonsense", 2 * 24 * time.Hour, 1}, // reads as the default
		{"7d", 2 * 24 * time.Hour, 0},
		{"7d", 8 * 24 * time.Hour, 1},
		{"30d", 29 * 24 * time.Hour, 0},
		{"immediately", 0, 1},
		{"forever", 10 * 365 * 24 * time.Hour, 0},
	} {
		if err := st.SetSetting(ctx, state.SettingMediaRetention, tc.retention); err != nil {
			t.Fatal(err)
		}
		if expired, err := st.ExpiredMedia(ctx, now.Add(tc.after)); err != nil || len(expired) != tc.want {
			t.Errorf("ExpiredMedia() with %q, %s after = %d item(s), %v; want %d", tc.retention, tc.after, len(expired), err, tc.want)
		}
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

// A favorite never expires, however long its agent has been gone, and
// unfavoriting it starts its clock again from then rather than letting it go
// at once.
func TestFavoriteMediaNeverExpiresUntilUnfavorited(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 30, 12, 0, 0, 0, time.UTC)
	addMedia(t, st, "kept", "pawly", "agent-01", now)
	addMedia(t, st, "plain", "pawly", "agent-01", now)
	item, err := st.SetMediaFavorite(ctx, "kept", true, now)
	if err != nil || !item.Favorite {
		t.Fatalf("SetMediaFavorite(true) = %+v, %v; want a favorite", item, err)
	}
	if err := st.OrphanAgentMedia(ctx, "pawly", "agent-01", now); err != nil {
		t.Fatal(err)
	}

	later := now.Add(365 * 24 * time.Hour)
	expired, err := st.ExpiredMedia(ctx, later)
	if err != nil || len(expired) != 1 || expired[0].ID != "plain" {
		t.Fatalf("ExpiredMedia() a year on = %+v, %v; want only [plain]", expired, err)
	}

	item, err = st.SetMediaFavorite(ctx, "kept", false, later)
	if err != nil || item.Favorite || !item.OrphanedAt.Equal(later) {
		t.Fatalf("SetMediaFavorite(false) = %+v, %v; want not a favorite, orphaned at %v", item, err, later)
	}
	if expired, _ := st.ExpiredMedia(ctx, later.Add(time.Hour)); len(expired) != 1 {
		t.Errorf("ExpiredMedia() an hour after unfavoriting = %+v; want still only [plain]", expired)
	}
	if expired, _ := st.ExpiredMedia(ctx, later.Add(25*time.Hour)); len(expired) != 2 {
		t.Errorf("ExpiredMedia() a day after unfavoriting = %+v; want both", expired)
	}

	// Unfavoriting an item that wasn't one, or whose agent is still there,
	// leaves its clock alone.
	addMedia(t, st, "live", "pawly", "agent-02", now)
	if item, err := st.SetMediaFavorite(ctx, "live", false, later); err != nil || !item.OrphanedAt.IsZero() {
		t.Errorf("SetMediaFavorite(false) on a live agent's item = %+v, %v; want it still unorphaned", item, err)
	}
	if _, err := st.SetMediaFavorite(ctx, "never-existed", true, now); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("SetMediaFavorite() on a missing item = %v, want ErrNotFound", err)
	}
}

// DeleteAgentMedia keeps an agent's favorites and says which they were.
func TestDeleteAgentMediaKeepsFavorites(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	now := time.Now()
	addMedia(t, st, "kept", "pawly", "agent-01", now)
	addMedia(t, st, "plain", "pawly", "agent-01", now)
	if _, err := st.SetMediaFavorite(ctx, "kept", true, now); err != nil {
		t.Fatal(err)
	}
	kept, err := st.DeleteAgentMedia(ctx, "pawly", "agent-01")
	if err != nil || len(kept) != 1 || kept[0].ID != "kept" {
		t.Fatalf("DeleteAgentMedia() = %+v, %v; want [kept]", kept, err)
	}
	if _, err := st.MediaItem(ctx, "plain"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("plain item after DeleteAgentMedia = %v, want ErrNotFound", err)
	}
}
