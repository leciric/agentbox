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

// An agent's worktree under ~/.local/share/agentbox is under ~/.agentbox once
// the database is opened, whichever home it is in; one elsewhere is left be.
func TestWorktreesFollowTheDataToDotAgentbox(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(migrations, func(m string) bool { return strings.Contains(m, "UPDATE agents SET worktree = replace") })
	if at < 0 {
		t.Fatal("no migration moves the worktrees")
	}
	for i, m := range migrations[:at] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", at)); err != nil {
		t.Fatal(err)
	}
	worktrees := map[string]string{
		"agent-01": "/home/u/.local/share/agentbox/worktrees/shop/agent-01",
		"agent-02": "/Users/u/.local/share/agentbox/worktrees/shop/agent-02",
		"agent-03": "/srv/worktrees/shop/agent-03",
	}
	for name, wt := range worktrees {
		if _, err := db.ExecContext(ctx, `INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at)
			VALUES ('shop', ?, ?, 'claude', 1, 'b', 'main', 'abc', ?, 'ready', 1)`, name, "ab-shop-"+name, wt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO artifacts (id, project, type, path, created_at) VALUES ('a1', 'shop', 'file', ?, 1)`,
		worktrees["agent-01"]+"/out.txt"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	want := map[string]string{
		"agent-01": "/home/u/.agentbox/worktrees/shop/agent-01",
		"agent-02": "/Users/u/.agentbox/worktrees/shop/agent-02",
		"agent-03": "/srv/worktrees/shop/agent-03",
	}
	for name, w := range want {
		a, err := st.Agent(ctx, "shop", name)
		if err != nil {
			t.Fatal(err)
		}
		if a.Worktree != w {
			t.Errorf("%s's worktree = %s, want %s", name, a.Worktree, w)
		}
	}
	var art string
	if err := st.DB().QueryRowContext(ctx, `SELECT path FROM artifacts WHERE id = 'a1'`).Scan(&art); err != nil {
		t.Fatal(err)
	}
	if art != "/home/u/.agentbox/worktrees/shop/agent-01/out.txt" {
		t.Errorf("artifact path = %s", art)
	}
}

func TestMovePaths(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO projects (name, root, created_at) VALUES ('shop', '/src/shop', 1)`); err != nil {
		t.Fatal(err)
	}
	for name, wt := range map[string]string{
		"agent-01": "/data/é/agentbox/worktrees/shop/agent-01",
		"agent-02": "/data/é/agentbox-other/worktrees/shop/agent-02",
	} {
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at)
			VALUES ('shop', ?, ?, 'claude', 1, 'b', 'main', 'abc', ?, 'ready', 1)`, name, "ab-shop-"+name, wt); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.MovePaths(ctx, "/data/é/agentbox", "/home/u/.agentbox")
	if err != nil || n != 1 {
		t.Fatalf("MovePaths = %d, %v, want 1 row", n, err)
	}
	for name, want := range map[string]string{
		"agent-01": "/home/u/.agentbox/worktrees/shop/agent-01",
		"agent-02": "/data/é/agentbox-other/worktrees/shop/agent-02",
	} {
		a, err := st.Agent(ctx, "shop", name)
		if err != nil {
			t.Fatal(err)
		}
		if a.Worktree != want {
			t.Errorf("%s's worktree = %s, want %s", name, a.Worktree, want)
		}
	}
}
