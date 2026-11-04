package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/state"
)

// mergeWhenGreenProject is a project with agent-01's pull request #9 open on
// the fake GitHub, its checks as given, and merge when green turned on.
func mergeWhenGreenProject(t *testing.T, checks string) (testDaemon, *fakeGraphQL, string) {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	gh.rest = true
	recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	head := commitOn(t, agents["agent-01"], "reminders.txt")
	gh.set(9, head, "OPEN", "MERGEABLE", checks, "", failingFor(checks)...)
	on := true
	if _, err := d.client.UpdateSettings(context.Background(), api.UpdateSettingsRequest{MergeWhenGreen: &on}); err != nil {
		t.Fatal(err)
	}
	return d, gh, head
}

func failingFor(checks string) []string {
	if checks == "FAILURE" {
		return []string{"test"}
	}
	return nil
}

const seenWork = `{"f":"finish-1","p":9}`

func resolve(t *testing.T, d testDaemon) {
	t.Helper()
	if _, err := d.client.MergeWhenGreen(context.Background(), "hello-stack", 9, api.MergeWhenGreenRequest{Method: "squash", Agent: "agent-01", Fingerprint: seenWork}); err != nil {
		t.Fatal(err)
	}
}

func markOf9(t *testing.T, d testDaemon) (state.MergeWhenGreen, bool) {
	t.Helper()
	m, ok, err := d.srv.store.MergeWhenGreenOf(context.Background(), "hello-stack", 9)
	if err != nil {
		t.Fatal(err)
	}
	return m, ok
}

func verdictOf(t *testing.T, d testDaemon) (state.AgentReview, bool) {
	t.Helper()
	r, ok, err := d.srv.store.AgentReviewOf(context.Background(), "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	return r, ok
}

func (f *fakeGraphQL) merged() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.merges...)
}

// Resolved while its checks run: reviewed at once, its machine kept, and
// merged with the method picked the first time the watch sees it green.
func TestMergeWhenGreenMergesOnceGreen(t *testing.T) {
	t.Parallel()
	d, gh, head := mergeWhenGreenProject(t, "PENDING")
	resolve(t, d)

	if r, ok := verdictOf(t, d); !ok || r.Verdict != api.ReviewReviewed || r.Fingerprint != seenWork {
		t.Fatalf("verdict = %+v, %v; want reviewed, against the work seen", r, ok)
	}
	if _, err := d.srv.store.Agent(context.Background(), "hello-stack", "agent-01"); err != nil {
		t.Fatalf("resolving freed the agent before the merge: %v", err)
	}

	pollNow(d)
	if m := gh.merged(); len(m) != 0 {
		t.Fatalf("merged while its checks ran: %v", m)
	}
	if m, ok := markOf9(t, d); !ok || !m.Pending() {
		t.Fatalf("mark = %+v, %v; want it still pending", m, ok)
	}

	gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if m := gh.merged(); len(m) != 1 || m[0] != "9:squash" {
		t.Fatalf("merges = %v, want #9 squashed", m)
	}
	if _, ok := markOf9(t, d); ok {
		t.Fatal("the mark outlived the merge")
	}
}

// Green already: the mark merges on the watch's next look, the one resolving
// starts, without waiting for the project to be due.
func TestMergeWhenGreenMergesAtOnceWhenGreen(t *testing.T) {
	t.Parallel()
	d, gh, _ := mergeWhenGreenProject(t, "")
	resolve(t, d)
	d.srv.pollPRWatch(context.Background()) // what resolving starts in the background
	if m := gh.merged(); len(m) != 1 || m[0] != "9:squash" {
		t.Fatalf("merges = %v, want #9 at once, with no checks to wait for", m)
	}
}

