package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// fakeGraphQL is a GitHub that answers the pull request watch's one query
// from pull requests a test sets, and counts the requests it was sent.
type fakeGraphQL struct {
	t        *testing.T
	mu       sync.Mutex
	prs      map[int]map[string]any // every pull request, open or not, by number
	requests int
	// limited makes every answer GitHub's "the hourly budget is spent".
	limited time.Time
	// remaining is what rateLimit.remaining says; 5000 when unset.
	remaining int
	resetAt   time.Time
}

var askedNumber = regexp.MustCompile(`pr(\d+): pullRequest\(number: (\d+)\)`)

func newFakeGraphQL(t *testing.T, d testDaemon) *fakeGraphQL {
	f := &fakeGraphQL{t: t, prs: map[int]map[string]any{}, remaining: 5000}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" || r.Method != http.MethodPost {
			// The REST calls are the pull request cache's, which a finish or
			// a fleet read can start: not the watch's business.
			http.NotFound(w, r)
			return
		}
		var body struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding the query: %v", err)
		}
		if body.Variables["owner"] != "acme" || body.Variables["name"] != "hello-stack" {
			t.Errorf("variables = %v", body.Variables)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++
		if !f.limited.IsZero() {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(f.limited.Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
			return
		}
		repo := map[string]any{}
		var open []any
		for n := 1; n <= 100; n++ {
			if pr, ok := f.prs[n]; ok && pr["state"] == "OPEN" {
				open = append(open, pr)
			}
		}
		repo["open"] = map[string]any{"nodes": open}
		for _, m := range askedNumber.FindAllStringSubmatch(body.Query, -1) {
			n, _ := strconv.Atoi(m[1])
			if pr, ok := f.prs[n]; ok {
				repo["pr"+m[1]] = pr
			} else {
				repo["pr"+m[1]] = nil
			}
		}
		reset := f.resetAt
		if reset.IsZero() {
			reset = time.Now().Add(time.Hour)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"rateLimit":  map[string]any{"remaining": f.remaining, "resetAt": reset.UTC().Format(time.RFC3339)},
			"repository": repo,
		}})
	}))
	t.Cleanup(stub.Close)
	d.setGitHub(t, stub.URL)
	return f
}

// set puts a pull request on the fake GitHub. checks is the rollup's state
// (SUCCESS, FAILURE, PENDING, or "" for none), and failing names the checks
// that failed.
func (f *fakeGraphQL) set(number int, head, state, mergeable, checks, review string, failing ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var commit map[string]any
	if checks != "" {
		var contexts []any
		for _, name := range failing {
			contexts = append(contexts, map[string]any{"__typename": "CheckRun", "name": name, "status": "COMPLETED",
				"conclusion": "FAILURE", "detailsUrl": "https://github.com/acme/hello-stack/actions/runs/1/job/" + name})
		}
		contexts = append(contexts, map[string]any{"__typename": "CheckRun", "name": "lint", "status": "COMPLETED", "conclusion": "SUCCESS"})
		commit = map[string]any{"oid": head, "statusCheckRollup": map[string]any{"state": checks, "contexts": map[string]any{"nodes": contexts}}}
	} else {
		commit = map[string]any{"oid": head, "statusCheckRollup": nil}
	}
	f.prs[number] = map[string]any{
		"number": number, "title": "Reminders page", "url": fmt.Sprintf("https://github.com/acme/hello-stack/pull/%d", number),
		"state": state, "isDraft": false, "updatedAt": "2026-09-27T10:00:00Z",
		"headRefName": "feat/reminders", "headRefOid": head, "baseRefName": "main",
		"mergeable": mergeable, "reviewDecision": nilIfEmpty(review),
		"commits": map[string]any{"nodes": []any{map[string]any{"commit": commit}}},
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (f *fakeGraphQL) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// prWatchRecorder stands in for the chats the watch speaks to.
type prWatchRecorder struct {
	mu     sync.Mutex
	agents []string // "agent-01: message"
	leads  []leadNotice
	woke   bool // what the fake wake reports
	fail   error
}

type leadNotice struct {
	text string
	act  bool
}

func recordPRWatch(d testDaemon) *prWatchRecorder {
	rec := &prWatchRecorder{}
	d.srv.prTell = func(_ context.Context, a state.Agent, text string) (bool, error) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		if rec.fail != nil {
			return false, rec.fail
		}
		rec.agents = append(rec.agents, a.Name+": "+text)
		return rec.woke, nil
	}
	d.srv.prLead = func(_ context.Context, _ string, notice string, act bool) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.leads = append(rec.leads, leadNotice{notice, act})
	}
	return rec
}

