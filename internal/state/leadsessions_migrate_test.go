package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
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
	before := migrationIndex(t, "SELECT project, 'main', 'Chat'")
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
			before := migrationIndex(t, "SELECT '_home', 'main'")
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

// A session's id is never handed out again once it was used: the ids of the
// sessions deleted before ids were kept are found in what they left behind,
// their leads' rows, conversations and spending.
func TestSessionIDsMigrationKeepsDeletedSessionsIDs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	before := migrationIndex(t, "ADD COLUMN closed_at")
	for i, m := range migrations[:before] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO projects (name, root, created_at) VALUES ('web', '/src/web', 1)`,
		`INSERT INTO lead_sessions (project, id, topic, created_at) VALUES ('web', 'prod', 'Prod', 1)`,
		`INSERT INTO chat_items (project, agent, id, position, data) VALUES ('web', 'lead.session', 'u1', 0, '{}')`,
		`INSERT INTO token_usage (project, agent, ai, kind, at) VALUES ('web', 'lead.session-2', 'claude', 'turn', 1)`,
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
	ls, err := st.AddUntitledLeadSession(ctx, "web", time.Now())
	if err != nil || ls.ID != "session-3" {
		t.Errorf("a new session = %+v, %v; want session-3, past the two ids deleted sessions had", ls, err)
	}
	if prod, err := st.AddLeadSession(ctx, "web", "Prod", time.Now()); err != nil || prod.ID != "prod-2" {
		t.Errorf("a new Prod session = %+v, %v; want prod-2", prod, err)
	}
	open, err := st.LeadSessions(ctx, "web")
	if err != nil || len(open) != 3 || open[0].Closed() {
		t.Errorf("web's sessions = %+v, %v; want all three open", open, err)
	}
}
