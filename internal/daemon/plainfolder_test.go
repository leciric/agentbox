package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A folder that was never a repository is refused with what adding it would
// do and leave out, and made one with Init: its files committed, its
// node_modules not, and with no remote, said so.
func TestAddProjectFromAPlainFolder(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "Site")
	writeFile(t, dir, "index.html", "<h1>hi</h1>\n")
	writeFile(t, dir, "node_modules/x/index.js", "module.exports = 1\n")

	_, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: dir})
	if !api.HasReason(err, api.ErrorNotARepository) || !strings.Contains(err.Error(), "runs git init there") {
		t.Fatalf("AddProject(plain folder) = %v, want %s", err, api.ErrorNotARepository)
	}
	var se *api.StatusError
	if !errors.As(err, &se) || len(se.Skipped) != 1 || se.Skipped[0].Path != "node_modules" || se.Skipped[0].Reason != "dependencies" {
		t.Errorf("Skipped = %+v", se)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Fatal("refusing made a repository anyway")
	}

	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: dir, Init: true})
	if err != nil || p.Root != dir || p.Branch != "main" || !p.NoRemote {
		t.Fatalf("AddProject(init) = %+v, %v", p, err)
	}
	if got := testutil.Git(t, dir, "ls-tree", "-r", "--name-only", "HEAD"); got != "index.html" {
		t.Errorf("committed %q", got)
	}

	// Once it has a remote, it says so.
	testutil.Git(t, dir, "remote", "add", "origin", "https://example.com/site.git")
	if projects, _ := d.client.Projects(ctx); len(projects) != 1 || projects[0].NoRemote {
		t.Errorf("with an origin, Projects() = %+v", projects)
	}
}

// With no remote, an agent's branch lands by merging it here.
func TestMergeAgentIntoItsBaseWithNoRemote(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "notes")
	writeFile(t, dir, "todo.md", "- ship\n")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: dir, Init: true}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "agent-01")
	testutil.Git(t, dir, "worktree", "add", "-q", "-b", "agentbox/done", wt)
	a := state.Agent{Project: "notes", Name: "agent-01", Instance: "ab-notes-agent-01", AI: "claude", Branch: "agentbox/done", BaseRef: "main",
		Worktree: wt, Status: state.AgentReady, CreatedAt: time.Now()}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}

	writeFile(t, wt, "done.md", "- shipped\n")
	if _, err := d.client.MergeAgent(ctx, a.Ref(), api.MergeAgentRequest{}); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("MergeAgent(uncommitted) = %v", err)
	}
	testutil.Git(t, wt, "add", ".")
	testutil.Git(t, wt, "commit", "-q", "-m", "Ship it")
	m, err := d.client.MergeAgent(ctx, a.Ref(), api.MergeAgentRequest{})
	if err != nil || m.Into != "main" || !m.FastForward {
		t.Fatalf("MergeAgent() = %+v, %v", m, err)
	}
	if got := testutil.Git(t, dir, "ls-files"); got != "done.md\ntodo.md" {
		t.Errorf("main's checkout has %q", got)
	}

	// A conflict is refused with its code, and nothing moves.
	writeFile(t, wt, "todo.md", "- agent\n")
	testutil.Git(t, wt, "commit", "-qam", "agent")
	writeFile(t, dir, "todo.md", "- user\n")
	testutil.Git(t, dir, "commit", "-qam", "user")
	if _, err := d.client.MergeAgent(ctx, a.Ref(), api.MergeAgentRequest{}); !api.HasReason(err, api.ErrorMergeConflict) || !strings.Contains(err.Error(), "todo.md") {
		t.Errorf("MergeAgent(conflict) = %v", err)
	}
}

// Init writes to the folder only once nothing else can refuse the project:
// a refusal leaves no .git behind.
func TestInitRefusesBeforeTouchingTheFolder(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	taken := filepath.Join(t.TempDir(), "taken")
	writeFile(t, taken, "a.txt", "a\n")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: taken, Init: true}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "plain")
	writeFile(t, dir, "b.txt", "b\n")
	for name, req := range map[string]api.AddProjectRequest{
		"an account that isn't there": {Path: dir, Init: true, ClaudeAccount: "nobody"},
		"a name that's taken":         {Path: dir, Init: true, Name: "taken"},
	} {
		if _, err := d.client.AddProject(ctx, req); err == nil {
			t.Errorf("%s: added", name)
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			t.Fatalf("%s: refused, but the folder was made a repository first", name)
		}
	}
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: "/", Init: true}); err == nil || !strings.Contains(err.Error(), "far more than one project") {
		t.Errorf("AddProject(/, init) = %v", err)
	}
}
