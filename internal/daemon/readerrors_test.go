package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// On 21 Oct 2026 a user's machine ran out of file descriptors (ENFILE) for a
// while. Every git process and file open failed, and the daemon took those
// failures for answers: the cleanup pass set out to remove two agents with
// thousands of lines of work as "finished with no changes", and fifteen
// reviewed agents came back as work to review across eight projects. These
// tests put each of those paths through the same failures.

// readFaults are the failures each path is put through: the system's file
// table full, this process's, and git failing for a reason of its own.
var readFaults = []error{syscall.ENFILE, syscall.EMFILE, errors.New("fatal: unable to read index")}

// finishedLongAgo adds an agent to the project at root that finished two
// days ago, with work as given: committed, uncommitted, or none.
func finishedLongAgo(t *testing.T, d testDaemon, repo gitrepo.Repo, name string, commit, dirty bool) state.Agent {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	worktree := filepath.Join(t.TempDir(), name)
	branch := "agentbox/" + name
	if err := repo.AddWorktree(worktree, branch, "HEAD", "test"); err != nil {
		t.Fatal(err)
	}
	if commit || dirty {
		if err := os.WriteFile(filepath.Join(worktree, "work.txt"), []byte("work"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if commit {
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

// TestCleanupKeepsAgentsGitCantRead: with git failing in an agent's worktree
// or in the project's repository, nothing is removed, not even the agent that
// truly has nothing; once git reads again, only that one goes.
func TestCleanupKeepsAgentsGitCantRead(t *testing.T) {
	t.Parallel()
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
	idle := finishedLongAgo(t, d, repo, "agent-01", false, false)
	committed := finishedLongAgo(t, d, repo, "agent-02", true, false)
	uncommitted := finishedLongAgo(t, d, repo, "agent-03", false, true)
	all := []state.Agent{idle, committed, uncommitted}
	kept := func(when string, agents ...state.Agent) {
		t.Helper()
		for _, a := range agents {
			if _, err := d.srv.store.Agent(ctx, a.Project, a.Name); err != nil {
				t.Errorf("%s: %s was removed", when, a.Name)
			}
		}
	}

	for _, fault := range readFaults {
		// In each worktree: its diff and its status.
		var undo []func()
		for _, a := range all {
			undo = append(undo, gitrepo.FailUnder(a.Worktree, fault))
		}
		d.srv.removeFinishedAgents(ctx, time.Now())
		for _, u := range undo {
			u()
		}
		kept("with "+fault.Error()+" in the worktrees", all...)

		// In the repository: its origin, the branches, their reflogs.
		u := gitrepo.FailUnder(root, fault)
		d.srv.removeFinishedAgents(ctx, time.Now())
		u()
		kept("with "+fault.Error()+" in the repository", all...)
	}

	d.srv.removeFinishedAgents(ctx, time.Now())
	if _, err := d.srv.store.Agent(ctx, idle.Project, idle.Name); err == nil {
		t.Error("once git reads again, the agent that finished with nothing is still there")
	}
	kept("once git reads again", committed, uncommitted)
}

// TestRemovalRechecksRightBeforeActing: a removal the pass decided on is
// abandoned when the fresh read just before it fails.
func TestRemovalRechecksRightBeforeActing(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	root := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: root}); err != nil {
		t.Fatal(err)
	}
	repo, _ := gitrepo.Open(root)
	a := finishedLongAgo(t, d, repo, "agent-01", false, false)
	st := agent.Status{Agent: a}
	m := d.srv.manager(nil)
	want := "it finished with no changes and no pull request more than a day ago"
	if reason, err := d.srv.recheckRemoval(ctx, m, repo, st, nil, time.Now()); reason != want || err != nil {
		t.Fatalf("recheckRemoval = %q, %v; want %q", reason, err, want)
	}
	for _, fault := range readFaults {
		undo := gitrepo.FailUnder(a.Worktree, fault)
		reason, err := d.srv.recheckRemoval(ctx, m, repo, st, nil, time.Now())
		undo()
		if err == nil {
			t.Errorf("with %v, recheckRemoval = %q and no error", fault, reason)
		}
	}
	// Its record moved on since the pass listed it.
	if err := d.srv.store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentDestroying); err != nil {
		t.Fatal(err)
	}
	if reason, err := d.srv.recheckRemoval(ctx, m, repo, st, nil, time.Now()); reason != "" || err != nil {
		t.Errorf("an agent being destroyed already: recheckRemoval = %q, %v", reason, err)
	}
}

// TestDestroyRefusesAWorktreeItCantRead: without Force, a status git can't
// read is no proof of a clean worktree.
func TestDestroyRefusesAWorktreeItCantRead(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	root := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: root}); err != nil {
		t.Fatal(err)
	}
	repo, _ := gitrepo.Open(root)
	a := finishedLongAgo(t, d, repo, "agent-01", false, true)
	for _, fault := range readFaults {
		undo := gitrepo.FailUnder(a.Worktree, fault)
		err := d.srv.manager(nil).Destroy(ctx, a, agent.DestroyOptions{})
		undo()
		if err == nil {
			t.Fatalf("with %v, Destroy went ahead", fault)
		}
		if got, err := d.srv.store.Agent(ctx, a.Project, a.Name); err != nil || got.Status != state.AgentReady {
			t.Errorf("with %v, the agent is %+v (%v), want it as it was", fault, got.Status, err)
		}
	}
	if _, err := os.Stat(filepath.Join(a.Worktree, "work.txt")); err != nil {
		t.Errorf("the uncommitted work is gone: %v", err)
	}
}

