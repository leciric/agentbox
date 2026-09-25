package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

func TestRemoveReason(t *testing.T) {
	now := time.Now()
	created := now.Add(-72 * time.Hour)
	ago := func(d time.Duration) *time.Time { at := now.Add(-d); return &at }
	pr := func(state string, updated time.Duration) *api.PullRequest {
		return &api.PullRequest{Number: 7, State: state, UpdatedAt: ago(updated)}
	}
	for _, tc := range []struct {
		name   string
		facts  cleanupFacts
		remove bool
	}{
		{"merged pull request", cleanupFacts{Changed: true, PR: pr("merged", time.Hour)}, true},
		{"closed pull request", cleanupFacts{Changed: true, PR: pr("closed", time.Hour)}, true},
		{"open pull request", cleanupFacts{Changed: true, PR: pr("open", time.Hour)}, false},
		{"merged, but still working", cleanupFacts{Busy: true, PR: pr("merged", time.Hour)}, false},
		{"merged, but uncommitted work", cleanupFacts{Dirty: true, PR: pr("merged", time.Hour)}, false},
		{"merged, but unpushed commits", cleanupFacts{Unpushed: true, PR: pr("merged", time.Hour)}, false},
		{"merged, on its own command line", cleanupFacts{Attended: true, PR: pr("merged", time.Hour)}, false},
		{"a pull request from before the agent", cleanupFacts{PR: pr("merged", 100*time.Hour)}, false},
		{"finished with nothing, two days ago", cleanupFacts{FinishedAt: ago(48 * time.Hour)}, true},
		{"finished with nothing, an hour ago", cleanupFacts{FinishedAt: ago(time.Hour)}, false},
		{"finished two days ago, talked to since", cleanupFacts{FinishedAt: ago(48 * time.Hour), LastActive: ago(time.Hour)}, false},
		{"finished with changes and no pull request", cleanupFacts{Changed: true, FinishedAt: ago(48 * time.Hour)}, false},
		{"never finished", cleanupFacts{LastActive: ago(48 * time.Hour)}, false},
	} {
		tc.facts.CreatedAt = created
		if got := removeReason(tc.facts, now); (got != "") != tc.remove {
			t.Errorf("%s: removeReason() = %q, want removed %v", tc.name, got, tc.remove)
		}
	}
}

// One pass removes an agent that finished two days ago having changed
// nothing, branch and all, and keeps one that finished just as long ago with
// a commit that exists nowhere else.
func TestRemoveFinishedAgents(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), `case "$1" in
  list) echo '[]' ;;
esac`)
	ctx := context.Background()
	root := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: root}); err != nil {
		t.Fatal(err)
	}
	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	add := func(name string, commit bool) state.Agent {
		worktree := filepath.Join(t.TempDir(), name)
		branch := "agentbox/" + name
		if err := repo.AddWorktree(worktree, branch, "HEAD"); err != nil {
			t.Fatal(err)
		}
		if commit {
			if err := os.WriteFile(filepath.Join(worktree, "work.txt"), []byte("work"), 0o644); err != nil {
				t.Fatal(err)
			}
			testutil.Git(t, worktree, "add", "work.txt")
			testutil.Git(t, worktree, "commit", "--quiet", "-m", "work")
		}
		base, _ := repo.ResolveCommit("main")
		a := state.Agent{
			Project: "hello-stack", Name: name, Instance: "ab-hello-stack-" + name, AI: "none",
			Branch: branch, BaseRef: "main", BaseCommit: base, Worktree: worktree,
			Status: state.AgentReady, CreatedAt: now.Add(-72 * time.Hour),
		}
		if err := d.srv.store.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		finished := now.Add(-48 * time.Hour)
		data, _ := json.Marshal(api.AgentEvent{Agent: name, Kind: api.AgentFinished, At: finished})
		if err := d.srv.store.AddAgentEvent(ctx, state.AgentEvent{ID: name + "-finished", Project: a.Project, Agent: name, CreatedAt: finished, Data: data}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	idle := add("agent-01", false)
	worked := add("agent-02", true)

	d.srv.removeFinishedAgents(ctx, now)

	if _, err := d.srv.store.Agent(ctx, idle.Project, idle.Name); err == nil {
		t.Error("the agent that finished with nothing is still there")
	}
	if repo.BranchExists(idle.Branch) {
		t.Error("its branch, merged by definition, is still there")
	}
	if _, err := d.srv.store.Agent(ctx, worked.Project, worked.Name); err != nil {
		t.Errorf("the agent with an unpushed commit was removed: %v", err)
	}
	if !repo.BranchExists(worked.Branch) {
		t.Error("the branch with an unpushed commit was deleted")
	}
}
