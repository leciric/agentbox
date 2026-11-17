package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// A lead's tools run without asking (agent.leadSettings), so destroying an
// agent's uncommitted work is the daemon's to ask about: a card the user
// answers, and nothing destroyed when they refuse. Anything that keeps the
// work goes ahead without one.
func TestLeadDestroyingUncommittedWorkAsksTheUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	dirty := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	addAgent(t, d, repo, "hello-stack", "agent-02", "")
	if err := os.WriteFile(filepath.Join(dirty.Worktree, "server.mjs"), []byte("// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var asked []api.ChatPermission
	answer := false
	d.srv.approve = func(_ context.Context, _ string, req api.ChatPermission) (bool, error) {
		asked = append(asked, req)
		return answer, nil
	}

	// A clean agent, or a destroy that isn't forced, asks nothing.
	if err := d.srv.approveLosingWork(ctx, "hello-stack", []string{"agent-02"}); err != nil || len(asked) != 0 {
		t.Fatalf("a clean agent: err %v, asked %+v", err, asked)
	}
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	if _, err := lead.RetireProject(ctx, api.RetireRequest{How: api.RetireDestroy, Agents: []string{"agent-01"}}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 0 {
		t.Fatalf("a destroy without force, which leaves dirty agents alone, asked %+v", asked)
	}

	// Forced over uncommitted work: the user refuses, and it stays.
	_, err := lead.RetireProject(ctx, api.RetireRequest{How: api.RetireDestroy, Force: true, Agents: []string{"agent-01"}})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refused destroy: err %v", err)
	}
	if len(asked) != 1 || asked[0].Approval != "agent" || !strings.Contains(asked[0].Title, "agent-01") {
		t.Fatalf("asked %+v, want one agent card naming agent-01", asked)
	}
	if _, err := d.srv.store.Agent(ctx, "hello-stack", "agent-01"); err != nil {
		t.Fatalf("agent-01 is gone after the user refused: %v", err)
	}

	// Approved, it may go ahead.
	answer = true
	if err := d.srv.approveLosingWork(ctx, "hello-stack", []string{"agent-01"}); err != nil {
		t.Fatalf("an approved destroy: %v", err)
	}
	// A sweep naming nobody counts every agent's work.
	answer = false
	if err := d.srv.approveLosingWork(ctx, "hello-stack", nil); !errors.Is(err, errRefused) {
		t.Fatalf("a sweep over a dirty agent, refused: err %v", err)
	}
}
