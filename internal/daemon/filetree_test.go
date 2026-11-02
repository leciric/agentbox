package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

// The file tree's routes read the project's checkout for its lead, one folder
// at a time, refuse paths that leave it, and say what changed against HEAD.
func TestFileTreeRoutes(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	ref := "hello-stack"
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("SECRET=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := d.client.ListFolder(ctx, ref, "", false)
	if err != nil {
		t.Fatal(err)
	}
	names := func(l api.FileList) (out []string) {
		for _, e := range l.Entries {
			out = append(out, e.Name)
		}
		return out
	}
	if n := names(list); !slices.Contains(n, "README.md") || slices.Contains(n, ".env") || slices.Contains(n, ".git") {
		t.Errorf("ListFolder() = %v, want README.md without .env or .git", n)
	}
	list, err = d.client.ListFolder(ctx, ref, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(list.Entries, func(e api.FileEntry) bool { return e.Name == ".env" }); i < 0 || !list.Entries[i].Ignored {
		t.Errorf("ListFolder(ignored) = %+v, want .env marked ignored", list.Entries)
	}

	for _, bad := range []string{"../", "../../etc/passwd", "/etc/passwd", ".git/config"} {
		if _, err := d.client.ReadFile(ctx, ref, bad, false, false); err == nil || api.IsNotFound(err) {
			t.Errorf("ReadFile(%q) = %v, want it refused", bad, err)
		}
	}
	if _, err := d.client.ReadFile(ctx, ref, "nope.txt", false, false); !api.IsNotFound(err) {
		t.Errorf("ReadFile(missing) = %v, want a 404", err)
	}

	before, err := d.client.ReadFile(ctx, ref, "README.md", false, true)
	if err != nil || before.Text == "" || before.Status != "" || before.Base != "HEAD" {
		t.Fatalf("ReadFile(README) = %+v, %v", before, err)
	}

	// Live: the folder listed above is watched, and an edit is told.
	events, unsubscribe := d.srv.events.subscribe()
	defer unsubscribe()
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(before.Text+"more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var change api.FilesChange
	deadline := time.After(10 * time.Second)
	for !slices.Contains(change.Dirs, "") || !slices.Contains(change.Files, "README.md") {
		select {
		case ev := <-events:
			if ev.Type == api.EventFiles {
				var c api.FilesChange
				_ = json.Unmarshal(ev.Data, &c)
				change.Ref = c.Ref
				change.Dirs = append(change.Dirs, c.Dirs...)
				change.Files = append(change.Files, c.Files...)
			}
		case <-deadline:
			t.Fatalf("no files event for the edit; got %+v", change)
		}
	}
	if change.Ref != "hello-stack/lead" {
		t.Errorf("change.Ref = %q", change.Ref)
	}

	st, err := d.client.FileStatus(ctx, ref, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Changes["README.md"] != api.FileModified || st.Changes["new.txt"] != api.FileUntracked {
		t.Errorf("FileStatus() = %+v", st)
	}
	after, err := d.client.ReadFile(ctx, ref, filepath.Join(repo, "README.md"), false, true)
	if err != nil || after.Status != api.FileModified || after.BaseText != before.Text || !strings.HasSuffix(after.Text, "more\n") {
		t.Errorf("ReadFile(README, base) = %+v, %v", after, err)
	}

	// A change in a folder nobody listed isn't watched: the cached status
	// misses it until asked for fresh.
	if err := os.WriteFile(filepath.Join(repo, "scripts", "x.sh"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := d.client.FileStatus(ctx, ref, false); st.Changes["scripts/x.sh"] != "" {
		t.Logf("the cached status saw scripts/x.sh already: %v", st.Changes)
	}
	if st, err := d.client.FileStatus(ctx, ref, true); err != nil || st.Changes["scripts/x.sh"] != api.FileUntracked {
		t.Errorf("FileStatus(fresh) = %+v, %v; want scripts/x.sh untracked", st, err)
	}

	if _, err := d.client.ListFolder(ctx, "home", "", false); err == nil {
		t.Error("ListFolder(home) succeeded")
	}
}

// A worktree nobody asked about for watchIdle stops being watched, and its
// status is read again only when something moved or it grew old.
func TestFileWatchesExpireAndCacheStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	f := newFileWatches(func(api.FilesChange) {})
	now := time.Now()
	f.now = func() time.Time { return now }
	reads := 0
	compute := func() (api.FileStatus, error) {
		reads++
		return api.FileStatus{Base: "HEAD"}, nil
	}
	for range 3 {
		if _, err := f.status("p/a", root, compute); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 1 {
		t.Errorf("status read %d times, want once", reads)
	}
	f.mu.Lock()
	f.roots["p/a"].gen++
	f.mu.Unlock()
	_, _ = f.status("p/a", root, compute)
	now = now.Add(statusTTL)
	_, _ = f.status("p/a", root, compute)
	if reads != 3 {
		t.Errorf("status read %d times, want again after a change and after the TTL", reads)
	}

	f.mu.Lock()
	rw := f.roots["p/a"]
	f.mu.Unlock()
	f.expire(rw) // used just now: kept
	if f.roots["p/a"] == nil {
		t.Fatal("expired while in use")
	}
	now = now.Add(watchIdle)
	f.expire(rw)
	if f.roots["p/a"] != nil {
		t.Error("still watched after watchIdle")
	}
	f.closeAll()
}

// The viewer's SQLite route lists a database's tables and pages its rows,
// and a missing table is a 404.
func TestFileTreeSQLiteRoute(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(repo, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE items (n INTEGER); INSERT INTO items VALUES (1), (2), (3)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	file, err := d.client.ReadFile(ctx, "hello-stack", "app.db", false, true)
	if err != nil || !file.SQLite || !file.Binary {
		t.Fatalf("ReadFile(app.db) = %+v, %v", file, err)
	}
	page, err := d.client.SQLite(ctx, "hello-stack", "app.db", "items", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tables) != 1 || page.Total != 3 || len(page.Rows) != 1 || page.Rows[0][0] != float64(2) {
		t.Fatalf("SQLite(items) = %+v", page)
	}
	if _, err := d.client.SQLite(ctx, "hello-stack", "app.db", "nope", 0, 10); !api.IsNotFound(err) {
		t.Fatalf("SQLite(nope) = %v, want a 404", err)
	}
}
