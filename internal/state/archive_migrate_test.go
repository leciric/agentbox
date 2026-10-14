package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Past agents arrive in a migration that loses nothing: an installation's
// agents and their chats are all there after it, still live, and the archive
// starts empty.
func TestArchiveMigrationKeepsEveryAgent(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	at := slices.IndexFunc(migrations, func(q string) bool { return strings.HasPrefix(q, "CREATE TABLE archived_agents") })
	if at < 0 {
		t.Fatal("no migration makes archived_agents")
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
	run(`INSERT INTO projects (name, root, created_at) VALUES ('p', '/src/p', 1)`,
		`INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at, id)
			VALUES ('p', 'agent-01', 'ab-p-agent-01', 'claude', 1, 'agentbox/a', 'main', 'c', '/w', 'ready', 1, 'agt_1')`,
		`INSERT INTO chat_items (project, agent, id, position, data) VALUES ('p', 'agent-01', 'u1', 0, '{"kind":"user","text":"hi"}')`)
	run(migrations[at:]...)

	count := func(q string) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM agents`); n != 1 {
		t.Errorf("%d agents after the migration, want 1", n)
	}
	if n := count(`SELECT count(*) FROM chat_items`); n != 1 {
		t.Errorf("%d chat items after the migration, want 1", n)
	}
	if n := count(`SELECT count(*) FROM archived_agents`); n != 0 {
		t.Errorf("%d archived agents after the migration, want none", n)
	}
}
