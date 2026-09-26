package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// An upgrade that fails part of the way through leaves the database at the
// version it started from, with none of the upgrade's earlier migrations
// applied: the pending migrations run as one transaction.
func TestFailedUpgradeRollsBackWhole(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	start := len(migrations) - 1
	for i, m := range migrations[:start] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", start)); err != nil {
		t.Fatal(err)
	}

	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = append(append([]string(nil), saved...),
		`CREATE TABLE upgrade_probe (x INTEGER)`,
		`THIS IS NOT SQL`)

	if st, err := Open(path); err == nil {
		_ = st.Close()
		t.Fatal("Open succeeded with a broken migration")
	}

	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != start {
		t.Errorf("user_version = %d after a failed upgrade, want %d", version, start)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'upgrade_probe'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("a migration before the failed one stayed applied")
	}
}

// TestNameReuseMigrationCleansUpOrphans upgrades a database that predates
// #name-reuse's migration and has, from before removeChat and
// CancelQuestions ran on every removal, chat and question rows left behind
// by agents that no longer exist. The migration must clean those up without
// touching the rows of agents that do.
func TestNameReuseMigrationCleansUpOrphans(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// The name-reuse migration set is the last 8 entries appended to
	// migrations; stop just short of it.
	start := len(migrations) - 8
	for i, m := range migrations[:start] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", start)); err != nil {
		t.Fatal(err)
	}

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO projects (name, root, created_at) VALUES ('pawly', '/src/pawly', 0)`)
	exec(`INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at)
		VALUES ('pawly', 'agent-01', 'ab-pawly-agent-01', 'claude', 0, 'agentbox/agent-01', 'main', 'abc', '/w/agent-01', 'ready', 0)`)

	// agent-01 still exists: its rows must survive.
	exec(`INSERT INTO chats (project, agent, session_id) VALUES ('pawly', 'agent-01', 'sess-01')`)
	exec(`INSERT INTO chat_items (project, agent, id, position, data) VALUES ('pawly', 'agent-01', 'i1', 0, '{}')`)
	exec(`INSERT INTO agent_events (id, project, agent, created_at, data) VALUES ('e1', 'pawly', 'agent-01', 0, '{}')`)
	exec(`INSERT INTO questions (id, project, agent, text, status, created_at) VALUES ('q1', 'pawly', 'agent-01', 'q?', 'pending', 0)`)

	// agent-99 is gone: its leftover rows are orphans.
	exec(`INSERT INTO chats (project, agent, session_id) VALUES ('pawly', 'agent-99', 'sess-99')`)
	exec(`INSERT INTO chat_items (project, agent, id, position, data) VALUES ('pawly', 'agent-99', 'i2', 0, '{}')`)
	exec(`INSERT INTO agent_events (id, project, agent, created_at, data) VALUES ('e2', 'pawly', 'agent-99', 0, '{}')`)
	exec(`INSERT INTO questions (id, project, agent, text, status, created_at) VALUES ('q2', 'pawly', 'agent-99', 'q?', 'pending', 0)`)
	exec(`INSERT INTO questions (id, project, agent, text, status, created_at, answered_at) VALUES ('q3', 'pawly', 'agent-99', 'q?', 'answered', 0, 1)`)

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	for _, table := range []string{"chats", "chat_items", "agent_events"} {
		var n int
		if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE agent = 'agent-99'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s: %d orphaned rows for agent-99 left after migrating", table, n)
		}
		if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE agent = 'agent-01'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s: agent-01's own row was removed too", table)
		}
	}

	var status string
	if err := st.db.QueryRowContext(ctx, `SELECT status FROM questions WHERE id = 'q1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Errorf("agent-01's pending question is now %q", status)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT status FROM questions WHERE id = 'q2'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != QuestionCancelled {
		t.Errorf("agent-99's pending question is now %q, want %q", status, QuestionCancelled)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT status FROM questions WHERE id = 'q3'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "answered" {
		t.Errorf("agent-99's already-answered question is now %q, want it left alone", status)
	}

	var id string
	if err := st.db.QueryRowContext(ctx, `SELECT id FROM agents WHERE project = 'pawly' AND name = 'agent-01'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Error("agent-01's row got no id from the migration")
	}
}
