package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A project made before the allow-list existed allows every account.
func TestClaudeAccountsMigrationAllowsEverything(t *testing.T) {
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
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(migrations)-1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO projects (name, root, created_at, claude_account) VALUES ('old', '/src/old', 1, 'work')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.Project(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	if p.ClaudeAccounts != nil || p.ClaudeAccount != "work" {
		t.Errorf("migrated project = account %q, allowed %v; want work and every account", p.ClaudeAccount, p.ClaudeAccounts)
	}
}

func TestSetProjectClaudeAccounts(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddProject(ctx, Project{Name: "pawly", Root: "/src/pawly", ClaudeAccount: "work", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	allowed := func() []string {
		t.Helper()
		p, err := st.Project(ctx, "pawly")
		if err != nil {
			t.Fatal(err)
		}
		return p.ClaudeAccounts
	}

	// A list that leaves out the project's own account is refused, and says so.
	err = st.SetProjectClaudeAccounts(ctx, "pawly", "work", []string{"personal", "client"})
	if err == nil || !strings.Contains(err.Error(), `"work"`) || !strings.Contains(err.Error(), "personal, client") {
		t.Fatalf("a list without the project's account: err = %v", err)
	}
	if got := allowed(); got != nil {
		t.Fatalf("a refused list was stored: %v", got)
	}

	if err := st.SetProjectClaudeAccounts(ctx, "pawly", "work", []string{" work", "client", "work", ""}); err != nil {
		t.Fatal(err)
	}
	if got := allowed(); !reflect.DeepEqual(got, []string{"work", "client"}) {
		t.Errorf("allowed = %v, want [work client]", got)
	}

	// The project's own account can only move inside the list.
	if err := st.SetProjectClaudeAccount(ctx, "pawly", "personal"); err == nil {
		t.Error("moved the project to an account its list leaves out")
	}
	if err := st.SetProjectClaudeAccount(ctx, "pawly", "client"); err != nil {
		t.Error(err)
	}
	// Both at once, which is how the app saves them.
	if err := st.SetProjectClaudeAccounts(ctx, "pawly", "personal", []string{"personal"}); err != nil {
		t.Fatal(err)
	}

	if err := st.SetProjectClaudeAccounts(ctx, "pawly", "personal", nil); err != nil {
		t.Fatal(err)
	}
	if got := allowed(); got != nil {
		t.Errorf("clearing the list left %v", got)
	}
	if err := st.SetProjectClaudeAccounts(ctx, "nope", "", nil); err == nil {
		t.Error("set the accounts of a project that doesn't exist")
	}
}
