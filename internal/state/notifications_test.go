package state_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/state"
)

// The history is kept per project, newest first, for KeepNotifications; a
// media notification's seen state is what marks its item unseen.
func TestNotificationsHistoryAndSeen(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, n := range []state.Notification{
		{ID: "old", Project: "pawly", Agent: "agent-01", Kind: "finished", CreatedAt: now.Add(-state.KeepNotifications - time.Hour)},
		{ID: "a", Project: "pawly", Agent: "agent-01", Kind: "finished", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "b", Project: "pawly", Agent: "agent-02", Kind: "media", MediaID: "m1", CreatedAt: now.Add(-time.Minute)},
		{ID: "gone", Project: "removed", Agent: "agent-01", Kind: "finished", CreatedAt: now},
	} {
		n.Data = []byte(`{}`)
		if err := st.AddNotification(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.Notifications(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "b" || list[1].ID != "a" {
		t.Fatalf("Notifications() = %+v, want b then a", list)
	}
	if unseen, err := st.UnseenMedia(ctx); err != nil || !unseen["m1"] {
		t.Fatalf("UnseenMedia() = %v, %v; want m1", unseen, err)
	}
	if n, err := st.SeeNotifications(ctx, nil, []string{"m1"}, false, false, now); err != nil || n != 1 {
		t.Fatalf("SeeNotifications(media) = %d, %v; want 1", n, err)
	}
	if unseen, _ := st.UnseenMedia(ctx); unseen["m1"] {
		t.Error("m1 still unseen after its notification was seen")
	}
	if n, err := st.SeeNotifications(ctx, nil, nil, false, false, now); err != nil || n != 0 {
		t.Errorf("SeeNotifications(nothing) = %d, %v; want 0", n, err)
	}
	if err := st.AddNotification(ctx, state.Notification{ID: "c", Project: "pawly", Agent: "agent-02", Kind: "media", MediaID: "m2", CreatedAt: now, Data: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if n, err := st.SeeNotifications(ctx, nil, nil, false, true, now); err != nil || n != 1 {
		t.Errorf("SeeNotifications(allMedia) = %d, %v; want 1, c's, and not a's", n, err)
	}
	if n, err := st.SeeNotifications(ctx, []string{"a"}, nil, true, false, now); err != nil || n != 2 {
		t.Errorf("SeeNotifications(all) = %d, %v; want 2 (a and gone)", n, err)
	}
	list, _ = st.Notifications(ctx, now)
	for _, n := range list {
		if n.SeenAt.IsZero() {
			t.Errorf("%s unseen after all were seen", n.ID)
		}
	}
}

func TestAllMediaFiltersByKind(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	addMedia(t, st, "n1", "pawly", "agent-01", time.Now().Add(-time.Minute))
	if err := st.AddMedia(ctx, state.Media{ID: "s1", Project: "other", Agent: "agent-02", Kind: "screenshot", Name: "s1", Source: "agent", Meta: "{}", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if all, err := st.AllMedia(ctx, nil); err != nil || len(all) != 2 || all[0].ID != "s1" {
		t.Fatalf("AllMedia() = %+v, %v; want s1 then n1", all, err)
	}
	if shots, err := st.AllMedia(ctx, []string{"screenshot", "recording"}); err != nil || len(shots) != 1 || shots[0].ID != "s1" {
		t.Fatalf("AllMedia(screenshot, recording) = %+v, %v; want s1", shots, err)
	}
}