func (rec *prWatchRecorder) take() ([]string, []leadNotice) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	agents, leads := rec.agents, rec.leads
	rec.agents, rec.leads = nil, nil
	return agents, leads
}

// pollNow makes every project due and looks once.
func pollNow(d testDaemon) {
	for _, p := range []string{"hello-stack"} {
		d.srv.prWatch.poke(p)
	}
	d.srv.pollPRWatch(context.Background())
}

// The watch finds the agent's pull request by its commits, and tells the
// agent only when something turns bad: a failure, then the same failure again
// is one message, a conflict is another, and a merge ends the watch.
func TestPRWatchTellsTheAgentOnTransitionsOnly(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	rec := recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	head := commitOn(t, agents["agent-01"], "reminders.txt")
	ctx := context.Background()

	// Somebody else's pull request is never watched.
	gh.set(3, "0123456789abcdef", "OPEN", "CONFLICTING", "FAILURE", "", "test")
	gh.set(9, head, "OPEN", "MERGEABLE", "PENDING", "")
	pollNow(d)
	if said, leads := rec.take(); len(said) != 0 || len(leads) != 0 {
		t.Fatalf("a pull request with nothing wrong was announced: %v %v", said, leads)
	}
	rows, _ := d.srv.store.PRWatches(ctx, "hello-stack")
	if len(rows) != 1 || rows[0].Number != 9 || rows[0].Agent != "agent-01" || rows[0].Checks != "pending" {
		t.Fatalf("watched = %+v, want only #9, agent-01's", rows)
	}

	gh.set(9, head, "OPEN", "MERGEABLE", "FAILURE", "", "test")
	pollNow(d)
	said, leads := rec.take()
	if len(said) != 1 || !strings.Contains(said[0], "`test`") || !strings.Contains(said[0], "--log-failed") ||
		!strings.Contains(said[0], "feat/reminders") || strings.Contains(said[0], "`lint`") {
		t.Fatalf("failing checks told the agent %q", said)
	}
	if len(leads) != 1 || leads[0].act || !strings.Contains(leads[0].text, "has failing checks (test)") {
		t.Fatalf("the lead was told %+v, want one notice without a turn", leads)
	}

	// Still failing, same commit: nothing new to say.
	pollNow(d)
	if said, leads := rec.take(); len(said) != 0 || len(leads) != 0 {
		t.Fatalf("the same failure was announced again: %v %v", said, leads)
	}

	// Its base moved on and it conflicts now; the checks are the same.
	gh.set(9, head, "OPEN", "CONFLICTING", "FAILURE", "", "test")
	pollNow(d)
	said, _ = rec.take()
	if len(said) != 1 || !strings.Contains(said[0], "conflicts with `main`") || !strings.Contains(said[0], "Sonnet subagent") || strings.Contains(said[0], "checks are failing") {
		t.Fatalf("a conflict told the agent %q", said)
	}

	// GitHub working mergeability out again isn't the conflict going away.
	gh.set(9, head, "OPEN", "UNKNOWN", "FAILURE", "", "test")
	pollNow(d)
	gh.set(9, head, "OPEN", "CONFLICTING", "FAILURE", "", "test")
	pollNow(d)
	if said, _ := rec.take(); len(said) != 0 {
		t.Fatalf("an unknown in between announced the conflict again: %q", said)
	}

	// A push that fails again is a new failure; changes requested is news too.
	gh.set(9, "fedcba9876543210", "OPEN", "MERGEABLE", "FAILURE", "CHANGES_REQUESTED", "test")
	pollNow(d)
	said, _ = rec.take()
	if len(said) != 1 || !strings.Contains(said[0], "fedcba9") || !strings.Contains(said[0], "asked for changes") {
		t.Fatalf("a new failing head told the agent %q", said)
	}

	gh.set(9, "fedcba9876543210", "MERGED", "UNKNOWN", "FAILURE", "")
	pollNow(d)
	if rows, _ := d.srv.store.PRWatches(ctx, "hello-stack"); len(rows) != 0 {
		t.Fatalf("a merged pull request is still watched: %+v", rows)
	}
	if said, leads := rec.take(); len(said) != 0 || len(leads) != 0 {
		t.Fatalf("a merge was announced: %v %v", said, leads)
	}
}

