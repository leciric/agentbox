package daemon

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// The check `agentbox vm migrate` ends with passes when the daemon has
// everything the backup had, and names what it doesn't: an agent, a chat's
// items, a machine.
func TestCheckMigration(t *testing.T) {
	t.Parallel()
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(d.root, "worktree"), 0o755); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(d.root, "backup.db")
	if _, err := d.srv.store.DB().ExecContext(ctx, "VACUUM INTO ?", backup); err != nil {
		t.Fatal(err)
	}
	check, err := d.client.CheckMigration(ctx, api.MigrationCheckRequest{Backup: backup})
	if err != nil {
		t.Fatal(err)
	}
	if !check.OK || len(check.Problems) > 0 {
		t.Fatalf("a daemon made from the backup: %+v", check)
	}
	if found := strings.Join(check.Found, "\n"); !strings.Contains(found, "1 project(s): hello-stack") || !strings.Contains(found, "1 agent(s), 1 of them with their worktree and a machine here") {
		t.Errorf("found:\n%s", found)
	}

	// What the backup has and this daemon doesn't.
	db, err := sql.Open("sqlite", "file:"+backup)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at)
		 VALUES ('hello-stack', 'agent-02', 'ab-hello-stack-agent-02', 'none', 1, 'agentbox/b', 'main', 'abc', '/nowhere', 'ready', 1)`,
		`INSERT INTO chat_items (project, agent, id, position, data) VALUES ('hello-stack', 'agent-01', 'i1', 0, '{}')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	check, err = d.client.CheckMigration(ctx, api.MigrationCheckRequest{Backup: backup})
	if err != nil {
		t.Fatal(err)
	}
	problems := strings.Join(check.Problems, "\n")
	if check.OK || !strings.Contains(problems, "agent hello-stack/agent-02 isn't here") || !strings.Contains(problems, "hello-stack/agent-01's chat has 0 item(s) here, of the 1 it had") {
		t.Errorf("problems:\n%s", problems)
	}

	if _, err := d.client.CheckMigration(ctx, api.MigrationCheckRequest{Backup: "relative.db"}); err == nil {
		t.Error("a relative backup path was taken")
	}
}

// Recreate runs as a job, and says why it can't make a machine: here, an
// agent whose worktree isn't one.
func TestRecreateIsAJob(t *testing.T) {
	t.Parallel()
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	if _, err := d.client.Recreate(ctx, "hello-stack/agent-01", api.RecreateRequest{Home: "relative"}); err == nil {
		t.Error("a relative home was taken")
	}
	job, err := d.client.Recreate(ctx, "hello-stack/agent-01", api.RecreateRequest{Stopped: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.client.FollowJobLog(ctx, job.ID, io.Discard); err != nil {
		t.Fatal(err)
	}
	j, err := d.client.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Kind != "recreate" || j.Status != api.JobFailed || !strings.Contains(j.Error, "worktree") {
		t.Errorf("job = %+v", j)
	}
}
