package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// endedPR is a list or lookup item for a pull request that was merged (or,
// with merged false, closed), touched just now.
func endedPR(number int, head string, merged bool) string {
	at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	mergedAt := "null"
	if merged {
		mergedAt = fmt.Sprintf("%q", at)
	}
	return fmt.Sprintf(`{"number":%d,"title":"Work","state":"closed","merged_at":%s,"html_url":"https://github.com/acme/hello-stack/pull/%d","draft":false,"updated_at":%q,"base":{"ref":"main"},"head":{"ref":"feat/work-%d","sha":%q}}`,
		number, mergedAt, number, at, number, head)
}

func openPR(number int, head string) string {
	at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	return fmt.Sprintf(`{"number":%d,"title":"Work","state":"open","html_url":"https://github.com/acme/hello-stack/pull/%d","draft":false,"updated_at":%q,"base":{"ref":"main"},"head":{"ref":"feat/work-%d","sha":%q}}`,
		number, number, at, number, head)
}

func readyNames(t *testing.T, d testDaemon) []string {
	t.Helper()
	items, err := d.client.ReadyToReview(context.Background(), "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, i := range items {
		names = append(names, i.Agent)
	}
	return names
}

// The Windows report: agents whose pull requests were merged days before,
// while an older daemon ran, came back as ready to review after the upgrade.
// The PR watch had never seen them open, so it never saw them end, and the
// review list never looked at a pull request's state. Now the daemon asks
// GitHub once as it starts, holding the list back until it has, and frees
// each agent whose pull request ended, as a merge from the review does.
func TestStartupFreesAgentsWhosePullRequestsEndedWhileDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newSlowGitHub(t, d, "[]", 0)
	_, agents := pullsProject(t, d, "agent-01", "agent-02", "agent-03")
	merged := commitOn(t, agents["agent-01"], "merged.txt")
	closed := commitOn(t, agents["agent-02"], "closed.txt")
	open := commitOn(t, agents["agent-03"], "open.txt")
	for _, a := range agents {
		d.srv.record(ctx, finishedEvent(a, api.AgentChanges{}, nil, "Done.", time.Now()))
	}
	// agent-01's pull request is older than the list page: only a lookup by
	// its commit finds it, as for one merged three days ago in a busy repo.
	gh.setCommit(merged, "["+endedPR(7, merged, true)+"]")
	gh.setList("[" + openPR(9, open) + "," + endedPR(8, closed, false) + "]")

	d.srv.holdReview(ctx)
	if names := readyNames(t, d); len(names) != 0 {
		t.Fatalf("before GitHub was asked, ready to review = %v, want nothing yet", names)
	}
	d.srv.reconcilePullRequests(ctx)

	for name, want := range map[string]string{"agent-01": "merged", "agent-02": "closed"} {
		a := agents[name]
		if agentHasMachine(d, a) || !isArchived(t, d, a) {
			t.Errorf("%s, whose pull request was %s, is still live", name, want)
			continue
		}
		past, _ := d.srv.store.ArchivedAgentByName(ctx, a.Project, a.Name)
		if past.PR.State != want {
			t.Errorf("%s was archived with its pull request %+v, want %s", name, past.PR, want)
		}
	}
	if !agentHasMachine(d, agents["agent-03"]) {
		t.Error("agent-03, whose pull request is open, was freed")
	}
	if names := readyNames(t, d); len(names) != 1 || names[0] != "agent-03" {
		t.Errorf("ready to review = %v, want only agent-03", names)
	}
}

// An agent whose pull request was merged but that has work only on its
// machine keeps it, and the lead hears so; but it isn't work to review.
func TestMergedPullRequestIsNeverToReview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newSlowGitHub(t, d, "[]", 0)
	_, agents := pullsProject(t, d, "agent-01")
	a := agents["agent-01"]
	head := commitOn(t, a, "merged.txt")
	commitOn(t, a, "after.txt") // committed after the merge, never pushed
	d.srv.record(ctx, finishedEvent(a, api.AgentChanges{}, nil, "Done.", time.Now()))
	gh.setList("[" + endedPR(7, head, true) + "]")

	d.srv.reconcilePullRequests(ctx)
	if !agentHasMachine(d, a) {
		t.Fatal("an agent with unpushed commits lost its machine")
	}
	if names := readyNames(t, d); len(names) != 0 {
		t.Errorf("ready to review = %v, want nothing: its pull request was merged", names)
	}
}