// Checks failing don't give the mark up: the watch tells the agent to fix
// them, the item stays out of the review saying so, and the fix, new commits
// the user never saw, is merged once green.
func TestMergeWhenGreenRedThenFixMerges(t *testing.T) {
	t.Parallel()
	d, gh, head := mergeWhenGreenProject(t, "PENDING")
	rec := recordPRWatch(d)
	agent := agent01(t, d)
	d.srv.record(context.Background(), finishedEvent(agent, api.AgentChanges{}, &api.PullRequest{Number: 9, State: "open"}, "Reminders page.", time.Now()))
	resolve(t, d)

	gh.set(9, head, "OPEN", "MERGEABLE", "FAILURE", "", "test")
	pollNow(d)
	if m := gh.merged(); len(m) != 0 {
		t.Fatalf("merged with failing checks: %v", m)
	}
	m := pendingMark(t, d, api.MergeWaitFailing)
	if m.Detail != "test" {
		t.Errorf("detail = %q, want the failing check", m.Detail)
	}
	if said, _ := rec.take(); len(said) != 1 || !strings.Contains(said[0], "`test`") {
		t.Fatalf("the agent was told %q, want to fix the failing check", said)
	}
	if r, ok := verdictOf(t, d); !ok || r.Verdict != api.ReviewReviewed {
		t.Fatalf("the verdict was taken back: %+v, %v", r, ok)
	}

	// The agent pushes a fix, and finishes again: new work, but still out
	// of the review, shown as merging when green.
	fix := commitOn(t, agent, "fix.txt")
	d.srv.record(context.Background(), finishedEvent(agent, api.AgentChanges{}, &api.PullRequest{Number: 9, State: "open"}, "Fixed the test.", time.Now()))
	gh.set(9, fix, "OPEN", "MERGEABLE", "PENDING", "")
	pollNow(d)
	pendingMark(t, d, api.MergeWaitChecks)
	if ready := readyToReview(t, d, "hello-stack"); len(ready) != 0 {
		t.Fatalf("back in the review while armed: %+v", ready)
	}
	done, err := d.client.Reviewed(context.Background(), "hello-stack")
	if err != nil || len(done) != 1 || done[0].MergeWhenGreen == nil || done[0].MergeWhenGreen.Waiting != api.MergeWaitChecks {
		t.Fatalf("reviewed = %+v, %v; want it there, waiting on its checks", done, err)
	}

	gh.set(9, fix, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if m := gh.merged(); len(m) != 1 || m[0] != "9:squash" {
		t.Fatalf("merges = %v, want the fixed #9 squashed", m)
	}
	if _, ok := markOf9(t, d); ok {
		t.Fatal("the mark outlived the merge")
	}
}

// A conflict is the agent's to rebase: the mark waits, and the rebased head
// merges once green.
func TestMergeWhenGreenConflictThenRebaseMerges(t *testing.T) {
	t.Parallel()
	d, gh, head := mergeWhenGreenProject(t, "SUCCESS")
	rec := recordPRWatch(d)
	resolve(t, d)
	gh.set(9, head, "OPEN", "CONFLICTING", "SUCCESS", "")
	pollNow(d)
	if m := gh.merged(); len(m) != 0 {
		t.Fatalf("merged while conflicting: %v", m)
	}
	if m := pendingMark(t, d, api.MergeWaitConflict); m.Detail != "main" {
		t.Errorf("detail = %q, want the base it conflicts with", m.Detail)
	}
	if said, _ := rec.take(); len(said) != 1 {
		t.Fatalf("the agent was told %q, want once to rebase", said)
	}

	rebased := commitOn(t, agent01(t, d), "rebased.txt")
	gh.set(9, rebased, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if m := gh.merged(); len(m) != 1 || m[0] != "9:squash" {
		t.Fatalf("merges = %v, want the rebased #9", m)
	}
}

// New commits alone keep the mark armed: they are what a fix is.
func TestMergeWhenGreenNewCommitsKeepItArmed(t *testing.T) {
	t.Parallel()
	d, gh, _ := mergeWhenGreenProject(t, "PENDING")
	resolve(t, d)
	next := commitOn(t, agent01(t, d), "more.txt")
	gh.set(9, next, "OPEN", "MERGEABLE", "PENDING", "")
	pollNow(d)
	pendingMark(t, d, api.MergeWaitChecks)
	if r, ok := verdictOf(t, d); !ok || r.Verdict != api.ReviewReviewed {
		t.Fatalf("the verdict was taken back: %+v, %v", r, ok)
	}
	gh.set(9, next, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if m := gh.merged(); len(m) != 1 {
		t.Fatalf("merges = %v, want the new head merged", m)
	}
}

// Fix after fix still red: given up after mergeFixRounds of them, back in
// the review with why.
func TestMergeWhenGreenGivesUpAfterFixRounds(t *testing.T) {
	t.Parallel()
	d, gh, head := mergeWhenGreenProject(t, "FAILURE")
	recordPRWatch(d)
	resolve(t, d)
	agent := agent01(t, d)
	for round := 0; ; round++ {
		gh.set(9, head, "OPEN", "MERGEABLE", "FAILURE", "", "test")
		pollNow(d)
		m, _ := markOf9(t, d)
		if !m.Pending() {
			if round != mergeFixRounds || !strings.Contains(m.Error, "after 5 fixes") {
				t.Fatalf("given up after %d fixes: %q; want after %d", round, m.Error, mergeFixRounds)
			}
			break
		}
		if round >= mergeFixRounds {
			t.Fatalf("still armed after %d fixes", round)
		}
		if got := mergeWhenGreenToAPI(m).FixRounds; got != round {
			t.Fatalf("fix rounds = %d, want %d", got, round)
		}
		head = commitOn(t, agent, fmt.Sprintf("fix-%d.txt", round))
	}
	if r, ok := verdictOf(t, d); ok {
		t.Fatalf("given up, the verdict stayed: %+v", r)
	}
	if m := gh.merged(); len(m) != 0 {
		t.Fatalf("merged: %v", m)
	}
}

// Unmergeable for mergeGiveUpAfter, however few fixes came, it is given up.
func TestMergeWhenGreenGivesUpAfterTheTimeLimit(t *testing.T) {
	t.Parallel()
	d, gh, head := mergeWhenGreenProject(t, "PENDING")
	recordPRWatch(d)
	now := time.Now()
	d.srv.prWatch.now = func() time.Time { return now }
	resolve(t, d)
	gh.set(9, head, "OPEN", "CONFLICTING", "SUCCESS", "")
	pollNow(d)
	pendingMark(t, d, api.MergeWaitConflict)

	now = now.Add(mergeGiveUpAfter - time.Minute)
	pollNow(d)
	pendingMark(t, d, api.MergeWaitConflict)

	now = now.Add(time.Minute)
	pollNow(d)
	if m, _ := markOf9(t, d); m.Pending() || !strings.Contains(m.Error, "still conflicting with main after 3 days") {
		t.Fatalf("mark = %+v; want given up after 3 days", m)
	}
}

// What still gives a mark up at once: GitHub refusing the merge for a reason
// no fix changes, the pull request closed, and its agent gone while red.
func TestMergeWhenGreenGivesUp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, d testDaemon, gh *fakeGraphQL, head string)
		want   string
	}{
		{"review required", func(_ *testing.T, _ testDaemon, gh *fakeGraphQL, head string) {
			gh.mu.Lock()
			gh.refuse = "At least 1 approving review is required by reviewers with write access."
			gh.mu.Unlock()
			gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
		}, "GitHub refused the merge"},
		{"branch protection", func(_ *testing.T, _ testDaemon, gh *fakeGraphQL, head string) {
			gh.mu.Lock()
			gh.refuse = "Repository rule violations found"
			gh.mu.Unlock()
			gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
		}, "Repository rule violations"},
		{"closed", func(_ *testing.T, _ testDaemon, gh *fakeGraphQL, head string) {
			gh.set(9, head, "CLOSED", "MERGEABLE", "SUCCESS", "")
		}, "was closed"},
		{"agent gone", func(t *testing.T, d testDaemon, gh *fakeGraphQL, head string) {
			if err := d.srv.store.RemoveAgent(context.Background(), "hello-stack", "agent-01"); err != nil {
				t.Fatal(err)
			}
			gh.set(9, head, "OPEN", "MERGEABLE", "FAILURE", "", "test")
		}, "nobody can fix it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, gh, head := mergeWhenGreenProject(t, "PENDING")
			recordPRWatch(d)
			resolve(t, d)
			tc.change(t, d, gh, head)
			pollNow(d)
			if m := gh.merged(); len(m) != 0 {
				t.Fatalf("merged: %v", m)
			}
			if m, ok := markOf9(t, d); !ok || m.Pending() || !strings.Contains(m.Error, tc.want) {
				t.Fatalf("mark = %+v, %v; want given up with %q", m, ok, tc.want)
			}
		})
	}
}

