package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

// The chats there were before a chat that never ran could start on the
// installation's default tool keep the Claude Code they ran: a project's
// first session is held to it, and the other sessions, which follow it, and
// chats that chose a tool are left as they were.
func TestChatToolMigrationHoldsFirstSessionsToClaude(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	const q = `UPDATE chats SET tool = 'claude' WHERE tool = '' AND agent = 'lead'`
	at := slices.Index(migrations, q)
	if at < 0 {
		t.Fatal("no migration holds the chats to Claude Code")
	}
	run := func(qs ...string) {
		t.Helper()
		for _, q := range qs {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
	run(migrations[:at]...)
	run(`INSERT INTO chats (project, agent, session_id, tool) VALUES ('p', 'lead', 's1', '')`,
		`INSERT INTO chats (project, agent, session_id, tool) VALUES ('p', 'lead.two', 's2', '')`,
		`INSERT INTO chats (project, agent, session_id, tool) VALUES ('q', 'lead', 's3', 'codex')`,
		`INSERT INTO chats (project, agent, session_id, tool) VALUES ('_home', 'lead', 's4', '')`)
	run(migrations[at:]...)

	for _, c := range []struct{ project, agent, want string }{
		{"p", "lead", "claude"},
		{"p", "lead.two", ""},
		{"q", "lead", "codex"},
		{"_home", "lead", "claude"},
	} {
		var tool string
		if err := db.QueryRowContext(ctx, `SELECT tool FROM chats WHERE project = ? AND agent = ?`, c.project, c.agent).Scan(&tool); err != nil {
			t.Fatal(err)
		}
		if tool != c.want {
			t.Errorf("%s/%s runs %q after the migration, want %q", c.project, c.agent, tool, c.want)
		}
	}
}
