package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/schedule"
	"agentbox/internal/state"
)

// setMain puts the default branch's newest commit on the fake GitHub, with
// its checks' rollup state and the names of the failing ones.
func (f *fakeGraphQL) setMain(sha, checks string, failing ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var contexts []any
	for _, name := range failing {
		contexts = append(contexts, map[string]any{"__typename": "CheckRun", "name": name, "status": "COMPLETED",
			"conclusion": "FAILURE", "detailsUrl": "https://github.com/acme/hello-stack/actions/runs/2/job/" + name})
	}
	f.head = map[string]any{"oid": sha, "url": "https://github.com/acme/hello-stack/commit/" + sha, "messageHeadline": "Bump the parser",
		"statusCheckRollup": map[string]any{"state": checks, "contexts": map[string]any{"nodes": contexts}}}
}

// from says who opened a pull request on the fake GitHub, from which branch,
// and whom a review is asked of.
func (f *fakeGraphQL) from(number int, author, branch string, reviewers ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr := f.prs[number]
	pr["author"] = map[string]any{"login": author}
	pr["headRefName"] = branch
	var nodes []any
	for _, r := range reviewers {
		nodes = append(nodes, map[string]any{"requestedReviewer": map[string]any{"__typename": "User", "login": r}})
	}
	pr["reviewRequests"] = map[string]any{"nodes": nodes}
}

func (f *fakeGraphQL) addIssue(number int, title string, labels ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var nodes []any
	for _, l := range labels {
		nodes = append(nodes, map[string]any{"name": l})
	}
	f.issues = append(f.issues, map[string]any{"number": number, "title": title, "body": "It crashes on start.",
		"url": fmt.Sprintf("https://github.com/acme/hello-stack/issues/%d", number), "author": map[string]any{"login": "reporter"},
		"labels": map[string]any{"nodes": nodes}})
}

