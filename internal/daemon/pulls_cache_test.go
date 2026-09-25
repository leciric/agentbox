package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// slowGitHub is a stub GitHub that takes its time over the pull request list,
// the way the real one does, and counts what was asked of it. Holding the
// answer back is the whole point: what the daemon does while GitHub hasn't
// answered is what this file is about.
type slowGitHub struct {
	mu    sync.Mutex
	calls map[string]int
	list  string // the body of the pull request list
	delay time.Duration
}

func newSlowGitHub(t *testing.T, list string, delay time.Duration) *slowGitHub {
	t.Helper()
	g := &slowGitHub{calls: map[string]int{}, list: list, delay: delay}
	stub := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(stub.Close)
	t.Setenv("AGENTBOX_GITHUB_API", stub.URL)
	return g
}

func (g *slowGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	what, delay, list := r.URL.Path, g.delay, g.list
	if head := r.URL.Query().Get("head"); head != "" {
		what += "?head=" + head
	}
	g.calls[what]++
	g.mu.Unlock()

	switch {
	case strings.HasSuffix(r.URL.Path, "/check-runs"):
		w.Write([]byte(`{"total_count":0}`))
	case strings.Contains(r.URL.Path, "/pulls/"):
		w.Write([]byte(`{"additions":1,"deletions":0,"comments":0}`))
	case strings.HasSuffix(r.URL.Path, "/pulls"):
		if r.URL.Query().Get("head") != "" {
			// The fallback for a branch the list page didn't carry: in these
			// tests, a branch outside the list has no pull request at all.
			w.Write([]byte(`[]`))
			return
		}
		time.Sleep(delay)
		w.Write([]byte(list))
	default:
		w.Write([]byte(`{"default_branch":"main","allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":true,"permissions":{"push":true}}`))
	}
}

func (g *slowGitHub) count(what string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[what]
}

func (g *slowGitHub) setList(list string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.list = list
}

// pullsList is a list page with one pull request per branch given.
func pullsList(branches ...string) string {
	var items []string
	for i, branch := range branches {
		items = append(items, fmt.Sprintf(
			`{"number":%d,"title":"Work on %s","state":"open","html_url":"https://github.com/acme/hello-stack/pull/%d","draft":false,"updated_at":"2026-09-1%dT10:00:00Z","base":{"ref":"main"},"head":{"ref":%q,"sha":"sha-%d"}}`,
			10+i, branch, 10+i, i, branch, i))
	}
	return "[" + strings.Join(items, ",") + "]"
}

// testClock makes the pull request cache's idea of now movable, so a test can
// age an entry past its TTL instead of waiting half a minute for it.
func testClock(d testDaemon) *atomic.Int64 {
	var offset atomic.Int64
	d.srv.pulls.now = func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	return &offset
}

// pullsProject sets up a project with a GitHub remote, a token, and agents.
func pullsProject(t *testing.T, d testDaemon, names ...string) string {
	t.Helper()
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)
	for _, name := range names {
		addAgent(t, d, repo, "hello-stack", name, "")
	}
	return repo
}