// A merge the watch sees is the agent's open tasks implemented, with the pull
// request they landed in; nobody else's task moves.
func TestPRWatchMergeImplementsTheAgentsTasks(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	head := commitOn(t, agents["agent-01"], "reminders.txt")
	ctx := context.Background()
	mine, err := d.srv.memory().AddTask(ctx, memory.Task{Project: "hello-stack", Agent: "agent-01", Goal: "Reminders page"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.srv.memory().AddTask(ctx, memory.Task{Project: "hello-stack", Goal: "Something else"})
	if err != nil {
		t.Fatal(err)
	}

	gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if got := taskOf(t, d, "hello-stack", mine.ID); got.Status != memory.TaskOpen {
		t.Fatalf("an open pull request closed the task: %s", got.Status)
	}

	gh.set(9, head, "MERGED", "UNKNOWN", "SUCCESS", "")
	pollNow(d)
	got := taskOf(t, d, "hello-stack", mine.ID)
	if got.Status != memory.TaskDone || got.PullNumber != 9 || got.PullURL != "https://github.com/acme/hello-stack/pull/9" {
		t.Errorf("after the merge the task is %s, %q #%d; want done by #9", got.Status, got.PullURL, got.PullNumber)
	}
	if got := taskOf(t, d, "hello-stack", other.ID); got.Status != memory.TaskOpen {
		t.Errorf("another task moved to %s", got.Status)
	}
}

// An agent that's gone can't fix anything: the chat is told, with a turn to
// decide who does.
func TestPRWatchTellsTheLeadWhenTheAgentIsGone(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	rec := recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	head := commitOn(t, agents["agent-01"], "reminders.txt")

	gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	if err := d.srv.store.RemoveAgent(context.Background(), "hello-stack", "agent-01"); err != nil {
		t.Fatal(err)
	}
	gh.set(9, head, "OPEN", "CONFLICTING", "SUCCESS", "")
	pollNow(d)
	said, leads := rec.take()
	if len(said) != 0 {
		t.Fatalf("a removed agent was told %q", said)
	}
	if len(leads) != 1 || !leads[0].act || !strings.Contains(leads[0].text, "agent-01, is gone") {
		t.Fatalf("the lead was told %+v, want one notice with a turn", leads)
	}
}

// An agent that couldn't be told leaves it to the lead, and one that had to
// be started says so.
func TestPRWatchWakesTheAgentOrFallsBackToTheLead(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	rec := recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	head := commitOn(t, agents["agent-01"], "reminders.txt")

	rec.woke = true
	gh.set(9, head, "OPEN", "CONFLICTING", "", "")
	pollNow(d)
	_, leads := rec.take()
	if len(leads) != 1 || leads[0].act || !strings.Contains(leads[0].text, "starting its machine") {
		t.Fatalf("the lead was told %+v", leads)
	}

	rec.fail = fmt.Errorf("its machine won't start")
	gh.set(9, head, "OPEN", "CONFLICTING", "FAILURE", "", "test")
	pollNow(d)
	_, leads = rec.take()
	if len(leads) != 1 || !leads[0].act || !strings.Contains(leads[0].text, "couldn't be told (its machine won't start)") {
		t.Fatalf("the lead was told %+v", leads)
	}
}

// A project with nothing an agent could have put in a pull request costs
// GitHub nothing, and neither does one with the watch off; a project can turn
// it on when the installation has it off.
func TestPRWatchAsksGitHubOnlyWhenThereIsSomethingToWatch(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	ctx := context.Background()

	pollNow(d)
	if n := gh.count(); n != 0 {
		t.Fatalf("an agent with no commits of its own cost %d request(s)", n)
	}
	commitOn(t, agents["agent-01"], "reminders.txt")
	on := false
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{PRWatch: &on}); err != nil {
		t.Fatal(err)
	}
	pollNow(d)
	if n := gh.count(); n != 0 {
		t.Fatalf("the watch turned off cost %d request(s)", n)
	}
	watch := state.PRWatchOn
	p, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{PRWatch: &watch})
	if err != nil {
		t.Fatal(err)
	}
	if p.PRWatch != "on" || !p.PRWatching {
		t.Fatalf("project = %+v, want its own watch on", p)
	}
	pollNow(d)
	if n := gh.count(); n != 1 {
		t.Fatalf("the project's own watch cost %d request(s), want 1", n)
	}
}

