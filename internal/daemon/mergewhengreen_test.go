package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
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

// Checks failing give the mark up: nothing is merged, the reason is kept for
// the item, and the verdict is taken back so the agent is in the review
// again.
func TestMergeWhenGreenGivesUpWhenRed(t *testing.T) {
	t.Parallel()
	d, gh, head := mergeWhenGreenProject(t, "PENDING")
	resolve(t, d)
	gh.set(9, head, "OPEN", "MERGEABLE", "FAILURE", "", "test")
	pollNow(d)
	if m := gh.merged(); len(m) != 0 {
		t.Fatalf("merged with failing checks: %v", m)
	}
	m, ok := markOf9(t, d)
	if !ok || m.Pending() || !strings.Contains(m.Error, "checks failed (test)") {
		t.Fatalf("mark = %+v, %v; want it given up, saying the checks failed", m, ok)
	}
	if r, ok := verdictOf(t, d); ok {
		t.Fatalf("the verdict stayed: %+v", r)
	}

	// Green again later: given up stays given up until the user says.
	gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if m := gh.merged(); len(m) != 0 {
		t.Fatalf("a given-up mark merged: %v", m)
	}
}

// What else gives a mark up: a conflict, commits nobody resolved, and GitHub
// refusing the merge.
func TestMergeWhenGreenGivesUp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(gh *fakeGraphQL, head string)
		want   string
	}{
		{"conflict", func(gh *fakeGraphQL, head string) { gh.set(9, head, "OPEN", "CONFLICTING", "SUCCESS", "") }, "conflicts with main"},
		{"new commits", func(gh *fakeGraphQL, _ string) { gh.set(9, "fedcba9876543210", "OPEN", "MERGEABLE", "SUCCESS", "") }, "new commits were pushed (fedcba9)"},
		{"refused", func(gh *fakeGraphQL, head string) {
			gh.mu.Lock()
			gh.refuse = "Required status check is expected"
			gh.mu.Unlock()
			gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
		}, "GitHub refused the merge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, gh, head := mergeWhenGreenProject(t, "PENDING")
			resolve(t, d)
			tc.change(gh, head)
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