// The tab and the fleet are answered from the cache, and GitHub is re-read
// behind the answer. The first request for a repository has nothing to show
// and says so; every one after it shows the last answer at once, however old.
func TestPullRequestsAreServedStaleWhileGitHubIsReRead(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	offset := testClock(d)
	gh := newSlowGitHub(t, pullsList("agentbox/agent-01"), 700*time.Millisecond)
	pullsProject(t, d, "agent-01")

	// The first request doesn't wait for GitHub: it says it has nothing yet,
	// and that it is reading.
	start := time.Now()
	first, err := d.client.ProjectPullRequests(context.Background(), "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 300*time.Millisecond {
		t.Errorf("the first request waited %v for GitHub; it should answer at once", took)
	}
	if !first.Refreshing || first.FetchedAt != nil || len(first.PullRequests) != 0 {
		t.Errorf("first request = %+v, want an empty answer that says it's refreshing", first)
	}
	if first.GitHub != "acme/hello-stack" {
		t.Errorf("GitHub = %q, want the repository named even before GitHub answers", first.GitHub)
	}

	// Once GitHub has answered, the list is there, with when it was read.
	out := pullsOf(t, d, "hello-stack")
	if len(out.PullRequests) != 1 || out.PullRequests[0].Number != 10 {
		t.Fatalf("after the first read = %+v", out.PullRequests)
	}
	if out.FetchedAt == nil || out.Refreshing {
		t.Errorf("FetchedAt/Refreshing = %v/%v, want a read that has settled", out.FetchedAt, out.Refreshing)
	}

	// GitHub moves on, and the entry goes stale. The next request still
	// answers at once — with what it had.
	gh.setList(pullsList("agentbox/agent-01", "agentbox/agent-02"))
	offset.Store(int64(2 * pullsTTL))
	start = time.Now()
	stale, err := d.client.ProjectPullRequests(context.Background(), "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 300*time.Millisecond {
		t.Errorf("a stale request waited %v for GitHub; it should serve what it has", took)
	}
	if len(stale.PullRequests) != 1 || !stale.Refreshing || stale.FetchedAt == nil {
		t.Errorf("stale request = %+v, want the old list, refreshing, with its age", stale)
	}

	// And the new list arrives behind it.
	waitFor(t, "the refreshed list", func() bool {
		out, err := d.client.ProjectPullRequests(context.Background(), "hello-stack")
		return err == nil && len(out.PullRequests) == 2
	})
}