// Each of the four events fires its trigger once, with what happened in the
// task; what was already there when the trigger was made doesn't fire, a
// pull request of an agent's isn't "from outside", and a review asked again
// after it was answered is another event.
func TestEventTriggersFireOncePerEvent(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	leadReadyToChat(t, d, "hello-stack")
	ctx := context.Background()
	gh.viewer = "leciric"

	// What is there before any trigger is made.
	gh.setMain("1111111111", "FAILURE", "test")
	gh.set(3, "3333333333", "OPEN", "MERGEABLE", "SUCCESS", "")
	gh.from(3, "someone", "fix/typo", "leciric")
	gh.addIssue(5, "Old crash", "bug")

	made := map[string]string{}
	for _, req := range []api.ScheduleRequest{
		{Title: str("Fix main"), Task: str("Find why CI broke on main and fix it."), On: str("ci_failed")},
		{Title: str("Review"), Task: str("Review it for me."), On: str("review-requested")},
		{Title: str("Bugs"), Task: str("Reproduce and fix this bug."), On: str("issue_labeled"), Label: str("bug")},
		{Title: str("Outside PRs"), Task: str("Look over this contribution."), On: str("pr_opened")},
	} {
		sc, err := d.client.AddSchedule(ctx, "hello-stack", req)
		if err != nil {
			t.Fatal(err)
		}
		if sc.Kind != api.ScheduleOnEvent || sc.NextRun != nil || !strings.HasPrefix(sc.Describe, "when ") {
			t.Fatalf("trigger = %+v", sc)
		}
		made[sc.Event] = sc.ID
	}

	// The first look only takes in what is there.
	pollNow(d)
	if chips := scheduleChips(t, d, "hello-stack"); len(chips) != 0 {
		t.Fatalf("what was there before fired: %+v", chips)
	}

	// Then each thing happens.
	gh.setMain("2222222222", "FAILURE", "test", "lint")
	gh.set(7, "7777777777", "OPEN", "MERGEABLE", "PENDING", "")
	gh.from(7, "contributor", "feature/dark-mode", "leciric")
	gh.from(3, "someone", "fix/typo", "leciric") // still asked: no new event
	head := commitOn(t, agents["agent-01"], "reminders.txt")
	gh.set(9, head, "OPEN", "MERGEABLE", "PENDING", "")
	gh.from(9, "leciric", "agentbox/agent-01")
	gh.addIssue(6, "Crash on save", "Bug", "p1")
	gh.addIssue(8, "Feature idea", "enhancement")
	pollNow(d)

	chips := scheduleChips(t, d, "hello-stack")
	byTitle := map[string]string{}
	for _, c := range chips {
		if c.ScheduledRun.Trigger != state.ScheduleTriggerEvent || c.ScheduledRun.Event == "" {
			t.Errorf("chip = %+v", c.ScheduledRun)
		}
		byTitle[c.ScheduledRun.Title] = c.Text
	}
	want := map[string][]string{
		"Fix main · 2222222": {"Find why CI broke on main and fix it.", "CI failed on main of acme/hello-stack", "- lint (https://"},
		"Review · #7":        {"Review it for me.", "requested from leciric", "feature/dark-mode", "@contributor"},
		"Bugs · #6":          {"Reproduce and fix this bug.", "Crash on save", "It crashes on start.", "Bug, p1"},
		"Outside PRs · #7":   {"Look over this contribution.", "not by an agent", "pull/7"},
	}
	if len(chips) != len(want) {
		t.Fatalf("fired %d times, want %d: %v", len(chips), len(want), byTitle)
	}
	for title, parts := range want {
		for _, part := range parts {
			if !strings.Contains(byTitle[title], part) {
				t.Errorf("%q's task lacks %q: %q", title, part, byTitle[title])
			}
		}
	}
	runs, err := d.client.ScheduleRuns(ctx, "hello-stack", made["issue_labeled"])
	if err != nil || len(runs) != 1 || runs[0].Trigger != "event" || runs[0].Outcome != "sent" ||
		!strings.Contains(runs[0].Event, "issue #6") || runs[0].EventURL != "https://github.com/acme/hello-stack/issues/6" {
		t.Fatalf("runs = %+v, %v", runs, err)
	}

	// Looking again, nothing has happened.
	pollNow(d)
	if n := len(scheduleChips(t, d, "hello-stack")); n != len(want) {
		t.Fatalf("a second look fired again: %d chips", n)
	}

	// The user reviews #7, and is asked again: another event.
	gh.from(7, "contributor", "feature/dark-mode")
	pollNow(d)
	gh.from(7, "contributor", "feature/dark-mode", "leciric")
	pollNow(d)
	if n := len(scheduleChips(t, d, "hello-stack")); n != len(want)+1 {
		t.Fatalf("a review asked again fired %d times in all, want %d", n, len(want)+1)
	}

	// Paused, it doesn't fire; resumed, it starts from what is there then.
	paused, resumed := true, false
	if _, err := d.client.UpdateSchedule(ctx, "hello-stack", made["ci_failed"], api.ScheduleRequest{Paused: &paused}); err != nil {
		t.Fatal(err)
	}
	gh.setMain("3333333333", "ERROR", "test")
	pollNow(d)
	if _, err := d.client.UpdateSchedule(ctx, "hello-stack", made["ci_failed"], api.ScheduleRequest{Paused: &resumed}); err != nil {
		t.Fatal(err)
	}
	pollNow(d)
	if n := len(scheduleChips(t, d, "hello-stack")); n != len(want)+1 {
		t.Fatalf("a red commit from while it was paused fired: %d chips", n)
	}
	gh.setMain("4444444444", "FAILURE", "test")
	pollNow(d)
	if n := len(scheduleChips(t, d, "hello-stack")); n != len(want)+2 {
		t.Fatalf("a red commit after it resumed fired %d times in all, want %d", n, len(want)+2)
	}
}

