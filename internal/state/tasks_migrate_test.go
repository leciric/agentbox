package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

// The tasks written before the list became the user's alone can't be told
// apart from the ones the user wrote, so the migration clears the list.
func TestUserManagedTasksMigrationClearsTheList(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	clear := slices.Index(migrations, "DELETE FROM task_dependencies")
	if clear < 0 {
		t.Fatal("no migration clears the task list")
	}
	run := func(qs ...string) {
		t.Helper()
		for _, q := range qs {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
	run(migrations[:clear]...)
	run(`INSERT INTO tasks (id, project, agent, status, goal, created_at, updated_at) VALUES
			('task_a', 'p', 'agent-01', 'active', 'Written by the agent''s creation', 1, 1),
			('task_b', 'p', '', 'open', 'Written by the lead or the user', 1, 1)`,
		`INSERT INTO task_dependencies (project, task_id, depends_on_id, created_at) VALUES ('p', 'task_a', 'task_b', 1)`)
	run(migrations[clear:]...)

	for _, table := range []string{"tasks", "task_dependencies"} {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows after the migration, want none", table, n)
		}
	}
}
