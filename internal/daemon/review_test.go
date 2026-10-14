package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/testutil"
)

// "Ready to review" lists the agents that finished, newest first, and a
// verdict takes one off until its work changes.
func TestReadyToReviewListsFinishedAgentsUntilReviewed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	older := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	newer := addAgent(t, d, repo, "hello-stack", "agent-02", "Dark mode")
	working := addAgent(t, d, repo, "hello-stack", "agent-03", "Still at it")
	now := time.Now()
	d.srv.record(ctx, createdEvent(working, "Do the thing", now))
	d.srv.record(ctx, finishedEvent(older, api.AgentChanges{}, &api.PullRequest{Number: 7, State: "open"}, "Paginated the reminders list.", now.Add(-time.Hour)))
	d.srv.record(ctx, finishedEvent(newer, api.AgentChanges{}, nil, "Added a dark theme.", now.Add(-time.Minute)))

	list := readyToReview(t, d, "hello-stack")
	if len(list) != 2 || list[0].Agent != "agent-02" || list[1].Agent != "agent-01" {
		t.Fatalf("want the two finished agents, newest first; got %+v", list)
	}
	if got := list[1]; got.Title != "Reminders page" || got.Branch != older.Branch || got.PR == nil || got.PR.Number != 7 || got.Fingerprint == "" {
		t.Errorf("a row lost what it shows: %+v", got)
	}
	// A finish with nothing to show isn't work to review, and neither is one
	// followed by a turn that ended on a question.
	idle := addAgent(t, d, repo, "hello-stack", "agent-04", "Answer a question")
	ev := finishedEvent(idle, api.AgentChanges{}, nil, "It's in server.mjs.", now)
	ev.Outcome = api.AgentOutcomeIdle
	d.srv.record(ctx, ev)
	asking := addAgent(t, d, repo, "hello-stack", "agent-05", "Asks after")
	d.srv.record(ctx, finishedEvent(asking, api.AgentChanges{}, nil, "Done.", now.Add(-2*time.Minute)))
	d.srv.record(ctx, api.AgentEvent{Project: asking.Project, Agent: asking.Name, Ref: asking.Ref(), Kind: api.AgentAsking, Summary: "Launch or stop?", At: now.Add(-time.Second)})
	if list := readyToReview(t, d, "hello-stack"); len(list) != 2 {
		t.Fatalf("an idle finish or one followed by a question is listed: %+v", list)
	}

	if all := readyToReview(t, d, ""); len(all) != 2 {
		t.Errorf("the AgentBox-wide list has %d agents, want 2", len(all))
	}

	// Reviewed and dismissed both take an agent off.
	if err := d.client.Review(ctx, newer.Ref(), api.ReviewRequest{Verdict: api.ReviewReviewed, Fingerprint: list[0].Fingerprint}); err != nil {
		t.Fatal(err)
	}
	if err := d.client.Review(ctx, older.Ref(), api.ReviewRequest{Verdict: api.ReviewDismissed}); err != nil {
		t.Fatal(err)
	}
	if list := readyToReview(t, d, "hello-stack"); len(list) != 0 {
		t.Fatalf("reviewed and dismissed agents are still listed: %+v", list)
	}

	// New commits on its branch bring one back, and so does finishing again.
	if err := os.WriteFile(filepath.Join(newer.Worktree, "theme.css"), []byte("body{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, newer.Worktree, "add", ".")
	testutil.Git(t, newer.Worktree, "commit", "--quiet", "-m", "Another go")
	d.srv.record(ctx, finishedEvent(older, api.AgentChanges{}, nil, "Fixed the review comments.", now))
	list = readyToReview(t, d, "hello-stack")
	if len(list) != 2 || list[0].Agent != "agent-01" {
		t.Fatalf("changed agents didn't come back, the latest finish first: %+v", list)
	}

	if err := d.client.Review(ctx, newer.Ref(), api.ReviewRequest{Verdict: "lgtm"}); err == nil {
		t.Error("a verdict that is neither reviewed nor dismissed was taken")
	}
	if err := d.client.Review(ctx, working.Ref(), api.ReviewRequest{Verdict: api.ReviewReviewed}); err == nil {
		t.Error("an agent that hasn't finished was reviewed")
	}

	// The verdict goes with the agent: its name may go to another.
	if err := d.client.Review(ctx, older.Ref(), api.ReviewRequest{Verdict: api.ReviewReviewed}); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.RemoveAgent(ctx, older.Project, older.Name); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.srv.store.AgentReviewOf(ctx, older.Project, older.Name); err != nil || ok {
		t.Errorf("the verdict outlived its agent (ok=%v, err=%v)", ok, err)
	}
}

func readyToReview(t *testing.T, d testDaemon, project string) []api.ReviewItem {
	t.Helper()
	out, err := d.client.ReadyToReview(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// What brings an agent back after a verdict, and what doesn't.
func TestReviewMarkMovedTo(t *testing.T) {
	t.Parallel()
	then := reviewMark{Finish: "f1", Tip: "abc", PR: 4, PRHead: "abc", Failing: true, Conflict: true}
	for _, tc := range []struct {
		name  string
		now   reviewMark
		moved bool
	}{
		{"nothing changed", then, false},
		{"finished again", reviewMark{Finish: "f2", Tip: "abc", PR: 4, PRHead: "abc", Failing: true, Conflict: true}, true},
		{"new commits", reviewMark{Finish: "f1", Tip: "def", PR: 4, PRHead: "abc", Failing: true, Conflict: true}, true},
		{"pushed to its pull request", reviewMark{Finish: "f1", Tip: "abc", PR: 4, PRHead: "def", Failing: true, Conflict: true}, true},
		{"checks fixed", reviewMark{Finish: "f1", Tip: "abc", PR: 4, PRHead: "abc", Conflict: true}, true},
		{"conflict resolved", reviewMark{Finish: "f1", Tip: "abc", PR: 4, PRHead: "abc", Failing: true}, true},
		{"pull request not read yet", reviewMark{Finish: "f1", Tip: "abc"}, false},
	} {
		if got := then.movedTo(tc.now); got != tc.moved {
			t.Errorf("%s: movedTo = %v, want %v", tc.name, got, tc.moved)
		}
	}
	healthy := reviewMark{Finish: "f1", PR: 4, PRHead: "abc"}
	if healthy.movedTo(reviewMark{Finish: "f1", PR: 4, PRHead: "abc", Failing: true}) {
		t.Error("checks that started failing brought it back")
	}
}
