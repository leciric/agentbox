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

// Renaming an account carries it over everywhere the store names it, and
// nowhere else: other accounts, GitHub accounts and the token ledger stay as
// they were.
func TestRenameClaudeAccount(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, p := range []Project{
		{Name: "own", Root: "/src/own", ClaudeAccount: "work", GitHubAccount: "work"},
		{Name: "listed", Root: "/src/listed", ClaudeAccount: "client", ClaudeAccounts: []string{"client", "work", "spare"}},
		// An allow-list that still names a removed account called what the
		// new name is keeps one entry for it.
		{Name: "dupe", Root: "/src/dupe", ClaudeAccounts: []string{"work", "personal"}},
		{Name: "other", Root: "/src/other", ClaudeAccount: "client"},
	} {
		p.CreatedAt = time.Now()
		if err := st.AddProject(ctx, p); err != nil {
			t.Fatal(err)
		}
		if len(p.ClaudeAccounts) > 0 {
			if err := st.SetProjectClaudeAccounts(ctx, p.Name, p.ClaudeAccount, p.ClaudeAccounts); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, a := range []Agent{
		{Project: "own", Name: "agent-01", ClaudeAccount: "work", GitHubAccount: "work"},
		{Project: "own", Name: LeadName, ClaudeAccount: "work", Role: "lead"},
		{Project: "other", Name: "agent-01", ClaudeAccount: "client"},
	} {
		a.AI, a.Status, a.CreatedAt = "claude", AgentReady, time.Now()
		a.Instance = a.Project + "-" + a.Name
		if err := st.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	at := time.UnixMilli(1_700_000_000_000)
	if err := st.SetClaudeLimit(ctx, "work", []byte(`{"status":"allowed"}`), at); err != nil {
		t.Fatal(err)
	}
	// A stale reading of a removed account that had the new name.
	if err := st.SetClaudeLimit(ctx, "personal", []byte(`{"status":"rejected"}`), at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetClaudeLimit(ctx, "client", []byte(`{"status":"allowed_warning"}`), at); err != nil {
		t.Fatal(err)
	}

	// A move that fails undoes everything.
	if _, err := st.RenameClaudeAccount(ctx, "work", "personal", func() error { return fmt.Errorf("disk full") }); err == nil {
		t.Fatal("a failed move was not reported")
	}
	if p, _ := st.Project(ctx, "own"); p.ClaudeAccount != "work" {
		t.Fatalf("a failed rename left own on %q", p.ClaudeAccount)
	}

	moved := false
	done, err := st.RenameClaudeAccount(ctx, "work", "personal", func() error { moved = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !moved {
		t.Error("move was not called")
	}
	if want := []string{"dupe", "listed", "own"}; !reflect.DeepEqual(done.Projects, want) {
		t.Errorf("projects carried over = %v, want %v", done.Projects, want)
	}
	if want := []string{"own/agent-01", "own/" + LeadName}; !reflect.DeepEqual(done.Agents, want) {
		t.Errorf("agents carried over = %v, want %v", done.Agents, want)
	}

	for _, c := range []struct {
		name, account string
		allowed       []string
		github        string
	}{
		{"own", "personal", nil, "work"},
		{"listed", "client", []string{"client", "personal", "spare"}, ""},
		{"dupe", "", []string{"personal"}, ""},
		{"other", "client", nil, ""},
	} {
		p, err := st.Project(ctx, c.name)
		if err != nil {
			t.Fatal(err)
		}
		if p.ClaudeAccount != c.account || !reflect.DeepEqual(p.ClaudeAccounts, c.allowed) || p.GitHubAccount != c.github {
			t.Errorf("%s: account %q, allowed %v, github %q; want %q, %v, %q", c.name, p.ClaudeAccount, p.ClaudeAccounts, p.GitHubAccount, c.account, c.allowed, c.github)
		}
	}
	for _, c := range []struct{ project, name, account, github string }{
		{"own", "agent-01", "personal", "work"},
		{"own", LeadName, "personal", ""},
		{"other", "agent-01", "client", ""},
	} {
		a, err := st.Agent(ctx, c.project, c.name)
		if err != nil {
			t.Fatal(err)
		}
		if a.ClaudeAccount != c.account || a.GitHubAccount != c.github {
			t.Errorf("%s/%s: account %q, github %q; want %q, %q", c.project, c.name, a.ClaudeAccount, a.GitHubAccount, c.account, c.github)
		}
	}

	limits, err := st.ClaudeLimits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range limits {
		got[l.Account] = string(l.Reading)
	}
	if want := map[string]string{"client": `{"status":"allowed_warning"}`, "personal": `{"status":"allowed"}`}; !reflect.DeepEqual(got, want) {
		t.Errorf("limits after the rename = %v, want %v", got, want)
	}
}