// GitHub refusing for now (a required check not reported yet, the branch
// moved under the merge) keeps the mark, and it merges on a later look.
func TestMergeWhenGreenRetriesARefusalForNow(t *testing.T) {
	t.Parallel()
	d, gh, head := mergeWhenGreenProject(t, "SUCCESS")
	gh.mu.Lock()
	gh.refuse = "Required status check \"test\" is expected."
	gh.mu.Unlock()
	resolve(t, d)
	pollNow(d)
	if m := pendingMark(t, d, api.MergeWaitRetry); !strings.Contains(m.Detail, "is expected") {
		t.Errorf("detail = %q, want GitHub's reason", m.Detail)
	}
	gh.mu.Lock()
	gh.refuse = ""
	gh.mu.Unlock()
	gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if m := gh.merged(); len(m) != 1 {
		t.Fatalf("merges = %v, want it merged once GitHub let it", m)
	}
}

func TestMergeRefusedForNow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&github.StatusError{Code: 409, Message: "Head branch was modified. Review and try the merge again."}, true},
		{&github.StatusError{Code: 405, Message: "Pull Request is not mergeable"}, true},
		{&github.StatusError{Code: 405, Message: "Required status check \"test\" is expected."}, true},
		{&github.StatusError{Code: 502, Message: "Bad gateway"}, true},
		{errors.New("dial tcp: i/o timeout"), true},
		{&github.StatusError{Code: 405, Message: "At least 1 approving review is required by reviewers with write access."}, false},
		{&github.StatusError{Code: 405, Message: "Repository rule violations found"}, false},
		{&github.StatusError{Code: 405, Message: "Base branch is protected"}, false},
		{fmt.Errorf("%w: nope", github.ErrNoAccess), false},
	} {
		if got := mergeRefusedForNow(tc.err); got != tc.want {
			t.Errorf("mergeRefusedForNow(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func agent01(t *testing.T, d testDaemon) state.Agent {
	t.Helper()
	a, err := d.srv.store.Agent(context.Background(), "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// pendingMark is #9's mark, which must still be armed, waiting on waiting.
func pendingMark(t *testing.T, d testDaemon, waiting string) state.MergeWhenGreen {
	t.Helper()
	m, ok := markOf9(t, d)
	if !ok || !m.Pending() || m.Waiting != waiting {
		t.Fatalf("mark = %+v, %v; want it armed, waiting on %q", m, ok, waiting)
	}
	return m
}

// Cancelled, nothing is merged when it turns green, and the agent is back
// in the review.
func TestMergeWhenGreenCancel(t *testing.T) {
	t.Parallel()
	d, gh, head := mergeWhenGreenProject(t, "PENDING")
	resolve(t, d)
	if err := d.client.CancelMergeWhenGreen(context.Background(), "hello-stack", 9); err != nil {
		t.Fatal(err)
	}
	if _, ok := markOf9(t, d); ok {
		t.Fatal("cancelled, the mark is still there")
	}
	if r, ok := verdictOf(t, d); ok {
		t.Fatalf("cancelled, the verdict stayed: %+v", r)
	}
	gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if m := gh.merged(); len(m) != 0 {
		t.Fatalf("a cancelled mark merged: %v", m)
	}
}

// Off, the button's route refuses, and marks left from before aren't acted
// on.
func TestMergeWhenGreenOff(t *testing.T) {
	t.Parallel()
	d, gh, head := mergeWhenGreenProject(t, "PENDING")
	resolve(t, d)
	off := false
	if _, err := d.client.UpdateSettings(context.Background(), api.UpdateSettingsRequest{MergeWhenGreen: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.MergeWhenGreen(context.Background(), "hello-stack", 9, api.MergeWhenGreenRequest{Method: "squash"}); err == nil || !strings.Contains(err.Error(), "off") {
		t.Fatalf("resolving with it off: %v", err)
	}
	gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if m := gh.merged(); len(m) != 0 {
		t.Fatalf("merged with the setting off: %v", m)
	}
}
