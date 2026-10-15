package state

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Activity is the newest item of any kind across a project's chats, and the
// user's last message ignores what AgentBox woke a chat with or hid. Times
// are compared as times: a later item written with an earlier-sorting
// string (another offset, a trimmed fraction) still wins.
func TestProjectsActivity(t *testing.T) {
	t.Parallel()
	s := openSearchStore(t)
	ctx := context.Background()
	for _, name := range []string{"pawly", "quiet", "empty"} {
		if err := s.AddProject(ctx, Project{Name: name, Root: "/src/" + name}); err != nil {
			t.Fatal(err)
		}
	}
	save := func(project, agent string, items ...map[string]any) {
		t.Helper()
		if err := s.SaveChatItems(ctx, project, agent, chatRows(t, items...)); err != nil {
			t.Fatal(err)
		}
	}
	save("pawly", "lead",
		map[string]any{"id": "u1", "kind": "user", "text": "hi", "createdAt": "2026-10-15T10:00:00Z"},
		map[string]any{"id": "w1", "kind": "user", "text": "wake", "woken": true, "createdAt": "2026-10-15T11:00:00Z"},
		map[string]any{"id": "h1", "kind": "user", "text": "hidden", "hidden": true, "createdAt": "2026-10-15T11:30:00Z"})
	save("pawly", "fix-login",
		// 12:00:00.5Z sorts before 12:00:00Z as a string, but is later.
		map[string]any{"id": "a1", "kind": "assistant", "text": "done", "createdAt": "2026-10-15T12:00:00.5Z"},
		map[string]any{"id": "a0", "kind": "tool", "createdAt": "2026-10-15T12:00:00Z"},
		// 10:30 in -03:00 is 13:30Z: the user's latest.
		map[string]any{"id": "u2", "kind": "user", "text": "more", "createdAt": "2026-10-15T10:30:00-03:00"})
	save("quiet", "lead", map[string]any{"id": "w", "kind": "user", "woken": true, "createdAt": "2026-10-14T09:00:00Z"})

	got, err := s.ProjectsActivity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ProjectActivity{
		{Project: "pawly", LastActivity: "2026-10-15T10:30:00-03:00", LastMessage: "2026-10-15T10:30:00-03:00"},
		{Project: "quiet", LastActivity: "2026-10-14T09:00:00Z"},
	}
	if len(got) != len(want) {
		t.Fatalf("ProjectsActivity() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ProjectsActivity()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Without u2, the activity is the assistant's half second.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM chat_items WHERE id = 'u2'`); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ProjectsActivity(ctx)
	if got[0].LastActivity != "2026-10-15T12:00:00.5Z" || got[0].LastMessage != "2026-10-15T10:00:00Z" {
		t.Errorf("after deleting u2: %+v", got[0])
	}
}

// Both maxima are index lookups, not scans of chat_items: it is polled.
func TestProjectsActivityUsesItsIndexes(t *testing.T) {
	t.Parallel()
	s := openSearchStore(t)
	rows, err := s.db.QueryContext(context.Background(), `EXPLAIN QUERY PLAN `+projectsActivityQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	all := strings.Join(plan, "\n")
	for _, index := range []string{"chat_items_created", "chat_items_user_created"} {
		if !strings.Contains(all, "SEARCH chat_items USING INDEX "+index+" (project=?") {
			t.Errorf("the plan doesn't search %s by project:\n%s", index, all)
		}
	}
	if strings.Contains(all, "SCAN chat_items") || strings.Contains(all, "TEMP B-TREE") {
		t.Errorf("the plan scans or sorts chat_items:\n%s", all)
	}
}

func TestProjectIcons(t *testing.T) {
	t.Parallel()
	s := openSearchStore(t)
	ctx := context.Background()
	if err := s.AddProject(ctx, Project{Name: "pawly", Root: "/src/pawly"}); err != nil {
		t.Fatal(err)
	}
	if v, err := s.ProjectIconVersion(ctx, "pawly"); v != 0 || err != nil {
		t.Fatalf("ProjectIconVersion with no row = %d, %v", v, err)
	}
	t0 := time.UnixMilli(1_000_000)
	if err := s.SaveDetectedProjectIcon(ctx, "pawly", []byte("png"), "image/png", "favicon.ico", t0); err != nil {
		t.Fatal(err)
	}
	v1, _ := s.ProjectIconVersion(ctx, "pawly")
	if v1 != t0.UnixMilli() {
		t.Errorf("version after the first detection = %d, want %d", v1, t0.UnixMilli())
	}
	// The same icon found again doesn't change the version.
	if err := s.SaveDetectedProjectIcon(ctx, "pawly", []byte("png"), "image/png", "favicon.ico", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.ProjectIconVersion(ctx, "pawly"); v != v1 {
		t.Errorf("version after the same detection = %d, want %d", v, v1)
	}
	ic, ok, _ := s.ProjectIcon(ctx, "pawly")
	if !ok || !ic.DetectedAt.Equal(t0.Add(time.Hour)) || string(ic.Detected) != "png" {
		t.Errorf("ProjectIcon = %+v, %v", ic, ok)
	}
	// A choice made in the same millisecond still moves the version.
	if err := s.ChooseProjectIcon(ctx, "pawly", "emoji", "🐶", nil, "", t0); err != nil {
		t.Fatal(err)
	}
	v2, _ := s.ProjectIconVersion(ctx, "pawly")
	if v2 <= v1 {
		t.Errorf("version after choosing = %d, want more than %d", v2, v1)
	}
	ic, _, _ = s.ProjectIcon(ctx, "pawly")
	if ic.Chosen != "emoji" || ic.Emoji != "🐶" || string(ic.Detected) != "png" {
		t.Errorf("after choosing: %+v", ic)
	}
	if err := s.RemoveProject(ctx, "pawly"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ProjectIcon(ctx, "pawly"); ok {
		t.Error("the icon outlived its project")
	}
}
