package state

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// A state.db from any earlier release is what `agentbox vm migrate` hands the
// VM's daemon: stopped at every version a release could have left it at, with a
// project, an agent and a chat in it, it migrates to this one and keeps them,
// and Compare finds all of it in the migrated copy.
func TestAnEarlierReleasesStateMigratesAndCompares(t *testing.T) {
	ctx := context.Background()
	for version := 2; version <= len(migrations); version++ {
		dir := t.TempDir()
		old := filepath.Join(dir, "old.db")
		db, err := sql.Open("sqlite", "file:"+old)
		if err != nil {
			t.Fatal(err)
		}
		for i, m := range migrations[:version] {
			if _, err := db.ExecContext(ctx, m); err != nil {
				t.Fatalf("version %d: migration %d: %v", version, i+1, err)
			}
		}
		seed := []string{
			`INSERT INTO projects (name, root, created_at) VALUES ('shop', '/home/u/shop', 1)`,
			`INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at)
			 VALUES ('shop', 'agent-01', 'ab-shop-agent-01', 'claude', 1, 'agentbox/checkout', 'main', 'abc', '/home/u/.local/share/agentbox/worktrees/shop/agent-01', 'ready', 1)`,
		}
		have, err := tables(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if have["chat_items"] {
			seed = append(seed, `INSERT INTO chat_items (project, agent, id, position, data) VALUES ('shop', 'agent-01', 'i1', 0, '{}')`)
		}
		for _, q := range seed {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatalf("version %d: %s: %v", version, q, err)
			}
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()

		migrated := filepath.Join(dir, "state.db")
		copyFile(t, old, migrated)
		st, err := Open(migrated)
		if err != nil {
			t.Fatalf("version %d: migrating: %v", version, err)
		}
		if _, err := st.Agent(ctx, "shop", "agent-01"); err != nil {
			t.Fatalf("version %d: the agent after migrating: %v", version, err)
		}
		from, err := OpenReadOnly(old)
		if err != nil {
			t.Fatal(err)
		}
		carried, err := Compare(ctx, from, st.DB())
		if err != nil {
			t.Fatalf("version %d: %v", version, err)
		}
		for _, c := range carried {
			if c.To < c.From {
				t.Errorf("version %d: %s has %d rows after migrating, of %d", version, c.Table, c.To, c.From)
			}
		}
		if len(carried) == 0 || carried[0].Table != "projects" || carried[0].From != 1 {
			t.Errorf("version %d: Compare = %+v, want the one project first", version, carried)
		}
		_ = from.Close()
		_ = st.Close()
	}
}

// OpenReadOnly never writes: the host's own state.db stays as it was.
func TestOpenReadOnlyRefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	db, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO projects (name, root, created_at) VALUES ('x', '/x', 1)`); err == nil {
		t.Error("a write through OpenReadOnly succeeded")
	}
	if ok, err := HasTable(context.Background(), db, "agents"); err != nil || !ok {
		t.Errorf("HasTable(agents) = %v, %v", ok, err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
