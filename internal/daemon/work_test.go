package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// The Work page lists live agents' work and past agents', newest first, with
// what each waits for, its report, its media and a past agent's changes from
// its kept branch; the filters and the search narrow it, and the counts say
// how many each filter keeps.
func TestWorkListsLiveAndPastAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ready := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	working := addAgent(t, d, repo, "hello-stack", "agent-02", "Still at it")
	d.srv.record(ctx, createdEvent(working, "Do the thing", now))
	d.srv.record(ctx, finishedEvent(ready, api.AgentChanges{Files: 2, Insertions: 10, Deletions: 1}, &api.PullRequest{Number: 7, State: "open"}, "Paginated the reminders list.", now.Add(-time.Minute)))
	if _, err := d.srv.memory().AddReport(ctx, memory.Report{Project: "hello-stack", Agent: "agent-01", Status: memory.StatusPartial, Summary: "Paged it; sorting is left."}); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.AddMedia(ctx, state.Media{ID: "m1", Project: "hello-stack", Agent: "agent-01", Kind: "screenshot", Name: "after", File: "m1.png", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	// A past agent, archived an hour ago with a merged pull request, whose
	// branch is still in the repository.
	base := strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "HEAD"))
	head := strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	testutil.Git(t, repo, "checkout", "-q", "-b", "agentbox/agent-07")
	if err := os.WriteFile(filepath.Join(repo, "added.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "-A")
	testutil.Git(t, repo, "commit", "-q", "-m", "past work")
	testutil.Git(t, repo, "checkout", "-q", head)
	past := state.Agent{
		Project: "hello-stack", Name: "agent-07", Instance: "ab-hello-stack-agent-07", AI: "claude", Title: "Dark mode",
		Branch: "agentbox/agent-07", BaseRef: head, BaseCommit: base, Worktree: "/gone/agent-07",
		Status: state.AgentReady, CreatedAt: now.Add(-2 * time.Hour), Interface: state.InterfaceChat,
	}
	if err := d.srv.store.AddAgent(ctx, past); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.ArchiveAgent(ctx, past, "Add a dark theme", state.ArchivedPR{Number: 5, Title: "feat: dark mode", State: state.PRMerged}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	page := work(t, d, "/v1/projects/hello-stack/work")
	// One still at work sorts by when it was made, the others by when they finished.
	if len(page.Items) != 3 || page.Items[0].Item.Agent != "agent-02" || page.Items[1].Item.Agent != "agent-01" || page.Items[2].Item.Agent != "agent-07" {
		t.Fatalf("want the working agent, the finished one and the past one, newest first; got %+v", page.Items)
	}
	r := page.Items[1]
	if r.Status != api.WorkReview || r.Item.Fingerprint == "" || r.Report == nil || r.Report.Status != memory.StatusPartial || r.Screenshots != 1 || r.Item.Changes == nil || r.Item.Changes.Files != 2 {
		t.Errorf("the finished agent's row lost what it shows: %+v", r)
	}
	if w := page.Items[0]; w.Status != api.WorkWorking || w.Item.Fingerprint != "" {
		t.Errorf("the working agent's row = %+v", w)
	}
	p := page.Items[2]
	if !p.Past || p.Status != api.WorkDone || p.Task != "Add a dark theme" || p.Item.PR == nil || p.Item.PR.Number != 5 || p.Item.Changes == nil || p.Item.Changes.Files != 1 || p.Item.Changes.Insertions != 2 {
		t.Errorf("the past agent's row = %+v (changes %+v)", p, p.Item.Changes)
	}
	if page.Total != 3 || page.Counts[""] != 3 || page.Counts[api.WorkFilterReview] != 1 || page.Counts[api.WorkFilterOpen] != 1 || page.Counts[api.WorkFilterMerged] != 1 || page.Counts[api.WorkFilterNone] != 1 {
		t.Errorf("counts = %v, total %d", page.Counts, page.Total)
	}

	if got := work(t, d, "/v1/work?filter=review").Items; len(got) != 1 || got[0].Item.Agent != "agent-01" {
		t.Errorf("filter=review = %+v", got)
	}
	if got := work(t, d, "/v1/work?project=hello-stack&filter=merged").Items; len(got) != 1 || got[0].Item.Agent != "agent-07" {
		t.Errorf("filter=merged = %+v", got)
	}
	for _, q := range []string{"dark", "theme", "%235", "agent-07"} {
		if got := work(t, d, "/v1/work?q="+q); len(got.Items) != 1 || got.Items[0].Item.Agent != "agent-07" || got.Counts[""] != 1 {
			t.Errorf("q=%s = %+v", q, got.Items)
		}
	}
	// Paged, the cursor continues where the page ended.
	first := work(t, d, "/v1/work?limit=2")
	if len(first.Items) != 2 || first.Next == "" || first.Total != 3 {
		t.Fatalf("first page = %+v", first)
	}
	if rest := work(t, d, "/v1/work?limit=2&cursor="+first.Next); len(rest.Items) != 1 || rest.Items[0].Item.Agent != "agent-07" {
		t.Errorf("second page = %+v", rest.Items)
	}

	// A past agent's diff is its kept branch's.
	rec := httptest.NewRecorder()
	d.srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/archived-agents/hello-stack/agent-07/diff", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "+++ b/added.txt") {
		t.Errorf("past diff = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	d.srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/work?filter=nope", nil))
	if rec.Code == http.StatusOK {
		t.Error("an unknown filter was taken")
	}
}

func work(t *testing.T, d testDaemon, path string) api.WorkPage {
	t.Helper()
	rec := httptest.NewRecorder()
	d.srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body.String())
	}
	var page api.WorkPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

