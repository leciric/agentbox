package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// "Pause whole agents for memory" is gone: its switch and the boots it
// measured are dropped, while the agents it had frozen stay for the daemon
// to thaw as it starts, and every other setting stays as it was.
func TestAgentFreezeMigrationDropsItsSettings(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	drop := slices.IndexFunc(migrations, func(q string) bool {
		return strings.HasPrefix(q, "DELETE FROM settings") && strings.Contains(q, "'agent_freeze'")
	})
	if drop < 0 {
		t.Fatal("no migration drops agent_freeze")
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
			('agent_freeze', '1'), ('boot_costs', '[1342177280]'), ('agent_freeze_frozen', '["p/agent-02"]'),
			('auto_stop_idle', '1')`)
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
	if want := []string{SettingAgentFreezeFrozen, "auto_stop_idle"}; !slices.Equal(keys, want) {
		t.Errorf("settings after the migration: %q, want %q", keys, want)
	}
}
