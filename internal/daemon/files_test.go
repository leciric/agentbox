package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/testutil"
)

// The cache remembers a worktree's listing for its TTL, and refreshes once
// that has passed — the unit the HTTP tests below don't exercise, since they
// each only call it once.
func TestFilesCacheRefreshesAfterTTL(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testutil.Git(t, root, "init", "-q", "-b", "main")
	c := newFilesCache()
	now := time.Now()
	c.now = func() time.Time { return now }

	if err := os.WriteFile(filepath.Join(root, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, err := c.list("proj/agent", root)
	if err != nil || !slices.Contains(got, "a.txt") {
		t.Fatalf("list() = %q, %v", got, err)
	}

	// A file made just after is invisible within the TTL...
	if err := os.WriteFile(filepath.Join(root, "b.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, _ = c.list("proj/agent", root)
	if slices.Contains(got, "b.txt") {
		t.Errorf("list() saw b.txt before the cache entry expired")
	}

	// ...and appears once the TTL has passed.
	now = now.Add(filesTTL)
	got, _, _ = c.list("proj/agent", root)
	if !slices.Contains(got, "b.txt") {
		t.Errorf("list() = %q after the TTL, want it to include b.txt", got)
	}
}

// A freshly made agent's worktree is a real git checkout on disk, so the
// files route reads its tracked files, plus anything untracked added since.
func TestFilesListsAgentWorktree(t *testing.T) {
	t.Setenv("INCUS_INSTANCES", `[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]`)
	d := startTestDaemon(t, t.TempDir(), recordingIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	job, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", Name: "agent-01", AI: "none"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "agent-01 to be created", func() bool {
		j, err := d.client.Job(ctx, job.ID)
		return err == nil && j.Done()
	})
	if j, _ := d.client.Job(ctx, job.ID); j.Status != api.JobSucceeded {
		t.Fatalf("creating agent-01 = %s: %s", j.Status, j.Error)
	}

	a, err := d.client.Agent(ctx, "hello-stack/agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Worktree, "notes.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := d.client.Files(ctx, "hello-stack/agent-01")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"README.md", "message.txt", "notes.md"} {
		if !slices.Contains(files.Files, want) {
			t.Errorf("Files() = %q, missing %q", files.Files, want)
		}
	}
	if slices.Contains(files.Files, ".env") {
		t.Errorf("Files() = %q, should not contain the gitignored .env", files.Files)
	}
	if files.Truncated {
		t.Error("Files() truncated on a handful of files")
	}

	if _, err := d.client.Files(ctx, "no-such-project/agent-01"); !api.IsNotFound(err) {
		t.Errorf("Files() for an unknown project = %v, want a 404", err)
	}
}

// A project you have never written to has no worktree of its own yet, so its
// files are the main checkout's — the same thing its first message stands on.
func TestFilesFallsBackToProjectRootBeforeLeadStarts(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}

	files, err := d.client.Files(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(files.Files, "README.md") {
		t.Errorf("Files() before the lead started = %q, missing README.md", files.Files)
	}
}

// Once the lead has its own worktree, its files come from there, not the main
// checkout, the same way its chat reads that branch's tip.
func TestFilesUsesLeadWorktreeOnceStarted(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	a, err := d.srv.manager(nil).EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Worktree, "lead-only.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := d.client.Files(ctx, "hello-stack/lead")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(files.Files, "lead-only.md") {
		t.Errorf("Files() for the started lead = %q, missing lead-only.md", files.Files)
	}
}
