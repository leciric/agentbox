package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

// The user's task list went with the Tasks tab: an installation that had
// tasks, blocking edges and a "tasks go to" loses all three on upgrade.
func TestTheTaskListIsDroppedOnUpgrade(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	drop := slices.Index(migrations, "DROP TABLE task_dependencies")
	if drop < 0 {
		t.Fatal("no migration drops the task list")
	}
	run := func(qs ...string) {
		t.Helper()
		for _, q := range qs {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
	run(migrations[:drop]...)
	run(`INSERT INTO tasks (id, project, status, goal, created_at, updated_at) VALUES
			('task_a', 'p', 'open', 'Paginate the reminders page', 1, 1),
			('task_b', 'p', 'open', 'Index the count query', 1, 1)`,
		`INSERT INTO task_dependencies (project, task_id, depends_on_id, created_at) VALUES ('p', 'task_a', 'task_b', 1)`,
		`INSERT INTO settings (key, value) VALUES ('task_target', 'lead')`)
	run(migrations[drop:]...)

	var tables int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('tasks', 'task_dependencies') OR tbl_name IN ('tasks', 'task_dependencies')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Errorf("%d tables or indexes of the task list are left after the migration, want none", tables)
	}
	var settings int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = 'task_target'`).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	if settings != 0 {
		t.Error(`"tasks go to" is still set after the migration`)
	}
}