// TestReadErrorsAddNothingToReview: what the user reviewed stays reviewed,
// what was merged stays off the list, and what is to review stays, while git
// can't read the project's branches; and with nothing known from before, a
// read error adds nothing.
func TestReadErrorsAddNothingToReview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newSlowGitHub(t, d, "[]", 0)
	root, agents := pullsProject(t, d, "agent-01", "agent-02", "agent-03")
	commitOn(t, agents["agent-01"], "reviewed.txt")
	merged := commitOn(t, agents["agent-02"], "merged.txt")
	commitOn(t, agents["agent-03"], "work.txt")
	for _, a := range agents {
		d.srv.record(ctx, finishedEvent(a, api.AgentChanges{Files: 1}, nil, "Done.", time.Now()))
	}
	// agent-02's pull request is older than the list page: only a lookup by
	// its commit knows it was merged.
	gh.setCommit(merged, "["+endedPR(7, merged, true)+"]")
	settled := func() bool { _, busy := d.srv.pulls.state("acme/hello-stack"); return !busy }
	readyNames(t, d)
	waitFor(t, "GitHub's answer", settled)
	if names := slices.Sorted(slices.Values(readyNames(t, d))); !slices.Equal(names, []string{"agent-01", "agent-03"}) {
		t.Fatalf("ready to review = %v, want agent-01 and agent-03", names)
	}
	items, err := d.client.ReadyToReview(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range items {
		if i.Agent == "agent-01" {
			if err := d.client.Review(ctx, i.Ref, api.ReviewRequest{Verdict: api.ReviewReviewed, Fingerprint: i.Fingerprint}); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := func(when string, ready, reviewed []string) {
		t.Helper()
		if names := readyNames(t, d); !slices.Equal(names, ready) {
			t.Errorf("%s: ready to review = %v, want %v", when, names, ready)
		}
		done, err := d.client.Reviewed(ctx, "hello-stack")
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, i := range done {
			names = append(names, i.Agent)
		}
		if !slices.Equal(names, reviewed) {
			t.Errorf("%s: reviewed = %v, want %v", when, names, reviewed)
		}
	}
	want("before anything failed", []string{"agent-03"}, []string{"agent-01"})

	for _, fault := range readFaults {
		// The repository: its origin and every branch.
		undo := gitrepo.FailUnder(root, fault)
		want("with "+fault.Error()+" in the repository", []string{"agent-03"}, []string{"agent-01"})
		undo()
		// The branches' reflogs only, as in the user's log.
		undo = gitrepo.FailUnder(filepath.Join(root, ".git", "worktrees"), fault)
		want("with "+fault.Error()+" in .git/worktrees", []string{"agent-03"}, []string{"agent-01"})
		undo()
		waitFor(t, "the refresh behind it", settled)
	}

	// With nothing read in full before, as after a restart, a read error
	// adds nothing.
	d.srv.mu.Lock()
	d.srv.reviewKnown = nil
	d.srv.mu.Unlock()
	undo := gitrepo.FailUnder(root, syscall.ENFILE)
	want("with ENFILE and nothing known", nil, nil)
	undo()
	want("once git reads again", []string{"agent-03"}, []string{"agent-01"})
}

// A tip that reads as none, where there was one, is a read that failed, not
// work that moved: the exact shape of every phantom of 21 Oct 2026.
func TestNoTipIsNoMove(t *testing.T) {
	t.Parallel()
	then := reviewMark{Finish: "f1", Tip: "b3073cf"}
	if then.movedTo(reviewMark{Finish: "f1"}) {
		t.Error("a tip that couldn't be read moved the work")
	}
	if !then.movedTo(reviewMark{Finish: "f1", Tip: "c0ffee1"}) {
		t.Error("a new commit didn't move the work")
	}
	if !then.movedTo(reviewMark{Finish: "f2"}) {
		t.Error("a new finish didn't move the work")
	}
}

// TestChangesAreUnknownOnReadErrors: measureChanges fails when git does,
// changesOf shows the last it read meanwhile, and a removed worktree has
// none, with no error.
func TestChangesAreUnknownOnReadErrors(t *testing.T) {
	t.Parallel()
	root := testutil.FixtureRepo(t, "hello-stack")
	repo, _ := gitrepo.Open(root)
	base, _ := repo.ResolveCommit("main")
	worktree := filepath.Join(t.TempDir(), "agent-01")
	if err := repo.AddWorktree(worktree, "agentbox/agent-01", base, "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{Worktree: worktree, BaseCommit: base}
	if c := changesOf(a); c.Files != 1 || !c.Dirty {
		t.Fatalf("changesOf = %+v, want one file, dirty", c)
	}
	for _, fault := range readFaults {
		undo := gitrepo.FailUnder(worktree, fault)
		if c, err := measureChanges(a); err == nil {
			t.Errorf("with %v, measureChanges = %+v and no error", fault, c)
		}
		if c := changesOf(a); c.Files != 1 || !c.Dirty {
			t.Errorf("with %v, changesOf = %+v, want the last read", fault, c)
		}
		undo()
	}
	if c, err := measureChanges(state.Agent{Worktree: filepath.Join(t.TempDir(), "gone"), BaseCommit: base}); err != nil || c != (api.AgentChanges{}) {
		t.Errorf("a removed worktree: measureChanges = %+v, %v", c, err)
	}
}
