package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// addAgent puts a ready agent with a worktree in the store, without needing a
// machine: the fleet reads git and the store, not incus.
func addAgent(t *testing.T, d testDaemon, repo, project, name, title string) state.Agent {
	t.Helper()
	ctx := context.Background()
	worktree := d.paths.Worktree(project, name)
	// Named after the work, as Create names it, so nothing here gets to
	// assume an agent's branch is the prefix and its name.
	branch := "agentbox/" + name
	if slug := agent.SlugForBranch(title); slug != "" {
		branch = "agentbox/" + slug
		for i := 2; testutil.Git(t, repo, "branch", "--list", branch) != ""; i++ {
			branch = fmt.Sprintf("agentbox/%s-%d", slug, i)
		}
	}
	commit := testutil.Git(t, repo, "rev-parse", "HEAD")
	testutil.Git(t, repo, "worktree", "add", "--quiet", "-b", branch, worktree, commit)
	a := state.Agent{
		Project: project, Name: name, Title: title, Instance: "ab-" + project + "-" + name,
		AI: "claude", Branch: branch, BaseRef: "main", BaseCommit: commit,
		Worktree: worktree, Status: state.AgentReady, CreatedAt: time.Now(),
		Interface: state.InterfaceChat,
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	return a
}

// The fleet is how you follow a project's agents without opening each one.
func TestFleetShowsWhatEachAgentHasChanged(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	addAgent(t, d, repo, "hello-stack", "agent-02", "")

	// agent-01 changes a file and leaves it uncommitted.
	if err := os.WriteFile(filepath.Join(a.Worktree, "server.mjs"), []byte("// changed\nconsole.log('hi');\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fleet, err := d.client.Fleet(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(fleet.Agents) != 2 {
		t.Fatalf("Fleet() has %d agents, want 2: %+v", len(fleet.Agents), fleet.Agents)
	}
	first := fleet.Agents[0]
	if first.Name != "agent-01" || first.Title != "Reminders page" {
		t.Errorf("first agent = %+v, want agent-01 with its title", first)
	}
	if first.Changes.Files != 1 || first.Changes.Insertions == 0 {
		t.Errorf("agent-01 changes = %+v, want one changed file with insertions", first.Changes)
	}
	if !first.Changes.Dirty {
		t.Error("agent-01 has uncommitted work, but Dirty is false")
	}
	if second := fleet.Agents[1]; second.Changes.Files != 0 || second.Changes.Dirty {
		t.Errorf("agent-02 changed nothing, but reports %+v", second.Changes)
	}
	// Without a shared GitHub token there are no pull requests, and that is
	// not an error: the fleet still works.
	if first.PR != nil || fleet.GitHub != "" || fleet.GitHubError != nil {
		t.Errorf("Fleet() = github %q, error %+v, pr %+v; want none without a token", fleet.GitHub, fleet.GitHubError, first.PR)
	}
}

// The project's chat is not one of its agents, so it never appears in the fleet.
func TestFleetLeavesOutTheProjectChat(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.manager(nil).EnsureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")

	fleet, err := d.client.Fleet(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(fleet.Agents) != 1 || fleet.Agents[0].Name != "agent-01" {
		t.Errorf("Fleet() = %+v, want only agent-01", fleet.Agents)
	}
}

// A project's media is one stream, labelled with the agent each item came from
// and what that agent was for, and filterable by agent and by kind.
func TestProjectMediaIsLabelledAndFilterable(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	addAgent(t, d, repo, "hello-stack", "agent-02", "Slow build")

	for _, m := range []state.Media{
		{ID: "m1", Project: "hello-stack", Agent: "agent-01", Kind: "note", Name: "what I did", Source: "agent", CreatedAt: time.Now().Add(-2 * time.Minute)},
		{ID: "m2", Project: "hello-stack", Agent: "agent-01", Kind: "screenshot", Name: "the page", Source: "agent", CreatedAt: time.Now().Add(-time.Minute)},
		{ID: "m3", Project: "hello-stack", Agent: "agent-02", Kind: "log", Name: "build output", Source: "agent", CreatedAt: time.Now()},
	} {
		if err := d.srv.store.AddMedia(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	all, err := d.client.ProjectMedia(ctx, "hello-stack", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("ProjectMedia() = %d items, want 3", len(all))
	}
	if all[0].ID != "m3" {
		t.Errorf("newest first: got %s", all[0].ID)
	}
	// Every item says which agent it came from, and what that agent was for.
	for _, item := range all {
		if item.AgentName == "" {
			t.Errorf("%s has no agent name", item.ID)
		}
	}
	if all[0].AgentName != "agent-02" || all[0].AgentTitle != "Slow build" {
		t.Errorf("m3 = %s / %q, want agent-02 / Slow build", all[0].AgentName, all[0].AgentTitle)
	}

	mine, err := d.client.ProjectMedia(ctx, "hello-stack", "agent-01", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 2 {
		t.Errorf("filtering by agent-01 = %d items, want 2", len(mine))
	}
	shots, err := d.client.ProjectMedia(ctx, "hello-stack", "", "screenshot")
	if err != nil {
		t.Fatal(err)
	}
	if len(shots) != 1 || shots[0].ID != "m2" {
		t.Errorf("filtering by kind = %+v, want only m2", shots)
	}
	both, err := d.client.ProjectMedia(ctx, "hello-stack", "agent-02", "screenshot")
	if err != nil || len(both) != 0 {
		t.Errorf("agent-02 has no screenshot, got %+v, %v", both, err)
	}
}

// Sharing a GitHub token checks it first, so a bad paste is refused here rather
// than failing inside an agent later.
func TestShareGitHubTokenChecksItFirst(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}

	if status, err := d.client.Auth(ctx); err != nil || status.GitHub {
		t.Errorf("Auth() = %+v, %v; want no GitHub token", status, err)
	}
	if _, err := d.client.SaveGitHubToken(ctx, "", ""); err == nil {
		t.Error("an empty token was accepted")
	}
	if creds.HasGitHubLogin() {
		t.Error("an empty token was stored")
	}
	// A token GitHub refuses is not stored either. This reaches GitHub, so it
	// only runs when the network is there; it must never store a bad token.
	if _, err := d.client.SaveGitHubToken(ctx, "", "ghp_definitely_not_a_real_token"); err == nil {
		t.Log("GitHub accepted a made-up token, which it should not; skipping")
	} else if !strings.Contains(err.Error(), "GitHub") {
		t.Logf("token check failed for another reason (offline?): %v", err)
	}
	if creds.HasGitHubLogin() {
		t.Error("a token GitHub refused was stored anyway")
	}
}

// An agent that has finished is holding a machine for nothing, and retiring it
// frees that without touching its branch: the branch is the work.
func TestRetireFreesFinishedAgentsAndKeepsTheirBranches(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	doneAgent := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	busyAgent := addAgent(t, d, repo, "hello-stack", "agent-02", "Still going")
	done, busy := doneAgent.Worktree, busyAgent.Worktree

	// agent-01 committed its work; agent-02 has changes it hasn't committed.
	if err := os.WriteFile(filepath.Join(done, "server.mjs"), []byte("// done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, done, "add", "-A")
	testutil.Git(t, done, "-c", "user.email=t@t", "-c", "user.name=T", "commit", "-q", "-m", "done")
	if err := os.WriteFile(filepath.Join(busy, "server.mjs"), []byte("// halfway\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A dry run says what would happen and changes nothing.
	dry, err := d.client.Retire(ctx, "hello-stack", api.RetireRequest{How: api.RetireDestroy, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Retired) != 1 || dry.Retired[0].Name != "agent-01" {
		t.Errorf("dry run would retire %+v, want only agent-01", dry.Retired)
	}
	if len(dry.Skipped) != 1 || dry.Skipped[0].Name != "agent-02" || !strings.Contains(dry.Skipped[0].Reason, "uncommitted") {
		t.Errorf("dry run skipped %+v, want agent-02 for its uncommitted work", dry.Skipped)
	}
	if _, err := d.srv.store.Agent(ctx, "hello-stack", "agent-01"); err != nil {
		t.Errorf("the dry run destroyed agent-01: %v", err)
	}

	// For real: agent-01 goes, agent-02 stays, and both branches survive.
	out, err := d.client.Retire(ctx, "hello-stack", api.RetireRequest{How: api.RetireDestroy})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Retired) != 1 || out.Retired[0].Branch != "agentbox/reminders-page" {
		t.Errorf("Retire() = %+v, want agent-01 with its branch named", out.Retired)
	}
	if _, err := d.srv.store.Agent(ctx, "hello-stack", "agent-01"); err == nil {
		t.Error("agent-01 is still there after being destroyed")
	}
	if _, err := d.srv.store.Agent(ctx, "hello-stack", "agent-02"); err != nil {
		t.Errorf("agent-02 was retired despite its uncommitted work: %v", err)
	}
	for _, branch := range []string{doneAgent.Branch, busyAgent.Branch} {
		if out := testutil.Git(t, repo, "branch", "--list", branch); out == "" {
			t.Errorf("branch %s was deleted: the work is gone", branch)
		}
	}
	// The committed work is still on the branch, though the agent has gone.
	if body := testutil.Git(t, repo, "show", doneAgent.Branch+":server.mjs"); !strings.Contains(body, "done") {
		t.Errorf("%s lost its commit: %q", doneAgent.Branch, body)
	}
}

// Naming an agent means you meant it, but uncommitted work still needs --force.
func TestRetireNamedAgentStillProtectsUncommittedWork(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Halfway")
	worktree := a.Worktree
	if err := os.WriteFile(filepath.Join(worktree, "server.mjs"), []byte("// halfway\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := d.client.Retire(ctx, "hello-stack", api.RetireRequest{How: api.RetireDestroy, Agents: []string{"agent-01"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Retired) != 0 || len(out.Skipped) != 1 {
		t.Errorf("Retire(agent-01) = %+v / %+v, want it skipped", out.Retired, out.Skipped)
	}
	out, err = d.client.Retire(ctx, "hello-stack", api.RetireRequest{How: api.RetireDestroy, Agents: []string{"agent-01"}, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Retired) != 1 {
		t.Errorf("Retire(--force) = %+v, want agent-01 retired", out)
	}
	if testutil.Git(t, repo, "branch", "--list", a.Branch) == "" {
		t.Error("--force deleted the branch; it should only discard the uncommitted worktree")
	}
}
