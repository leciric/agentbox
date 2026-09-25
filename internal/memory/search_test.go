package memory_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// TestFTS5IsAvailable is the assumption the whole of search rests on: the
// pure-Go SQLite AgentBox uses (D11)
// is built with FTS5, and its default tokenizer keeps a path, a port and an
// error string findable as themselves. If this ever fails, Search has to fall
// back to LIKE behind the same interface — so it is tested against the driver
// directly, not through the store, to say plainly which of the two broke.
func TestFTS5IsAvailable(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "fts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE VIRTUAL TABLE probe USING fts5(title, content)`); err != nil {
		t.Fatalf("modernc.org/sqlite has no FTS5: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO probe (title, content) VALUES (?, ?)`,
		"The daemon's port", "internal/daemon/server.go listens on :7777 and answers connection refused when it isn't up"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`"internal/daemon/server.go"`, `"7777"`, `"connection refused"`} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM probe WHERE probe MATCH ?`, query).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if n != 1 {
			t.Errorf("%s matched %d rows, want 1", query, n)
		}
	}
	var rank float64
	if err := db.QueryRow(`SELECT bm25(probe, 2.0, 1.0) FROM probe WHERE probe MATCH ? ORDER BY bm25(probe) LIMIT 1`, `"port"`).Scan(&rank); err != nil {
		t.Fatalf("bm25: %v", err)
	}
}

func TestSearch(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	port, err := s.AddMemory(ctx, memory.Memory{
		Project: "pawly", Kind: memory.KindProject, Importance: 5,
		Title: "The API listens on port 7777", Content: "internal/daemon/server.go binds :7777 inside the agent.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMemory(ctx, memory.Memory{
		Project: "pawly", Kind: memory.KindDiscovery,
		Title: "Emulator boots slowly", Content: "The Android emulator needs three minutes on a cold machine.",
	}); err != nil {
		t.Fatal(err)
	}
	// Another project's memory, with the same words in it.
	if _, err := s.AddMemory(ctx, memory.Memory{Project: "other", Title: "Port 7777 is taken here too"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent(ctx, memory.Event{
		Project: "pawly", Agent: "agent-01", Type: "test_failed",
		Payload: json.RawMessage(`{"error":"connection refused on 127.0.0.1:7777"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReport(ctx, memory.Report{
		Project: "pawly", Agent: "agent-02", Task: "Move the API off 7777",
		Summary:     "Nothing moved yet.",
		Discoveries: []string{"internal/daemon/server.go hardcodes the port"},
	}); err != nil {
		t.Fatal(err)
	}

	// A bare token, which is how a port or a version number is searched for.
	got, err := s.Search(ctx, "pawly", "7777", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Memories) != 1 || got.Memories[0].ID != port.ID {
		t.Errorf("memories = %v, want the one about the port", titles(got.Memories))
	}
	if len(got.Events) != 1 {
		t.Errorf("events = %d, want the failed test", len(got.Events))
	}
	if len(got.Reports) != 1 {
		t.Errorf("reports = %d, want the one about moving the port", len(got.Reports))
	}

	// A filename survives the tokenizer as a phrase, and finds the memory and
	// the report that name it.
	got, err = s.Search(ctx, "pawly", "internal/daemon/server.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Memories) != 1 || len(got.Reports) != 1 {
		t.Errorf("by filename: %d memories, %d reports", len(got.Memories), len(got.Reports))
	}

	// An error string, quoted or not, matches the event's payload.
	got, err = s.Search(ctx, "pawly", "connection refused", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 {
		t.Errorf("by error string: %d events", len(got.Events))
	}

	// The search never crosses a project.
	got, err = s.Search(ctx, "other", "7777", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Memories) != 1 || !strings.Contains(got.Memories[0].Title, "here too") {
		t.Errorf("another project's search = %v", titles(got.Memories))
	}
	if len(got.Events) != 0 || len(got.Reports) != 0 {
		t.Errorf("another project saw %d events and %d reports", len(got.Events), len(got.Reports))
	}

	// A question whose words are spread across the project still answers:
	// nothing has all of them, so the same search runs again with any of them.
	got, err = s.Search(ctx, "pawly", "which port does the emulator use", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Memories) != 2 {
		t.Errorf("a question found %v, want both memories through the any-word pass", titles(got.Memories))
	}

	// A title is worth more than a body: both memories mention a port, and the
	// one called after it comes first.
	ranked, err := s.Search(ctx, "pawly", "port", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked.Memories) == 0 || ranked.Memories[0].ID != port.ID {
		t.Errorf("ranked = %v, want the port memory first", titles(ranked.Memories))
	}

	// What is refused, and what simply finds nothing.
	if _, err := s.Search(ctx, "pawly", "   ", 10); err == nil {
		t.Error("an empty search was accepted")
	}
	// FTS5 syntax typed by a user is words, not syntax: every term is quoted
	// before it reaches MATCH, so this finds little and fails at nothing.
	if _, err := s.Search(ctx, "pawly", `NOT (port* OR "unclosed`, 10); err != nil {
		t.Errorf("a query full of FTS5 syntax failed instead of being read as words: %v", err)
	}
}

// TestSearchLeavesOutSupersededMemories is the rule that makes memory worth
// trusting: what the project has since decided is wrong doesn't come back.
func TestSearchLeavesOutSupersededMemories(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	old, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Title: "The API listens on port 7777"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Search(ctx, "pawly", "7777", 10); len(got.Memories) != 1 {
		t.Fatalf("before superseding: %d memories", len(got.Memories))
	}
	if _, err := s.AddMemory(ctx, memory.Memory{
		Project: "pawly", Title: "The API listens on port 8080", Content: "It moved off 7777.", SupersedesID: old.ID,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Search(ctx, "pawly", "7777", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got.Memories {
		if m.ID == old.ID {
			t.Error("a superseded memory came back from Search")
		}
	}
	if len(got.Memories) != 1 {
		t.Errorf("got %v, want only the replacement (which mentions 7777)", titles(got.Memories))
	}
}

// TestSearchIndexFollowsTheRows checks the triggers rather than the queries:
// an FTS5 index that isn't kept in step with its table is the failure this
// design is most exposed to.
func TestSearchIndexFollowsTheRows(t *testing.T) {
	ctx := context.Background()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := memory.New(st.DB())

	m, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Title: "Chromium needs a virtual display"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Search(ctx, "pawly", "chromium", 10); len(got.Memories) != 1 {
		t.Fatalf("after insert: %d memories", len(got.Memories))
	}
	// The daemon has no delete yet; the trigger is what makes one safe later,
	// so it is exercised here through the database directly.
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Search(ctx, "pawly", "chromium", 10); len(got.Memories) != 0 {
		t.Errorf("after delete: %d memories, want the index to have followed", len(got.Memories))
	}
	// The same for an update, which is what SupersedeMemory does.
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO memories (id, project, kind, title, content, importance, created_at, updated_at)
		 VALUES ('mem_x', 'pawly', 'project', 'Playwright drives the pages', '', 3, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE memories SET title = 'xdotool drives the desktop' WHERE id = 'mem_x'`); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Search(ctx, "pawly", "playwright", 10); len(got.Memories) != 0 {
		t.Error("the index still holds the title an update replaced")
	}
	if got, _ := s.Search(ctx, "pawly", "xdotool", 10); len(got.Memories) != 1 {
		t.Error("the index doesn't hold the title an update wrote")
	}

	// An integrity check is FTS5's own word on whether the index matches the
	// table it mirrors.
	for _, index := range []string{"memories_fts", "events_fts", "reports_fts"} {
		if _, err := st.DB().ExecContext(ctx,
			`INSERT INTO `+index+` (`+index+`) VALUES ('integrity-check')`); err != nil {
			t.Errorf("%s: %v", index, err)
		}
	}
}

func titles(memories []memory.Memory) []string {
	out := make([]string, 0, len(memories))
	for _, m := range memories {
		out = append(out, m.Title)
	}
	return out
}