// The interval follows what's happening: quick while checks run, doubling
// while nothing changes, and GitHub's reset when it says to stop.
func TestPRWatchPacesItself(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	head := commitOn(t, agents["agent-01"], "reminders.txt")
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	d.srv.prWatch.now = func() time.Time { return now }
	wait := func() time.Duration {
		d.srv.prWatch.mu.Lock()
		defer d.srv.prWatch.mu.Unlock()
		return d.srv.prWatch.repos["hello-stack"].next.Sub(now)
	}
	// look waits until the next look is due, and looks.
	look := func() {
		now = now.Add(wait())
		d.srv.pollPRWatch(context.Background())
	}

	gh.set(9, head, "OPEN", "MERGEABLE", "PENDING", "")
	d.srv.pollPRWatch(context.Background())
	if got := wait(); got != prWatchFast {
		t.Fatalf("with checks running, the next look is in %v, want %v", got, prWatchFast)
	}
	gh.set(9, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	look()
	if got := wait(); got != prWatchCalm {
		t.Fatalf("after a change, the next look is in %v, want %v", got, prWatchCalm)
	}
	var waits []time.Duration
	for range 5 {
		look()
		waits = append(waits, wait())
	}
	if want := []time.Duration{4 * time.Minute, 8 * time.Minute, prWatchIdle, prWatchIdle, prWatchIdle}; fmt.Sprint(waits) != fmt.Sprint(want) {
		t.Fatalf("with nothing changing, the looks are %v apart, want %v", waits, want)
	}
	before := gh.count()
	now = now.Add(time.Minute)
	d.srv.pollPRWatch(context.Background()) // not due yet
	now = now.Add(-time.Minute)
	if gh.count() != before {
		t.Fatal("the watch looked before it was due")
	}

	// A low budget waits for GitHub's reset.
	gh.mu.Lock()
	gh.remaining, gh.resetAt = 100, now.Add(wait()).Add(40*time.Minute) // 40 minutes after the look below
	gh.mu.Unlock()
	look()
	if got := wait(); got != 40*time.Minute {
		t.Fatalf("with 100 points left, the next look is in %v, want the reset's 40m", got)
	}

	// GitHub refusing with its rate limit waits until it says.
	gh.mu.Lock()
	gh.limited = now.Add(50 * time.Minute).Truncate(time.Second)
	gh.mu.Unlock()
	d.srv.prWatch.poke("hello-stack")
	d.srv.pollPRWatch(context.Background())
	if got := wait(); got != 50*time.Minute {
		t.Fatalf("rate limited, the next look is in %v, want 50m", got)
	}
}

// The fleet and the pull requests tab show what the watch read: conflict,
// review, and checks, for the head the watch read.
func TestPRWatchOverlaysWhatItRead(t *testing.T) {
	w := newPRWatcher()
	w.repos["p"] = &prRepo{live: map[int]github.WatchedPR{
		9: {Number: 9, HeadSHA: "abc", Mergeable: "conflicting", Checks: "failing", Review: "changes_requested"},
	}}
	pr := &api.PullRequest{Number: 9, State: "open", HeadSHA: "abc", Checks: "pending"}
	w.overlay("p", pr)
	if !pr.Watched || !pr.Conflict || pr.Checks != "failing" || pr.Review != "changes_requested" {
		t.Errorf("overlay = %+v", pr)
	}
	stale := &api.PullRequest{Number: 9, State: "open", HeadSHA: "def", Checks: "pending"}
	w.overlay("p", stale)
	if stale.Watched || stale.Conflict || stale.Checks != "pending" {
		t.Errorf("another head was overlaid: %+v", stale)
	}
}

func TestPRProblems(t *testing.T) {
	for _, c := range []struct {
		name string
		prev state.PRWatch
		pr   github.WatchedPR
		want string
	}{
		{"first sight of a failure", state.PRWatch{}, github.WatchedPR{HeadSHA: "a", Checks: "failing"}, "[checks]"},
		{"still failing", state.PRWatch{HeadSHA: "a", Checks: "failing"}, github.WatchedPR{HeadSHA: "a", Checks: "failing"}, "[]"},
		{"failing on a new head", state.PRWatch{HeadSHA: "a", Checks: "failing"}, github.WatchedPR{HeadSHA: "b", Checks: "failing"}, "[checks]"},
		{"pending", state.PRWatch{}, github.WatchedPR{HeadSHA: "a", Checks: "pending"}, "[]"},
		{"conflict", state.PRWatch{}, github.WatchedPR{Mergeable: "conflicting"}, "[conflict]"},
		{"still conflicting", state.PRWatch{Conflict: true}, github.WatchedPR{Mergeable: "conflicting"}, "[]"},
		{"changes requested", state.PRWatch{Review: "review_required"}, github.WatchedPR{Review: "changes_requested"}, "[review]"},
		{"still requested", state.PRWatch{Review: "changes_requested"}, github.WatchedPR{Review: "changes_requested"}, "[]"},
	} {
		if got := fmt.Sprint(prProblems(c.prev, c.pr)); got != c.want {
			t.Errorf("%s: prProblems = %s, want %s", c.name, got, c.want)
		}
	}
}
