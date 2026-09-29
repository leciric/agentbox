package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// The version that turned the shared budget on by itself (the migration
// writing shared_budget_offer, and the daemon) is undone: where the daemon
// turned it on, it's off again; where the user did, it stays on; and the offer
// is gone either way.
func TestSharedBudgetOffByDefaultMigration(t *testing.T) {
	const old = `INSERT INTO projects (name, root, created_at) VALUES ('old', '/src/old', 1700000000)`
	const recent = `INSERT INTO projects (name, root, created_at) VALUES ('new', '/src/new', 1790610000)`
	for _, tc := range []struct {
		name string
		// before is what the database had before the offer's migration,
		// after what the daemon of that version wrote since.
		before, after []string
		budget        string
		chosen        bool
	}{
		{name: "fresh, never turned on"},
		{name: "fresh, the daemon turned it on",
			after: []string{recent, `INSERT INTO settings (key, value) VALUES ('shared_budget', '1')`}},
		{name: "in use, the user turned it on before",
			before: []string{old, `INSERT INTO settings (key, value) VALUES ('shared_budget', '1')`},
			budget: "1", chosen: true},
		{name: "in use, offered and turned on from the offer",
			before: []string{old},
			after:  []string{`INSERT INTO settings (key, value) VALUES ('shared_budget', '1')`},
			budget: "1", chosen: true},
		{name: "in use, offered and left",
			before: []string{old}},
		{name: "turned off by the user",
			after:  []string{recent, `INSERT INTO settings (key, value) VALUES ('shared_budget', '0')`},
			budget: "0", chosen: true},
		{name: "disk settings dropped",
			before: []string{old, `INSERT INTO settings (key, value) VALUES ('shared_budget', '1'), ('shared_budget_disk_weight', '10'), ('shared_budget_disk_write', '16MiB')`},
			budget: "1", chosen: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "state.db")
			db, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			// Up to just before the offer, then what the database had, the
			// offer, and what the daemon did after it.
			offer := migrationIndex(t, "SELECT 'shared_budget_offer', '1'")
			run := func(qs ...string) {
				t.Helper()
				for _, q := range qs {
					if _, err := db.ExecContext(ctx, q); err != nil {
						t.Fatalf("%s: %v", q, err)
					}
				}
			}
			run(migrations[:offer]...)
			run(tc.before...)
			run(migrations[offer])
			run(tc.after...)
			run(fmt.Sprintf("PRAGMA user_version = %d", offer+1))
			_ = db.Close()

			st, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			budget, chosen, err := st.SettingValue(ctx, SettingSharedBudget)
			if err != nil {
				t.Fatal(err)
			}
			if budget != tc.budget || chosen != tc.chosen {
				t.Errorf("shared_budget = %q (set: %v), want %q (set: %v)", budget, chosen, tc.budget, tc.chosen)
			}
			for _, key := range []string{"shared_budget_offer", "shared_budget_disk_weight", "shared_budget_disk_write"} {
				if _, set, _ := st.SettingValue(ctx, key); set {
					t.Errorf("%s is still there", key)
				}
			}
		})
	}
}
