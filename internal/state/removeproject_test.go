package state

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func openRemoveTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func countRows(t *testing.T, st *Store, table, project string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE project = ?`, project).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// An agent ever made leaves an agent_seq row that references the project, and
// the project's chat, secrets and the rest are keyed by it: removing the
// project, once its agents are destroyed, takes them all and nothing of
// another project's.
func TestRemoveProjectAfterItHadAnAgent(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	for _, name := range []string{"pawly", "other"} {
		if err := st.AddProject(ctx, Project{Name: name, Root: "/src/" + name, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}

	for _, project := range []string{"pawly", "other"} {
		name, err := st.NextAgentName(ctx, project, 1)
		if err != nil {
			t.Fatal(err)
		}
		a := Agent{
			Project: project, Name: name, Instance: "ab-" + project + "-" + name,
			AI: "claude", Branch: "agentbox/" + name, BaseRef: "main", BaseCommit: "abc123",
			Worktree: "/data/worktrees/" + project + "/" + name,
			Status:   AgentReady, CreatedAt: time.Now(), Source: "agentbox-base/ready", Interface: InterfaceChat,
		}
		if err := st.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := st.SetSecret(ctx, Secret{Project: project, Name: "KEY", Value: []byte("v"), UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveChat(ctx, project, "", Chat{SessionID: "s"}); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveChatItems(ctx, project, "", []ChatItem{{ID: "i1", Position: 1, Data: []byte(`{}`)}}); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMedia(ctx, Media{ID: "m-" + project, Project: project, Agent: name, Kind: "screenshot", Name: "x", Source: "test", Meta: "{}", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := st.RemoveAgent(ctx, project, name); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.RemoveProject(ctx, "pawly"); err != nil {
		t.Fatalf("RemoveProject after its agent was destroyed: %v", err)
	}

	for _, table := range projectOwned {
		if n := countRows(t, st, table, "pawly"); n != 0 {
			t.Errorf("%s still has %d row(s) of the removed project", table, n)
		}
	}
	for _, table := range []string{"agent_seq", "secrets", "chats", "chat_items"} {
		if n := countRows(t, st, table, "other"); n == 0 {
			t.Errorf("%s lost the other project's rows", table)
		}
	}
	if n := countRows(t, st, "media", "pawly"); n != 1 {
		t.Errorf("a removed project's media = %d, want it kept for the retention sweep", n)
	}
	if _, err := st.Project(ctx, "other"); err != nil {
		t.Errorf("the other project: %v", err)
	}

	// Added again under the same name, it starts from agent-01.
	if err := st.AddProject(ctx, Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got, err := st.NextAgentName(ctx, "pawly", 1); err != nil || got != "agent-01" {
		t.Errorf("first agent of a project added again = %q, %v", got, err)
	}
}

// A table that references projects(name) fails RemoveProject with a foreign
// key error, and one with a project column but no key silently leaks its rows
// into a project added again. Every one must be in projectOwned (deleted) or
// projectKept (left on purpose), so a new migration can't forget.
func TestEveryProjectTableIsRemovedOrKept(t *testing.T) {
	st := openRemoveTest(t)
	rows, err := st.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()

	var found int
	for _, table := range tables {
		var column, fk int
		if err := st.db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = 'project'`, table).Scan(&column); err != nil {
			t.Fatal(err)
		}
		if err := st.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list(?) WHERE "table" = 'projects'`, table).Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if column == 0 && fk == 0 {
			continue
		}
		found++
		_, kept := projectKept[table]
		owned := slices.Contains(projectOwned, table)
		switch {
		case owned && kept:
			t.Errorf("%s is both in projectOwned and projectKept", table)
		case !owned && !kept:
			t.Errorf("%s is keyed by project but RemoveProject neither deletes nor keeps it: add it to projectOwned or projectKept in state.go", table)
		case fk > 0 && kept && table != "agents":
			t.Errorf("%s references projects(name), so keeping its rows makes RemoveProject fail", table)
		}
	}
	if found < len(projectOwned) {
		t.Errorf("found %d project tables, but projectOwned lists %d: one doesn't exist", found, len(projectOwned))
	}
	for _, table := range projectOwned {
		if !slices.Contains(tables, table) {
			t.Errorf("projectOwned lists %s, which isn't a table", table)
		}
	}
}
