package daemon

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
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

	// Reviewed and dismissed both take an agent off. (Uncommitted work keeps
	// the reviewed one's machine, which reviewing would otherwise free.)
	holdMachine(t, newer)
	if err := d.client.Review(ctx, newer.Ref(), api.ReviewRequest{Verdict: api.ReviewReviewed, Fingerprint: list[0].Fingerprint}); err != nil {
		t.Fatal(err)
	}
	if err := d.client.Review(ctx, older.Ref(), api.ReviewRequest{Verdict: api.ReviewDismissed}); err != nil {
		t.Fatal(err)
	}
	if list := readyToReview(t, d, "hello-stack"); len(list) != 0 {
		t.Fatalf("reviewed and dismissed agents are still listed: %+v", list)
	}
	// They move to Reviewed, the latest verdict first.
	done, err := d.client.Reviewed(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 || done[0].Agent != "agent-01" || done[0].Verdict != api.ReviewDismissed || done[1].Verdict != api.ReviewReviewed || done[0].ReviewedAt == nil {
		t.Fatalf("Reviewed doesn't list both verdicts, the latest first: %+v", done)
	}
	if all, err := d.client.Reviewed(ctx, ""); err != nil || len(all) != 2 {
		t.Errorf("the AgentBox-wide Reviewed has %d agents (%v), want 2", len(all), err)
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
	if done, _ := d.client.Reviewed(ctx, "hello-stack"); len(done) != 0 {
		t.Errorf("changed agents are still in Reviewed: %+v", done)
	}

	if err := d.client.Review(ctx, newer.Ref(), api.ReviewRequest{Verdict: "lgtm"}); err == nil {
		t.Error("a verdict that is neither reviewed nor dismissed was taken")
	}
	if err := d.client.Review(ctx, working.Ref(), api.ReviewRequest{Verdict: api.ReviewReviewed}); err == nil {
		t.Error("an agent that hasn't finished was reviewed")
	}

	// The verdict goes with the agent: its name may go to another.
	holdMachine(t, older)
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

// Both lists are read a page at a time: the cursor continues from where the
// last page ended even when an item left the list meanwhile, and the total
// counts the whole list.
func TestReviewListsArePaged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var agents []state.Agent
	for i := range 5 {
		a := addAgent(t, d, repo, "hello-stack", fmt.Sprintf("agent-%02d", i+1), fmt.Sprintf("Task %d", i+1))
		d.srv.record(ctx, finishedEvent(a, api.AgentChanges{}, nil, "Done.", now.Add(-time.Duration(i)*time.Minute)))
		agents = append(agents, a)
	}
	// agent-01 finished last, so it's first.
	page, err := d.client.ReviewPage(ctx, "", false, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 || len(page.Items) != 2 || page.Items[0].Agent != "agent-01" || page.Items[1].Agent != "agent-02" || page.Next == "" {
		t.Fatalf("first page = %+v", page)
	}
	// agent-02, where the page ended, is reviewed before the next is read.
	holdMachine(t, agents[1])
	if err := d.client.Review(ctx, agents[1].Ref(), api.ReviewRequest{Verdict: api.ReviewReviewed}); err != nil {
		t.Fatal(err)
	}
	next, err := d.client.ReviewPage(ctx, "", false, 2, page.Next)
	if err != nil {
		t.Fatal(err)
	}
	if next.Total != 4 || len(next.Items) != 2 || next.Items[0].Agent != "agent-03" || next.Items[1].Agent != "agent-04" || next.Next == "" {
		t.Fatalf("second page = %+v, want agent-03 and agent-04 of 4", next)
	}
	last, err := d.client.ReviewPage(ctx, "", false, 2, next.Next)
	if err != nil || len(last.Items) != 1 || last.Items[0].Agent != "agent-05" || last.Next != "" {
		t.Fatalf("last page = %+v, %v", last, err)
	}
	done, err := d.client.ReviewPage(ctx, "hello-stack", true, 1, "")
	if err != nil || done.Total != 1 || len(done.Items) != 1 || done.Items[0].Agent != "agent-02" || done.Next != "" {
		t.Fatalf("reviewed page = %+v, %v", done, err)
	}
	if _, err := d.client.ReviewPage(ctx, "", false, 2, "nonsense"); err == nil {
		t.Error("a cursor that isn't one of ours was taken")
	}
}

// An empty list is "items":[], never null: the app maps a page's items
// without a guard, and a null crashed it with "Cannot read properties of
// null (reading 'map')".
func TestEmptyReviewPageHasItems(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"?limit=20", "?limit=20&reviewed=1", "?limit=1&cursor=1:p/agent-01"} {
		w := httptest.NewRecorder()
		if err := reviewList(w, httptest.NewRequest("GET", "/v1/review"+query, nil), nil); err != nil {
			t.Fatal(err)
		}
		if body := w.Body.String(); !strings.Contains(body, `"items":[]`) {
			t.Errorf("%s: %s, want \"items\":[]", query, body)
		}
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

// Request changes, from the review: the agent is told what to change the way
// tell_agent tells it, the work it showed is recorded as turned down, and
// while its chat is on the request the app is told what was asked.
func TestRequestChangesTellsTheAgentAndRecordsIt(t *testing.T) {
	t.Parallel()
	w := newWakeTest(t)
	ctx := context.Background()
	a, err := w.srv.store.Agent(ctx, "hello-stack", "agent-02")
	if err != nil {
		t.Fatal(err)
	}
	w.srv.record(ctx, finishedEvent(a, api.AgentChanges{}, nil, "Added the settings page.", time.Now()))
	list := readyToReview(t, w.testDaemon, "hello-stack")
	if len(list) != 1 {
		t.Fatalf("want agent-02 ready to review, got %+v", list)
	}
	if _, err := w.client.RequestChanges(ctx, a.Ref(), api.RequestChangesRequest{Text: "  "}); err == nil {
		t.Error("an empty request was sent")
	}
	res, err := w.client.RequestChanges(ctx, a.Ref(), api.RequestChangesRequest{Text: "Move the save button up.", Fingerprint: list[0].Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "user" {
		t.Errorf("the request is a %s item, want the user's message", res.Kind)
	}
	waitFor(t, "the request to reach the AI tool", func() bool { return w.tool.got("Move the save button up.") })
	r, ok, err := w.srv.store.AgentReviewOf(ctx, "hello-stack", "agent-02")
	if err != nil || !ok || r.Verdict != api.ReviewChanges || r.Note != "Move the save button up." || r.Fingerprint != list[0].Fingerprint {
		t.Fatalf("the verdict = %+v (%v, %v), want changes with what was asked", r, ok, err)
	}
	if got := w.srv.changesRequested(ctx, "hello-stack", "agent-02", "running"); got != "Move the save button up." {
		t.Errorf("a working agent's request = %q", got)
	}
	if got := w.srv.changesRequested(ctx, "hello-stack", "agent-02", "ready"); got != "" {
		t.Errorf("an agent whose turn ended still has the request %q", got)
	}
	if _, err := w.client.RequestChanges(ctx, "hello-stack/agent-99", api.RequestChangesRequest{Text: "x"}); err == nil {
		t.Error("asked an agent that doesn't exist")
	}
}

// holdMachine leaves uncommitted work in a's worktree, which keeps its
// machine through a review.
func holdMachine(t *testing.T, a state.Agent) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(a.Worktree, "wip.txt"), []byte("not committed"), 0o644); err != nil {
		t.Fatal(err)
	}
}
