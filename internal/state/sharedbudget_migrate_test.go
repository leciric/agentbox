package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// An installation with projects from before the shared budget turned itself
// on is offered it instead; a fresh one, and one whose user already chose, are
// not.
func TestSharedBudgetOfferMigration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seed   []string
		offer  string
		budget string
	}{
		{name: "fresh"},
		{name: "in use", seed: []string{`INSERT INTO projects (name, root, created_at) VALUES ('old', '/src/old', 1)`}, offer: "1"},
		{name: "chose off", seed: []string{
			`INSERT INTO projects (name, root, created_at) VALUES ('old', '/src/old', 1)`,
			`INSERT INTO settings (key, value) VALUES ('shared_budget', '0')`,
		}, budget: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "state.db")
			db, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			for i, m := range migrations[:len(migrations)-1] {
				if _, err := db.ExecContext(ctx, m); err != nil {
					t.Fatalf("migration %d: %v", i+1, err)
				}
			}
			for _, q := range append([]string{fmt.Sprintf("PRAGMA user_version = %d", len(migrations)-1)}, tc.seed...) {
				if _, err := db.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			_ = db.Close()

			st, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			if got, _ := st.Setting(ctx, SettingSharedBudgetOffer); got != tc.offer {
				t.Errorf("offer = %q, want %q", got, tc.offer)
			}
			if got, _ := st.Setting(ctx, SettingSharedBudget); got != tc.budget {
				t.Errorf("shared_budget = %q, want %q", got, tc.budget)
			}
		})
	}
}