// A past agent's changes are measured once for where its kept branch is, not
// on every read of the page, and again once the branch moves.
func TestWorkMeasuresPastChangesOncePerCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "HEAD"))
	head := strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	commit := func(file string) {
		if err := os.WriteFile(filepath.Join(repo, file), []byte("a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, repo, "add", "-A")
		testutil.Git(t, repo, "commit", "-q", "-m", file)
	}
	testutil.Git(t, repo, "checkout", "-q", "-b", "agentbox/agent-07")
	commit("one.txt")
	testutil.Git(t, repo, "checkout", "-q", head)
	past := state.Agent{
		Project: "hello-stack", Name: "agent-07", Instance: "ab-hello-stack-agent-07", AI: "claude", Title: "Dark mode",
		Branch: "agentbox/agent-07", BaseRef: head, BaseCommit: base, Worktree: "/gone/agent-07",
		Status: state.AgentReady, CreatedAt: time.Now().Add(-time.Hour), Interface: state.InterfaceChat,
	}
	if err := d.srv.store.AddAgent(ctx, past); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.ArchiveAgent(ctx, past, "", state.ArchivedPR{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	files := func() int {
		page := work(t, d, "/v1/work")
		if len(page.Items) != 1 || page.Items[0].Item.Changes == nil {
			t.Fatalf("page = %+v", page.Items)
		}
		return page.Items[0].Item.Changes.Files
	}
	measured := func() int {
		d.srv.mu.Lock()
		defer d.srv.mu.Unlock()
		return len(d.srv.pastChanges)
	}
	if got := files(); got != 1 || files() != 1 || measured() != 1 {
		t.Fatalf("files = %d, measured %d times; want 1 file, measured once", got, measured())
	}
	// The branch pushed on after the agent was archived.
	testutil.Git(t, repo, "checkout", "-q", "agentbox/agent-07")
	commit("two.txt")
	testutil.Git(t, repo, "checkout", "-q", head)
	if got := files(); got != 2 || measured() != 2 {
		t.Errorf("after the branch moved: files = %d, measured %d; want 2 and 2", got, measured())
	}
}