// Event triggers are looked for even with the pull request watch off, which
// then follows no agent's pull request.
func TestEventTriggersRunWithThePRWatchOff(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	rec := recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	leadReadyToChat(t, d, "hello-stack")
	ctx := context.Background()
	if err := d.srv.store.SetFlag(ctx, state.SettingPRWatch, false); err != nil {
		t.Fatal(err)
	}
	head := commitOn(t, agents["agent-01"], "reminders.txt")
	gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	gh.setMain("1111111111", "SUCCESS")
	pollNow(d)
	if gh.count() != 0 {
		t.Fatalf("with the watch off and no trigger, GitHub was asked %d times", gh.count())
	}
	if _, err := d.client.AddSchedule(ctx, "hello-stack", api.ScheduleRequest{Task: str("Fix main."), On: str("ci_failed")}); err != nil {
		t.Fatal(err)
	}
	pollNow(d)
	gh.set(9, head, "OPEN", "CONFLICTING", "FAILURE", "", "test")
	gh.setMain("2222222222", "FAILURE", "test")
	pollNow(d)
	if n := len(scheduleChips(t, d, "hello-stack")); n != 1 {
		t.Fatalf("the trigger fired %d times, want 1", n)
	}
	if said, _ := rec.take(); len(said) != 0 {
		t.Fatalf("the agent's pull request was watched with the watch off: %q", said)
	}
}

func TestEventTriggerRequestsAreChecked(t *testing.T) {
	t.Parallel()
	d, _ := oneProject(t)
	ctx := context.Background()
	for name, req := range map[string]api.ScheduleRequest{
		"unknown event": {Task: str("x"), On: str("deploy_failed")},
		"no label":      {Task: str("x"), On: str("issue_labeled")},
		"on and in":     {Task: str("x"), On: str("ci_failed"), In: str("1h")},
	} {
		if _, err := d.client.AddSchedule(ctx, "web", req); err == nil {
			t.Errorf("%s: made", name)
		}
	}
	// A trigger moved onto a time is a timed schedule again.
	sc, err := d.client.AddSchedule(ctx, "web", api.ScheduleRequest{Task: str("x"), On: str("issue_labeled"), Label: str("bug")})
	if err != nil || sc.EventLabel != "bug" || sc.Describe != `when an issue is labelled "bug"` {
		t.Fatalf("AddSchedule = %+v, %v", sc, err)
	}
	sc, err = d.client.UpdateSchedule(ctx, "web", sc.ID, api.ScheduleRequest{Every: str("1d")})
	if err != nil || sc.Kind != api.ScheduleEvery || sc.Event != "" || sc.EventLabel != "" || sc.NextRun == nil {
		t.Fatalf("UpdateSchedule = %+v, %v", sc, err)
	}
}

// A pull request is an agent's by its branch's prefix, or by its commits; a
// draft from outside still counts, and says so.
func TestMatchEvents(t *testing.T) {
	p := state.Project{BranchPrefix: "ab/"}
	isAgents := agentPR(p, []agentHead{{agent: "agent-02", branch: "custom", own: []string{"c0ffee"}}})
	open := []github.WatchedPR{
		{Number: 1, HeadBranch: "ab/agent-01"},
		{Number: 2, HeadBranch: "other", HeadSHA: "c0ffee"},
		{Number: 3, HeadBranch: "custom"},
		{Number: 4, HeadBranch: "patch-1", Draft: true, Author: ""},
	}
	got, _ := matchEvents(state.Schedule{Event: schedule.EventPROpened}, github.Repo{Owner: "a", Name: "b"}, &github.RepoEvents{}, open, isAgents)
	if len(got) != 1 || got[0].Key != "pr:4" || !strings.Contains(got[0].Details, "(a draft)") || !strings.Contains(got[0].Details, "a deleted account") {
		t.Fatalf("matchEvents = %+v", got)
	}
	// Checks still running on main, or passing, aren't an event.
	for _, checks := range []string{"pending", "passing", ""} {
		if got, _ := matchEvents(state.Schedule{Event: schedule.EventCIFailed}, github.Repo{}, &github.RepoEvents{Head: github.Commit{SHA: "x", Checks: checks}}, nil, isAgents); len(got) != 0 {
			t.Errorf("checks %q fired: %+v", checks, got)
		}
	}
}
