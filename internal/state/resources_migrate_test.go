package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The resource settings an earlier release stored — each agent's caps, the
// shared budget, "never freeze my CPU" and "GPU for agents" — are dropped,
// and every other setting stays as it was.
func TestResourceSettingsMigrationDropsThem(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	drop := slices.IndexFunc(migrations, func(q string) bool {
		return strings.Contains(q, "'never_freeze_cpu'") && strings.HasPrefix(q, "DELETE FROM settings")
	})
	if drop < 0 {
		t.Fatal("no migration drops the resource settings")
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
	run(`INSERT INTO settings (key, value) VALUES
			('default_cpu', '2'), ('default_cpu_allowance', ''), ('default_memory', '8GiB'), ('default_memory_seeded', '1'),
			('never_freeze_cpu', '1'), ('keep_free_cpu', '2'),
			('shared_budget', '1'), ('shared_budget_memory', '20GiB'), ('shared_budget_swap', '8GiB'), ('shared_budget_cpu', '12'), ('gpu_for_agents', '1'),
			('auto_stop_idle', '1'), ('disk_floor_min', '10737418240')`)
	run(migrations[drop:]...)

	rows, err := db.QueryContext(ctx, "SELECT key FROM settings ORDER BY key")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	if want := []string{"auto_stop_idle", "disk_floor_min"}; !slices.Equal(keys, want) {
		t.Errorf("settings after the migration: %q, want %q", keys, want)
	}
}