// Requests pile up on a tab that polls every few seconds; they must not pile
// GitHub calls up behind them.
func TestPullRequestRefreshIsSingleFlight(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newSlowGitHub(t, pullsList("agentbox/agent-01"), 500*time.Millisecond)
	pullsProject(t, d, "agent-01")

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := d.client.ProjectPullRequests(context.Background(), "hello-stack"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	pullsOf(t, d, "hello-stack")
	if n := gh.count("/repos/acme/hello-stack/pulls"); n != 1 {
		t.Errorf("20 requests made %d list calls, want 1", n)
	}
}

// The fleet used to ask GitHub once per agent branch. It now matches the
// agents against the one list the cache already holds, and only asks about a
// branch the list page doesn't carry — once, not on every poll.
func TestFleetMatchesAgentsAgainstOneList(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newSlowGitHub(t, pullsList("agentbox/agent-01", "agentbox/agent-02"), 0)
	pullsProject(t, d, "agent-01", "agent-02", "agent-03")

	pullsOf(t, d, "hello-stack") // wait out the first read
	fleet, err := d.client.Fleet(context.Background(), "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*api.PullRequest{}
	for _, a := range fleet.Agents {
		byName[a.Name] = a.PR
	}
	if byName["agent-01"] == nil || byName["agent-01"].Number != 10 {
		t.Errorf("agent-01's pull request = %+v, want #10 from the list", byName["agent-01"])
	}
	if byName["agent-02"] == nil || byName["agent-02"].Number != 11 {
		t.Errorf("agent-02's pull request = %+v, want #11 from the list", byName["agent-02"])
	}
	if byName["agent-03"] != nil {
		t.Errorf("agent-03 has no pull request, got %+v", byName["agent-03"])
	}
	if n := gh.count("/repos/acme/hello-stack/pulls"); n != 1 {
		t.Errorf("the list was read %d times for three agents, want 1", n)
	}
	// Only the branch the list didn't carry was asked about by name.
	for _, branch := range []string{"agentbox/agent-01", "agentbox/agent-02"} {
		if n := gh.count("/repos/acme/hello-stack/pulls?head=acme:" + branch); n != 0 {
			t.Errorf("%s is in the list, but was asked about by name %d times", branch, n)
		}
	}
	asked := "/repos/acme/hello-stack/pulls?head=acme:agentbox/agent-03"
	if n := gh.count(asked); n != 1 {
		t.Errorf("agent-03's branch was asked about %d times, want 1", n)
	}

	// Polling again doesn't ask again: "this branch has no pull request" is
	// remembered, and one opened later arrives in the list itself.
	for range 3 {
		if _, err := d.client.Fleet(context.Background(), "hello-stack"); err != nil {
			t.Fatal(err)
		}
	}
	if n := gh.count(asked); n != 1 {
		t.Errorf("three more polls asked about agent-03's branch %d times in total, want 1", n)
	}
}

// A refresh that finds something new says so on the event stream, so the app
// redraws then rather than at its next poll. One that finds nothing new says
// nothing: an event per poll would be the polling it replaces.
func TestPullRequestRefreshAnnouncesWhatMoved(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	offset := testClock(d)
	gh := newSlowGitHub(t, pullsList("agentbox/agent-01"), 0)
	pullsProject(t, d, "agent-01")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan api.PullsChange, 32)
	go d.client.Events(ctx, func(ev api.Event) error {
		var change api.PullsChange
		if ev.Type == api.EventPulls && json.Unmarshal(ev.Data, &change) == nil {
			events <- change
		}
		return nil
	})
	// The stream has to be there before the first request, or the event it
	// causes has nobody to reach.
	waitFor(t, "the event stream", func() bool { return d.srv.events.subscribers() > 0 })

	if _, err := d.client.ProjectPullRequests(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	select {
	case change := <-events:
		if change.Project != "hello-stack" || change.GitHub != "acme/hello-stack" || change.FetchedAt.IsZero() {
			t.Errorf("first event = %+v", change)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first read never announced itself")
	}

	// A refresh that finds the same list again is not news.
	offset.Store(int64(2 * pullsTTL))
	if _, err := d.client.ProjectPullRequests(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second read", func() bool { return gh.count("/repos/acme/hello-stack/pulls") == 2 })
	select {
	case change := <-events:
		t.Errorf("nothing moved, but an event went out: %+v", change)
	case <-time.After(500 * time.Millisecond):
	}

	// A refresh that finds a new pull request is.
	gh.setList(pullsList("agentbox/agent-01", "agentbox/agent-02"))
	offset.Store(int64(4 * pullsTTL))
	if _, err := d.client.ProjectPullRequests(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
	case <-time.After(10 * time.Second):
		t.Fatal("a new pull request never announced itself")
	}
}

// One refresh at a time per repository, and a stale entry starts one.
func TestPullsCacheClaimsOneRefreshAtATime(t *testing.T) {
	t.Parallel()
	c := newPullsCache()
	var now atomic.Int64
	c.now = func() time.Time { return time.Unix(now.Load(), 0) }

	gen, ok := c.claim("acme/x", nil)
	if !ok {
		t.Fatal("an empty cache refused to refresh")
	}
	if _, ok := c.claim("acme/x", nil); ok {
		t.Error("a second refresh was claimed while the first was still running")
	}
	if _, changed := c.finish("acme/x", gen, pullsEntry{prs: []api.PullRequest{{Number: 1}}}); !changed {
		t.Error("the first answer wasn't news")
	}
	if _, ok := c.claim("acme/x", nil); ok {
		t.Error("a fresh entry was refreshed anyway")
	}

	now.Add(int64(c.ttl/time.Second) + 1)
	gen, ok = c.claim("acme/x", nil)
	if !ok {
		t.Fatal("a stale entry wasn't refreshed")
	}
	if _, changed := c.finish("acme/x", gen, pullsEntry{prs: []api.PullRequest{{Number: 1}}}); changed {
		t.Error("the same answer was reported as a change")
	}
	if entry, refreshing := c.state("acme/x"); refreshing || len(entry.prs) != 1 {
		t.Errorf("state() = %+v / %v after the refresh settled", entry.prs, refreshing)
	}
}

// A merge invalidates the repository. A refresh that was already in flight
// must not put the pre-merge list back over it.
func TestPullsCacheDropsARefreshAMergeOvertook(t *testing.T) {
	t.Parallel()
	c := newPullsCache()
	gen, _ := c.claim("acme/x", nil)
	c.invalidate("acme/x")
	if _, changed := c.finish("acme/x", gen, pullsEntry{prs: []api.PullRequest{{Number: 1, State: "open"}}}); changed {
		t.Error("a refresh that a merge overtook was stored anyway")
	}
	if entry, _ := c.state("acme/x"); len(entry.prs) != 0 {
		t.Errorf("the pre-merge list came back: %+v", entry.prs)
	}
	if _, ok := c.claim("acme/x", nil); !ok {
		t.Error("the repository was left unable to refresh")
	}
}

// A new agent shouldn't wait out the TTL to find out it has a pull request:
// a branch the cached answer says nothing about is reason enough to re-read.
func TestPullsCacheRefreshesForABranchItDoesntKnow(t *testing.T) {
	t.Parallel()
	c := newPullsCache()
	gen, _ := c.claim("acme/x", []string{"agentbox/agent-01"})
	c.finish("acme/x", gen, pullsEntry{
		prs:      []api.PullRequest{{Number: 1, HeadBranch: "agentbox/agent-01"}},
		branches: map[string]branchPR{},
	})
	if _, ok := c.claim("acme/x", []string{"agentbox/agent-01"}); ok {
		t.Error("a branch the list carries was treated as unknown")
	}
	if _, ok := c.claim("acme/x", []string{"agentbox/agent-01", "agentbox/agent-02"}); !ok {
		t.Error("a branch nothing has been read about didn't start a refresh")
	}
}

// A GitHub that refuses the list must not turn into a call per request: an
// unknown branch is only worth re-reading for when the last read worked.
func TestPullsCacheDoesNotRetryAFailedListPerRequest(t *testing.T) {
	t.Parallel()
	c := newPullsCache()
	gen, _ := c.claim("acme/x", []string{"agentbox/agent-01"})
	c.finish("acme/x", gen, pullsEntry{listErr: newPullsErr(errors.New("GitHub 502"))})
	if _, ok := c.claim("acme/x", []string{"agentbox/agent-01"}); ok {
		t.Error("a failed read was retried straight away, once per request")
	}
}

// Fifteen agents are fifteen git diffs, on a tab that polls. They run at the
// same time rather than one after another: see the benchmark below.
func TestAgentChangesMeasuresEveryAgent(t *testing.T) {
	t.Parallel()
	agents := make([]state.Agent, 15)
	for i := range agents {
		agents[i] = state.Agent{Name: fmt.Sprintf("agent-%02d", i)}
	}
	var running, peak atomic.Int64
	out := agentChanges(agents, func(a state.Agent) api.AgentChanges {
		n := running.Add(1)
		for {
			was := peak.Load()
			if n <= was || peak.CompareAndSwap(was, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		var files int
		fmt.Sscanf(a.Name, "agent-%d", &files)
		return api.AgentChanges{Files: files}
	})
	for i, changes := range out {
		if changes.Files != i {
			t.Fatalf("agent %d got %d files: the answers came back in the wrong order", i, changes.Files)
		}
	}
	if peak.Load() < 2 {
		t.Errorf("the diffs ran one at a time (peak %d at once)", peak.Load())
	}
	if peak.Load() > changesConcurrency {
		t.Errorf("%d diffs ran at once, more than the bound of %d", peak.Load(), changesConcurrency)
	}
}

// BenchmarkAgentChanges is the fleet's diffstat for fifteen agents, against a
// fake git that takes as long as a real one on a warm worktree (~20 ms), one
// after another and as the fleet now runs it.
func BenchmarkAgentChanges(b *testing.B) {
	agents := make([]state.Agent, 15)
	for i := range agents {
		agents[i] = state.Agent{Name: fmt.Sprintf("agent-%02d", i)}
	}
	measure := func(state.Agent) api.AgentChanges {
		time.Sleep(20 * time.Millisecond)
		return api.AgentChanges{Files: 1}
	}
	b.Run("one at a time", func(b *testing.B) {
		for b.Loop() {
			for _, a := range agents {
				measure(a)
			}
		}
	})
	b.Run("concurrently", func(b *testing.B) {
		for b.Loop() {
			agentChanges(agents, measure)
		}
	})
}
