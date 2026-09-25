package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// modelWindowsMigration is the index of the migration under test: the
// database is built up to just before it, which later migrations follow.
func modelWindowsMigration() int {
	return slices.IndexFunc(migrations, func(m string) bool { return strings.Contains(m, "key = 'claude_model_windows'") })
}

// An installation that remembered opus at its 200k compact window, before
// RememberClaudeModelWindow stopped keeping a capped size, forgets it on
// upgrade, and keeps the windows that can't have been a cap.
func TestCappedModelWindowsAreForgottenOnUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	at := modelWindowsMigration()
	for i, m := range migrations[:at] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", at)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`,
		SettingClaudeModelWindows, `{"opus":200000,"default":200000,"sonnet":1000000,"claude-fable-5-1":1000000}`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w, err := st.ClaudeWindows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Seen) != 2 || w.Seen["sonnet"] != ClaudeFullWindow || w.Seen["claude-fable-5-1"] != ClaudeFullWindow {
		t.Errorf("Seen = %v after the upgrade, want only sonnet and claude-fable-5-1 at 1M", w.Seen)
	}
	for _, model := range []string{"opus", "default", "sonnet"} {
		if got, want := w.ContextWindows(model, 200_000), []int64{200_000, ClaudeFullWindow}; !slices.Equal(got, want) {
			t.Errorf("ContextWindows(%q) = %v, want %v", model, got, want)
		}
	}
}

// The upgrade leaves a setting it can't read, or an empty one, alone.
func TestModelWindowsMigrationToleratesOddValues(t *testing.T) {
	ctx := context.Background()
	for _, value := range []string{`{}`, `not json`, `{"opus":"200000"}`} {
		path := filepath.Join(t.TempDir(), "state.db")
		db, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		at := modelWindowsMigration()
		for _, m := range migrations[:at] {
			if _, err := db.ExecContext(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
		db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", at))
		db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, SettingClaudeModelWindows, value)
		db.Close()
		st, err := Open(path)
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if _, err := st.ClaudeWindows(ctx); err != nil {
			t.Errorf("%s: %v", value, err)
		}
		st.Close()
	}
}
