package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

// The migration adding the tool accounts' columns leaves every project, agent
// and chat made before it on the machine's default accounts.
func TestToolAccountsMigrationKeepsTheDefaults(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// Up to just before the seven statements the tool accounts added.
	before := len(migrations) - 7
	for i, m := range migrations[:before] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", before)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO projects (name, root, created_at, claude_account) VALUES ('old', '/src/old', 1, 'work')`,
		`INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at) VALUES ('old', 'agent-01', 'ab-old-agent-01', 'codex', 1, 'b', 'main', 'c', '/w', 'ready', 1)`,
		`INSERT INTO chats (project, agent, session_id, options, tool, account) VALUES ('old', 'lead', 's', '{}', 'codex', 'work')`,
	} {
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
	p, err := st.Project(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	if p.ClaudeAccount != "work" || p.CodexAccount != "" || p.CursorAccount != "" || len(p.OpenCodeAccounts) != 0 {
		t.Errorf("migrated project = %+v", p)
	}
	a, err := st.Agent(ctx, "old", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if a.CodexAccount != "" || len(a.OpenCodeAccounts) != 0 || a.CursorAccount != "" {
		t.Errorf("migrated agent = %+v", a)
	}
	c, err := st.Chat(ctx, "old", "lead")
	if err != nil {
		t.Fatal(err)
	}
	if c.Tool != "codex" || c.Account != "work" || len(c.ToolAccounts) != 0 {
		t.Errorf("migrated chat = %+v", c)
	}
}

func TestAccountMap(t *testing.T) {
	m := ParseAccountMap(" openai=home,anthropic=work,,bad,empty=")
	if !reflect.DeepEqual(m, AccountMap{"anthropic": "work", "openai": "home"}) {
		t.Fatalf("ParseAccountMap = %v", m)
	}
	if got := m.String(); got != "anthropic=work,openai=home" {
		t.Fatalf("String = %q", got)
	}
	if got := (AccountMap{"a": "", "b": "x"}).Clean(); !reflect.DeepEqual(got, AccountMap{"b": "x"}) {
		t.Fatalf("Clean = %v", got)
	}
	if AccountMap(nil).String() != "" || len(ParseAccountMap("")) != 0 {
		t.Fatal("an empty map isn't an empty column")
	}
}

// A project's, an agent's and a lead chat's tool accounts are stored and read
// back, and renaming an account follows it everywhere it is named.
func TestProjectAndAgentToolAccounts(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.AddProject(ctx, Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now(),
		CodexAccount: "work", OpenCodeAccounts: AccountMap{"anthropic": "work"}, CursorAccount: "team"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddProject(ctx, Project{Name: "other", Root: "/src/other", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Project(ctx, "pawly")
	if p.CodexAccount != "work" || p.CursorAccount != "team" || p.OpenCodeAccounts["anthropic"] != "work" {
		t.Fatalf("project = %+v", p)
	}
	if err := st.SetProjectToolAccount(ctx, "other", "cursor", "team"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectOpenCodeAccounts(ctx, "other", AccountMap{"openai": "work", "anthropic": "home"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectToolAccount(ctx, "other", "opencode", "x"); err == nil {
		t.Fatal("OpenCode's accounts were set as one name")
	}
	if err := st.SetProjectToolAccount(ctx, "nope", "codex", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a project that doesn't exist: %v", err)
	}
	if err := st.AddAgent(ctx, Agent{Project: "pawly", Name: "agent-01", Instance: "i1", AI: "codex", Status: AgentReady, CreatedAt: time.Now(), CodexAccount: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddAgent(ctx, Agent{Project: "pawly", Name: LeadName, Instance: "i2", AI: "claude", Role: RoleLead, Status: AgentReady, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentToolAccounts(ctx, "pawly", LeadName, "home", AccountMap{"anthropic": "work"}, "team"); err != nil {
		t.Fatal(err)
	}
	lead, _ := st.Agent(ctx, "pawly", LeadName)
	if lead.CodexAccount != "home" || lead.OpenCodeAccounts["anthropic"] != "work" || lead.CursorAccount != "team" {
		t.Fatalf("lead = %+v", lead)
	}
	if err := st.SaveChat(ctx, "pawly", LeadName, Chat{Tool: "cursor", ToolAccounts: AccountMap{"cursor": "team", "codex": "work"}}); err != nil {
		t.Fatal(err)
	}

	// A failed move undoes everything.
	if _, err := st.RenameToolAccount(ctx, "cursor", "", "team", "crew", func() error { return fmt.Errorf("disk full") }); err == nil {
		t.Fatal("a failed move was not reported")
	}
	if p, _ := st.Project(ctx, "pawly"); p.CursorAccount != "team" {
		t.Fatal("a failed rename changed the project")
	}

	done, err := st.RenameToolAccount(ctx, "cursor", "", "team", "crew", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(done.Projects, []string{"other", "pawly"}) || !reflect.DeepEqual(done.Agents, []string{"pawly/lead"}) {
		t.Fatalf("renamed = %+v", done)
	}
	if c, _ := st.Chat(ctx, "pawly", LeadName); c.ToolAccounts["cursor"] != "crew" || c.ToolAccounts["codex"] != "work" {
		t.Fatalf("the lead's choice = %v", c.ToolAccounts)
	}

	done, err = st.RenameToolAccount(ctx, "opencode", "anthropic", "work", "office", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(done.Projects, []string{"pawly"}) || !slices.Contains(done.Agents, "pawly/lead") {
		t.Fatalf("renamed = %+v", done)
	}
	other, _ := st.Project(ctx, "other")
	if other.OpenCodeAccounts["openai"] != "work" || other.OpenCodeAccounts["anthropic"] != "home" {
		t.Fatalf("another provider's account of the same name was renamed: %v", other.OpenCodeAccounts)
	}
	if p, _ := st.Project(ctx, "pawly"); p.OpenCodeAccounts["anthropic"] != "office" || p.CodexAccount != "work" {
		t.Fatalf("project after the renames = %+v", p)
	}
}
