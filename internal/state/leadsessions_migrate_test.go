package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// Every project chatted with before sessions gets one, "main": its chat, whose
// lead row it is. A project never chatted with gets none, and its agents
// belong to no session.
func TestLeadSessionsMigrationKeepsEachChat(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	before := len(migrations) - 4
	for i, m := range migrations[:before] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO projects (name, root, created_at) VALUES ('web', '/src/web', 1), ('quiet', '/src/quiet', 1)`,
		`INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at, role)
			VALUES ('web', 'lead', 'ab-web-lead', 'claude', 0, '', 'main', 'c', '/w/web/lead', 'ready', 1700000000, 'lead'),
			       ('web', 'agent-01', 'ab-web-agent-01', 'claude', 1, 'b', 'main', 'c', '/w/web/agent-01', 'ready', 1700000000, 'worker'),
			       ('quiet', 'agent-01', 'ab-quiet-agent-01', 'claude', 1, 'b', 'main', 'c', '/w/quiet/agent-01', 'ready', 1700000000, 'worker')`,
		fmt.Sprintf("PRAGMA user_version = %d", before),
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	web, err := st.LeadSessions(ctx, "web")
	if err != nil || len(web) != 1 || web[0].ID != MigratedSession || web[0].Topic != "Chat" || web[0].CreatedAt.Unix() != 1700000000 {
		t.Errorf("web's sessions = %+v, %v; want its chat as main", web, err)
	}
	if quiet, err := st.LeadSessions(ctx, "quiet"); err != nil || len(quiet) != 0 {
		t.Errorf("quiet's sessions = %+v, %v; want none", quiet, err)
	}
	lead, err := st.Agent(ctx, "web", LeadName)
	if err != nil || lead.Session() != MigratedSession {
		t.Errorf("web's lead = %+v, %v; want it main's", lead, err)
	}
	a, err := st.Agent(ctx, "web", "agent-01")
	if err != nil || a.Session() != "" {
		t.Errorf("web's agent = %+v, %v; want it nobody's", a, err)
	}
}

// The Home chat gets "main" too: its chat, titled, when it has a
// conversation, and an untitled one to write in when it has none.
func TestHomeSessionMigration(t *testing.T) {
	ctx := context.Background()
	for name, used := range map[string]bool{"chatted": true, "new": false} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			db, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			before := len(migrations) - 1
			for i, m := range migrations[:before] {
				if _, err := db.ExecContext(ctx, m); err != nil {
					t.Fatalf("migration %d: %v", i+1, err)
				}
			}
			if used {
				if _, err := db.ExecContext(ctx, `INSERT INTO chat_items (project, agent, id, position, data) VALUES ('_home', 'lead', 'u1', 0, '{}')`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", before)); err != nil {
				t.Fatal(err)
			}
			st, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			home, err := st.LeadSessions(ctx, HomeProject)
			if err != nil || len(home) != 1 || home[0].ID != MigratedSession || home[0].Untitled == used {
				t.Fatalf("Home's sessions = %+v, %v; want main, untitled only when it had no conversation", home, err)
			}
			if want := map[bool]string{true: "Chat", false: UntitledTopic}[used]; home[0].Topic != want {
				t.Errorf("Home's session is %q, want %q", home[0].Topic, want)
			}
		})
	}
}
