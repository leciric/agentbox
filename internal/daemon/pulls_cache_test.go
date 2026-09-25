package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	// byCommit is the body GitHub answers for the pull requests of a commit;
	// a commit missing from it was never pushed.
	byCommit map[string]string
}

func newSlowGitHub(t *testing.T, d testDaemon, list string, delay time.Duration) *slowGitHub {
	t.Helper()
	g := &slowGitHub{calls: map[string]int{}, list: list, delay: delay, byCommit: map[string]string{}}
	stub := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(stub.Close)
	d.setGitHub(t, stub.URL)
	return g
}

func (g *slowGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	what, delay, list := r.URL.Path, g.delay, g.list
	g.calls[what]++
	commit, isCommit := strings.CutPrefix(r.URL.Path, "/repos/acme/hello-stack/commits/")
	commit, _ = strings.CutSuffix(commit, "/pulls")
	byCommit, known := g.byCommit[commit]
	g.mu.Unlock()

	switch {
	case isCommit && known:
		w.Write([]byte(byCommit))
	case isCommit:
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprintf(w, `{"message":"No commit found for SHA: %s"}`, commit)
	case strings.HasSuffix(r.URL.Path, "/check-runs"):
		w.Write([]byte(`{"total_count":0}`))
	case strings.Contains(r.URL.Path, "/pulls/"):
		w.Write([]byte(`{"additions":1,"deletions":0,"comments":0}`))
	case strings.HasSuffix(r.URL.Path, "/pulls"):
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

func (g *slowGitHub) setCommit(commit, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.byCommit[commit] = body
}

// pullsList is a list page with one pull request per branch given, whose
// heads are commits no agent made.
func pullsList(branches ...string) string {
	var heads []listedPR
	for i, branch := range branches {
		heads = append(heads, listedPR{10 + i, branch, fmt.Sprintf("sha-%d", i)})
	}
	return pullsPage(heads...)
}

// listedPR is a pull request on a stub list page: its number, the branch it
// was pushed to, and the commit that branch is at.
type listedPR struct {
	number      int
	branch, sha string
}

// pullsPage is a list page of these pull requests, most recently updated
// first.
func pullsPage(prs ...listedPR) string {
	var items []string
	for i, pr := range prs {
		items = append(items, fmt.Sprintf(
			`{"number":%d,"title":"Work on %s","state":"open","html_url":"https://github.com/acme/hello-stack/pull/%d","draft":false,"updated_at":"2026-09-%02dT10:00:00Z","base":{"ref":"main"},"head":{"ref":%q,"sha":%q}}`,
			pr.number, pr.branch, pr.number, 28-i, pr.branch, pr.sha))
	}
	return "[" + strings.Join(items, ",") + "]"
}

// commitOn commits a change on an agent's branch, the way the agent would,
// and returns the commit.
func commitOn(t *testing.T, a state.Agent, file string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(a.Worktree, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, a.Worktree, "add", file)
	testutil.Git(t, a.Worktree, "commit", "--quiet", "-m", "add "+file)
	return testutil.Git(t, a.Worktree, "rev-parse", "HEAD")
}

// testClock makes the pull request cache's idea of now movable, so a test can
// age an entry past its TTL instead of waiting half a minute for it.
func testClock(d testDaemon) *atomic.Int64 {
	var offset atomic.Int64
	d.srv.pulls.now = func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	return &offset
}

// pullsProject sets up a project with a GitHub remote, a token, and agents.
func pullsProject(t *testing.T, d testDaemon, names ...string) (string, map[string]state.Agent) {
	t.Helper()
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)
	agents := map[string]state.Agent{}
	for _, name := range names {
		agents[name] = addAgent(t, d, repo, "hello-stack", name, "")
	}
	return repo, agents
}

// The tab and the fleet are answered from the cache, and GitHub is re-read
// behind the answer. The first request for a repository has nothing to show
// and says so; every one after it shows the last answer at once, however old.
func TestPullRequestsAreServedStaleWhileGitHubIsReRead(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	offset := testClock(d)
	gh := newSlowGitHub(t, d, pullsList("agentbox/agent-01"), 700*time.Millisecond)
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
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newSlowGitHub(t, d, pullsList("agentbox/agent-01"), 500*time.Millisecond)
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
// agents against the one list the cache already holds, and only looks up an
// agent the list page has nothing for — once, not on every poll. The match is
// by the agent's commits, never by the branch name: an agent's work is pushed
// under whatever name suits it (feat/…, fix/…), and an old pull request from
// a branch whose name is being reused isn't the agent's.
func TestFleetMatchesAgentsAgainstOneList(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newSlowGitHub(t, d, "[]", 0)
	_, agents := pullsProject(t, d, "agent-01", "agent-02", "agent-03", "agent-04")

	one := commitOn(t, agents["agent-01"], "one.txt")
	pushed := commitOn(t, agents["agent-02"], "two.txt")
	commitOn(t, agents["agent-02"], "two-again.txt") // committed again after its work was pushed
	three := commitOn(t, agents["agent-03"], "three.txt")
	// agent-04 hasn't committed anything.
	gh.setList(pullsPage(
		listedPR{12, "feat/one-account-per-project", one},
		listedPR{13, "fix/windows-setup-flicker", pushed},
		// A pull request from long ago, on a branch of the same name as
		// agent-03's, and nothing to do with it.
		listedPR{3, agents["agent-03"].Branch, "0123456789abcdef0123456789abcdef01234567"},
	))

	pullsOf(t, d, "hello-stack") // wait out the first read
	fleet, err := d.client.Fleet(context.Background(), "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*api.PullRequest{}
	for _, a := range fleet.Agents {
		byName[a.Name] = a.PR
	}
	if byName["agent-01"] == nil || byName["agent-01"].Number != 12 {
		t.Errorf("agent-01's pull request = %+v, want #12, pushed to feat/one-account-per-project", byName["agent-01"])
	}
	if byName["agent-02"] == nil || byName["agent-02"].Number != 13 {
		t.Errorf("agent-02's pull request = %+v, want #13, which has all but its newest commit", byName["agent-02"])
	}
	if byName["agent-03"] != nil {
		t.Errorf("agent-03 has no pull request, got %+v from a branch of the same name", byName["agent-03"])
	}
	if byName["agent-04"] != nil {
		t.Errorf("agent-04 has no commits, so no pull request, got %+v", byName["agent-04"])
	}
	if n := gh.count("/repos/acme/hello-stack/pulls"); n != 1 {
		t.Errorf("the list was read %d times for four agents, want 1", n)
	}
	// Only the agent the list had nothing for was looked up, by its commit;
	// the one without commits has nothing to look up.
	for _, commit := range []string{one, pushed} {
		if n := gh.count("/repos/acme/hello-stack/commits/" + commit + "/pulls"); n != 0 {
			t.Errorf("%s is in the list, but was looked up %d times", commit, n)
		}
	}
	asked := "/repos/acme/hello-stack/commits/" + three + "/pulls"
	if n := gh.count(asked); n != 1 {
		t.Errorf("agent-03's commit was looked up %d times, want 1", n)
	}

	// Polling again doesn't ask again: "this agent has no pull request" is
	// remembered, and one opened later arrives in the list itself.
	for range 3 {
		if _, err := d.client.Fleet(context.Background(), "hello-stack"); err != nil {
			t.Fatal(err)
		}
	}
	if n := gh.count(asked); n != 1 {
		t.Errorf("three more polls looked agent-03's commit up %d times in total, want 1", n)
	}

	// A new commit is a new question.
	four := commitOn(t, agents["agent-03"], "four.txt")
	d.client.Fleet(context.Background(), "hello-stack")
	waitFor(t, "a lookup of the new commit", func() bool {
		return gh.count("/repos/acme/hello-stack/commits/"+four+"/pulls") == 1
	})
}

// A pull request the list page doesn't carry — older than the whole page, or
// one whose branch has moved past the agent's commits because somebody pushed
// a fix to it from elsewhere — is still found, by asking GitHub which pull
// requests carry the agent's commits.
func TestFleetLooksUpAPullRequestByTheAgentsCommits(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newSlowGitHub(t, d, "[]", 0)
	_, agents := pullsProject(t, d, "agent-01", "agent-02")

	old := commitOn(t, agents["agent-01"], "old.txt")
	gh.setCommit(old, `[{"number":4,"title":"Old work","state":"closed","merged_at":"2026-09-02T10:00:00Z","html_url":"https://github.com/acme/hello-stack/pull/4","updated_at":"2026-09-02T10:00:00Z","head":{"ref":"feat/old","sha":"`+old+`"}}]`)
	// agent-02's newest commit was never pushed, and the branch it went to
	// has a commit on top that isn't the agent's at all.
	fixed := commitOn(t, agents["agent-02"], "fixed.txt")
	commitOn(t, agents["agent-02"], "unpushed.txt")
	gh.setCommit(fixed, `[{"number":8,"title":"Fixed","state":"open","html_url":"https://github.com/acme/hello-stack/pull/8","updated_at":"2026-09-20T10:00:00Z","head":{"ref":"fix/conflicts","sha":"fedcba9876543210fedcba9876543210fedcba98"}}]`)

	pullsOf(t, d, "hello-stack")
	var byName map[string]*api.PullRequest
	waitFor(t, "both agents' pull requests", func() bool {
		fleet, err := d.client.Fleet(context.Background(), "hello-stack")
		if err != nil {
			t.Fatal(err)
		}
		byName = map[string]*api.PullRequest{}
		for _, a := range fleet.Agents {
			byName[a.Name] = a.PR
		}
		return byName["agent-01"] != nil && byName["agent-02"] != nil
	})
	if pr := byName["agent-01"]; pr.Number != 4 || pr.State != "merged" {
		t.Errorf("agent-01's pull request = %+v, want #4, merged", pr)
	}
	if pr := byName["agent-02"]; pr.Number != 8 {
		t.Errorf("agent-02's pull request = %+v, want #8", pr)
	}
}

// An agent whose work was merged into its base branch with a merge commit
// has no commits of its own any more, as far as the base branch goes — but
// the pull request whose head is exactly where its branch is, is still its.
// One that merely carries that commit, because the agent only caught up with
// main, is not.
func TestAgentHeadsAfterAMerge(t *testing.T) {
	t.Parallel()
	testutil.GitEnv(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	base := testutil.Git(t, repo, "rev-parse", "HEAD")
	testutil.Git(t, repo, "branch", "agentbox/merged", base)
	testutil.Git(t, repo, "branch", "agentbox/caught-up", base)

	worktree := filepath.Join(t.TempDir(), "merged")
	testutil.Git(t, repo, "worktree", "add", "--quiet", worktree, "agentbox/merged")
	merged := commitOn(t, state.Agent{Worktree: worktree}, "merged.txt")
	testutil.Git(t, repo, "merge", "--quiet", "--no-ff", "-m", "Merge #5", "agentbox/merged")
	testutil.Git(t, repo, "branch", "-f", "agentbox/caught-up", "main")

	heads := agentHeads(repo, []state.Agent{
		{Name: "merged", Branch: "agentbox/merged", BaseRef: "main", BaseCommit: base},
		{Name: "caught-up", Branch: "agentbox/caught-up", BaseRef: "main", BaseCommit: base},
		{Name: "lead", Role: state.RoleLead, Branch: "main"},
	})
	if len(heads) != 2 {
		t.Fatalf("agentHeads() = %+v, want the two workers", heads)
	}
	if h := heads[0]; h.tip != merged || len(h.own) != 0 {
		t.Errorf("merged agent's head = %+v, want tip %s and nothing of its own", h, merged)
	}
	pr5 := api.PullRequest{Number: 5, HeadSHA: merged}
	if !heads[0].accepts(pr5, merged) {
		t.Error("the merged agent's own pull request wasn't accepted")
	}
	caughtUp := heads[1]
	if caughtUp.tip == merged || caughtUp.owns(merged) {
		t.Errorf("caught-up agent's head = %+v: it doesn't own what it caught up with", caughtUp)
	}
	if caughtUp.accepts(pr5, caughtUp.tip) {
		t.Error("an agent that only caught up with main was given a pull request merged into it")
	}
}

// A refresh that finds something new says so on the event stream, so the app
// redraws then rather than at its next poll. One that finds nothing new says
// nothing: an event per poll would be the polling it replaces.
func TestPullRequestRefreshAnnouncesWhatMoved(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	offset := testClock(d)
	gh := newSlowGitHub(t, d, pullsList("agentbox/agent-01"), 0)
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
func TestPullsCacheRefreshesForAnAgentItDoesntKnow(t *testing.T) {
	t.Parallel()
	c := newPullsCache()
	one := agentHead{agent: "agent-01", tip: "b1", own: []string{"b1", "a1"}}
	two := agentHead{agent: "agent-02", tip: "b2", own: []string{"b2"}}
	idle := agentHead{agent: "agent-03"} // no commits: nothing to find
	gen, _ := c.claim("acme/x", []agentHead{one})
	c.finish("acme/x", gen, pullsEntry{
		prs:     []api.PullRequest{{Number: 1, HeadBranch: "feat/anything", HeadSHA: "a1"}},
		lookups: map[string]lookedUp{},
	})
	if _, ok := c.claim("acme/x", []agentHead{one, idle}); ok {
		t.Error("an agent the list carries was treated as unknown")
	}
	if _, ok := c.claim("acme/x", []agentHead{one, two}); !ok {
		t.Error("an agent nothing has been read about didn't start a refresh")
	}
}

// A GitHub that refuses the list must not turn into a call per request: an
// unknown branch is only worth re-reading for when the last read worked.
func TestPullsCacheDoesNotRetryAFailedListPerRequest(t *testing.T) {
	t.Parallel()
	c := newPullsCache()
	one := []agentHead{{agent: "agent-01", tip: "b1", own: []string{"b1"}}}
	gen, _ := c.claim("acme/x", one)
	c.finish("acme/x", gen, pullsEntry{listErr: newPullsErr(errors.New("GitHub 502"))})
	if _, ok := c.claim("acme/x", one); ok {
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