// The PR watch seeing a pull request it follows merged frees the agent then,
// rather than only forgetting it.
func TestPRWatchFreesTheAgentWhenItsPullRequestMerges(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	a := agents["agent-01"]
	head := commitOn(t, a, "reminders.txt")

	gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if !agentHasMachine(d, a) {
		t.Fatal("an agent with an open pull request was freed")
	}
	gh.set(9, head, "MERGED", "UNKNOWN", "SUCCESS", "")
	pollNow(d)
	if agentHasMachine(d, a) || !isArchived(t, d, a) {
		t.Fatalf("the merge left agent-01 live (live %v, past %v)", agentHasMachine(d, a), isArchived(t, d, a))
	}
	past, _ := d.srv.store.ArchivedAgentByName(context.Background(), a.Project, a.Name)
	if past.PR.Number != 9 || past.PR.State != "merged" {
		t.Errorf("archived with %+v, want #9 merged", past.PR)
	}
}

// A destroy the last daemon left halfway — the row marked destroying, which
// every list leaves out — is finished as the next one starts, so the agent
// can't linger hidden: it becomes a past agent.
func TestStartupFinishesAnInterruptedDestroy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	_, agents := pullsProject(t, d, "agent-01")
	a := agents["agent-01"]
	if err := d.srv.store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentDestroying); err != nil {
		t.Fatal(err)
	}
	fleet, err := d.client.Fleet(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fleet.Agents {
		if f.Name == a.Name {
			t.Fatalf("an agent being destroyed is listed, as %q", f.State)
		}
	}
	d.srv.reconcilePullRequests(ctx)
	if agentHasMachine(d, a) || !isArchived(t, d, a) {
		t.Fatalf("the interrupted destroy wasn't finished (live %v, past %v)", agentHasMachine(d, a), isArchived(t, d, a))
	}
}

// followUp has agent a open a second pull request as agent-469 did for #407:
// a new branch of its own, made in its worktree, with a commit on it.
func followUp(t *testing.T, a state.Agent, branch string) string {
	t.Helper()
	testutil.Git(t, a.Worktree, "switch", "--quiet", "-c", branch)
	return commitOn(t, a, "follow-up.txt")
}

// agent-469 opened #405, then a follow-up #407 from a new branch; merging
// #405 freed it, and #407 left the review list with it. An agent is done
// when its last pull request ends, not its first: until then it stays, and
// its open one is what the review shows.
func TestFollowUpPullRequestKeepsTheAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newSlowGitHub(t, d, "[]", 0)
	_, agents := pullsProject(t, d, "agent-01")
	a := agents["agent-01"]
	first := commitOn(t, a, "first.txt")
	second := followUp(t, a, "agentbox/follow-up")
	d.srv.record(ctx, finishedEvent(a, api.AgentChanges{}, nil, "Opened the follow-up pull request, from a branch of its own.", time.Now()))

	gh.setList("[" + endedPR(7, first, true) + "," + openPR(8, second) + "]")
	d.srv.reconcilePullRequests(ctx)
	if !agentHasMachine(d, a) {
		t.Fatal("the first pull request's merge freed an agent whose follow-up is open")
	}
	items, err := d.client.ReadyToReview(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PR == nil || items[0].PR.Number != 8 {
		t.Fatalf("ready to review = %+v, want agent-01 with its open #8", items)
	}
	if got := items[0]; got.Branch != "agentbox/follow-up" || got.Summary != "Opened the follow-up pull request, from a branch of its own." {
		t.Errorf("the item shows branch %q, summary %q; want the follow-up's", got.Branch, got.Summary)
	}

	gh.setList("[" + endedPR(8, second, true) + "," + endedPR(7, first, true) + "]")
	d.srv.reconcilePullRequests(ctx)
	if agentHasMachine(d, a) || !isArchived(t, d, a) {
		t.Fatal("both pull requests merged and the agent is still live")
	}
	if past, _ := d.srv.store.ArchivedAgentByName(ctx, a.Project, a.Name); past.PR.Number != 8 {
		t.Errorf("archived with %+v, want its last pull request, #8", past.PR)
	}
}

// The PR watch follows both, and frees the agent when the second ends.
func TestPRWatchKeepsTheAgentWhileAFollowUpIsOpen(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	a := agents["agent-01"]
	first := commitOn(t, a, "first.txt")
	second := followUp(t, a, "agentbox/follow-up")

	gh.set(9, first, "OPEN", "MERGEABLE", "SUCCESS", "")
	gh.set(10, second, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	gh.set(9, first, "MERGED", "UNKNOWN", "SUCCESS", "")
	pollNow(d)
	if !agentHasMachine(d, a) {
		t.Fatal("#9's merge freed the agent while its #10 is open")
	}
	gh.set(10, second, "MERGED", "UNKNOWN", "SUCCESS", "")
	pollNow(d)
	if agentHasMachine(d, a) || !isArchived(t, d, a) {
		t.Fatal("both merged and the agent is still live")
	}
	if past, _ := d.srv.store.ArchivedAgentByName(context.Background(), a.Project, a.Name); past.PR.Number != 10 {
		t.Errorf("archived with %+v, want #10", past.PR)
	}
}
