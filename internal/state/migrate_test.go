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
	defer db.Close()
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
		st.Close()
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
