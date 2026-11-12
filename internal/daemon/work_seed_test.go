package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// seedWork fills d with months of history, as an install that has been used
// for a while has: projects projects, and past agents spread over them and
// over the last 180 days, most with a pull request, a verdict, a report,
// media and a kept branch that changed a file. It answers the projects'
// names.
func seedWork(t *testing.T, d testDaemon, projects, agents int) []string {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	var names []string
	roots := map[string]string{}
	bases := map[string]string{}
	for p := range projects {
		name := fmt.Sprintf("project-%d", p+1)
		repo := d.fixtureRepo(t, "hello-stack")
		if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo, Name: name}); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
		roots[name] = repo
		bases[name] = strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "HEAD"))
	}
	// Every past agent's branch, one commit on the project's first: written
	// with one git fast-import per repository rather than a commit each.
	imports := map[string]*bytes.Buffer{}
	for _, name := range names {
		imports[name] = &bytes.Buffer{}
	}
	states := []string{state.PRMerged, state.PRMerged, state.PRMerged, state.PRMerged, state.PRMerged, state.PRMerged, state.PRClosed, state.PROpen, "", ""}
	for i := range agents {
		project := names[i%projects]
		name := fmt.Sprintf("agent-%04d", i+1)
		branch := "agentbox/" + name
		// Newest first by number, a few a day back to 180 days ago.
		at := now.Add(-time.Duration(i) * 180 * 24 * time.Hour / time.Duration(agents)).Add(-time.Hour)
		a := state.Agent{
			Project: project, Name: name, ID: name, Title: fmt.Sprintf("Change %d: the reminders list pages and sorts", i+1),
			AI: []string{"claude", "claude", "codex", "opencode"}[i%4], Interface: state.InterfaceChat,
			Branch: branch, BaseRef: "main", BaseCommit: bases[project], CreatedAt: at.Add(-2 * time.Hour),
		}
		if i%17 == 3 && i > 0 {
			a.BaseRef = fmt.Sprintf("agentbox/agent-%04d", i) // stacked on the one before it in its project's list
		}
		var pr state.ArchivedPR
		if s := states[i%len(states)]; s != "" {
			pr = state.ArchivedPR{Number: 1000 + i, Title: fmt.Sprintf("feat: the reminders list pages and sorts (%d)", i+1), URL: fmt.Sprintf("https://github.com/acme/%s/pull/%d", project, 1000+i), State: s}
		}
		task := strings.Repeat(fmt.Sprintf("The reminders page takes long to load with %d reminders: page it, keep the filters, and add tests. ", i+1), 4)
		if err := d.srv.store.ArchiveAgent(ctx, a, task, pr, at); err != nil {
			t.Fatal(err)
		}
		if i%5 != 0 {
			if err := d.srv.store.SetAgentReview(ctx, state.AgentReview{Project: project, Agent: name, Verdict: api.ReviewReviewed, Fingerprint: "f", At: at.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
		}
		if i%10 != 9 {
			if _, err := d.srv.memory().AddReport(ctx, memory.Report{
				Project: project, Agent: name, Task: "Page the reminders list", Status: []string{memory.StatusDone, memory.StatusDone, memory.StatusPartial}[i%3],
				Summary:         strings.Repeat("The list answers its first page from an index and the app loads the rest as it scrolls. ", 5),
				Discoveries:     []string{strings.Repeat("The count was a query for each row. ", 4), strings.Repeat("The refetch read every loaded page. ", 4)},
				Decisions:       []string{strings.Repeat("Keyset paging rather than offsets, so a new row never shifts a page. ", 3)},
				RemainingIssues: []string{"Sorting by title is left."},
			}); err != nil {
				t.Fatal(err)
			}
		}
		for m := range i % 5 {
			kind := "screenshot"
			if m == 3 {
				kind = "recording"
			}
			if err := d.srv.store.AddMedia(ctx, state.Media{ID: fmt.Sprintf("m-%d-%d", i, m), Project: project, Agent: name, Kind: kind, Name: fmt.Sprintf("shot-%d", m), File: fmt.Sprintf("m-%d-%d.png", i, m), Size: 1000, CreatedAt: at, OrphanedAt: at}); err != nil {
				t.Fatal(err)
			}
		}
		body := fmt.Sprintf("line %d\n", i)
		fmt.Fprintf(imports[project], "commit refs/heads/%s\ncommitter Dev <dev@example.com> %d +0000\ndata 5\nwork\nfrom %s\nM 100644 inline changed-%d.txt\ndata %d\n%s\n",
			branch, at.Unix(), bases[project], i, len(body), body)
	}
	for _, name := range names {
		cmd := exec.Command("git", "fast-import", "--quiet")
		cmd.Dir, cmd.Stdin = roots[name], imports[name]
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fast-import: %v\n%s", err, out)
		}
	}
	return names
}

// TestWorkSeeded measures the Work page against a seeded history, when
// AGENTBOX_WORK_SEED says how many past agents (5000 is months of use), and
// with AGENTBOX_WORK_SERVE=<directory> then serves it on
// <directory>/data/run/agentbox.sock until interrupted, for the apps:
//
//	AGENTBOX_WORK_SEED=5000 go test ./internal/daemon -run TestWorkSeeded -v
func TestWorkSeeded(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("AGENTBOX_WORK_SEED"))
	if n == 0 {
		t.Skip("set AGENTBOX_WORK_SEED to how many past agents to seed")
	}
	root := os.Getenv("AGENTBOX_WORK_SERVE")
	if root == "" {
		root = t.TempDir()
	}
	d := startTestDaemon(t, root, fakeIncus)
	started := time.Now()
	names := seedWork(t, d, 6, n)
	t.Logf("seeded %d past agents in %d projects in %s", n, len(names), time.Since(started).Round(time.Millisecond))
	get := func(label, path string) api.WorkPage {
		var page api.WorkPage
		var size int
		best := time.Hour
		var first time.Duration
		for i := range 5 {
			rec := httptest.NewRecorder()
			began := time.Now()
			d.srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			took := time.Since(began)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body.String())
			}
			if i == 0 {
				first = took
			}
			best = min(best, took)
			size = rec.Body.Len()
			page = api.WorkPage{}
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("%-28s first %8s  then %8s  %7d bytes  %d items of %d", label, first.Round(10*time.Microsecond), best.Round(10*time.Microsecond), size, len(page.Items), page.Total)
		return page
	}
	first := get("every project, page 1", "/v1/work?limit=50")
	get("every project, page 2", "/v1/work?limit=50&cursor="+first.Next)
	get("one project, page 1", "/v1/projects/"+names[0]+"/work?limit=50")
	get("filter merged", "/v1/work?limit=50&filter=merged")
	get("search", "/v1/work?limit=50&q=reminders")
	get("search a number", "/v1/work?limit=50&q=%231234")
	if os.Getenv("AGENTBOX_WORK_SERVE") != "" {
		t.Logf("serving on %s", d.paths.Socket())
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		<-stop
	}
}

// The Work page a page at a time is the whole list, in its order, with no
// agent twice and none skipped: live agents among the past ones, of every
// project and of one, under each filter and a search, and where two agents
// finished in the same second.
func TestWorkPagesAreTheWholeList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	names := seedWork(t, d, 2, 130)
	p, err := d.srv.store.Project(ctx, names[0])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// Live: one at work, one finished just now, and one that finished weeks
	// ago, which sorts among the past agents.
	working := addAgent(t, d, p.Root, p.Name, "live-01", "Still at it")
	d.srv.record(ctx, createdEvent(working, "Do the thing", now))
	ready := addAgent(t, d, p.Root, p.Name, "live-02", "Just finished")
	d.srv.record(ctx, finishedEvent(ready, api.AgentChanges{}, nil, "Done.", now.Add(-time.Minute)))
	old := addAgent(t, d, p.Root, p.Name, "live-03", "Finished weeks ago")
	d.srv.record(ctx, finishedEvent(old, api.AgentChanges{}, nil, "Done then.", now.Add(-40*24*time.Hour)))
	// Two past agents of two projects archived in one second, and a third in
	// the first one's project: project and name order them.
	tie := now.Add(-20 * 24 * time.Hour).Truncate(time.Second)
	for _, a := range []state.Agent{{Project: names[1], Name: "tie-a"}, {Project: names[0], Name: "tie-c"}, {Project: names[0], Name: "tie-b"}} {
		a.Title, a.CreatedAt = "A tie", tie
		if err := d.srv.store.ArchiveAgent(ctx, a, "Tie", state.ArchivedPR{}, tie); err != nil {
			t.Fatal(err)
		}
	}

	refs := func(page api.WorkPage) []string {
		var out []string
		for _, w := range page.Items {
			out = append(out, w.Item.Ref)
		}
		return out
	}
	for _, path := range []string{
		"/v1/work?",
		"/v1/projects/" + names[1] + "/work?",
		"/v1/work?project=" + names[0] + "&",
		"/v1/work?filter=merged&",
		"/v1/work?filter=none&",
		"/v1/work?filter=review&",
		"/v1/work?q=tie&",
		"/v1/work?q=change+1&filter=closed&",
	} {
		whole := work(t, d, path+"limit=200")
		if whole.Next != "" || whole.Total != len(whole.Items) || whole.Total != whole.Counts[strings.TrimSuffix(strings.TrimPrefix(path[strings.Index(path, "?")+1:], "filter="), "&")] && strings.Contains(path, "?filter=") {
			t.Fatalf("%s whole: %d items, total %d, next %q, counts %v", path, len(whole.Items), whole.Total, whole.Next, whole.Counts)
		}
		for i := 1; i < len(whole.Items); i++ {
			a, b := whole.Items[i-1], whole.Items[i]
			if a.At.Before(b.At) || (a.At.Equal(b.At) && a.Item.Ref >= b.Item.Ref) {
				t.Fatalf("%s: %s (%v) is listed before %s (%v)", path, a.Item.Ref, a.At, b.Item.Ref, b.At)
			}
		}
		var got []string
		cursor, pages := "", 0
		for {
			page := work(t, d, path+"limit=7&cursor="+cursor)
			if page.Total != whole.Total || fmt.Sprint(page.Counts) != fmt.Sprint(whole.Counts) {
				t.Fatalf("%s page %d: total %d and counts %v, the whole list's are %d and %v", path, pages, page.Total, page.Counts, whole.Total, whole.Counts)
			}
			got = append(got, refs(page)...)
			pages++
			if cursor = page.Next; cursor == "" {
				break
			}
			if len(page.Items) != 7 {
				t.Fatalf("%s page %d has %d items and a next page", path, pages, len(page.Items))
			}
		}
		if want := refs(whole); !slices.Equal(got, want) {
			t.Errorf("%s in pages of 7 gave\n%v\nand the whole list is\n%v", path, got, want)
		}
	}

	all := work(t, d, "/v1/work?limit=200")
	if all.Total != 136 || all.Counts[api.WorkFilterReview] == 0 || all.Counts[api.WorkFilterMerged] != 78 || all.Counts[api.WorkFilterClosed] != 13 {
		t.Errorf("total %d, counts %v", all.Total, all.Counts)
	}
	if got := refs(all)[:2]; !slices.Equal(got, []string{working.Ref(), ready.Ref()}) {
		t.Errorf("the list starts with %v, want the live agents", got)
	}
	at := slices.Index(refs(all), old.Ref())
	if at < 10 || all.Items[at-1].Past == false || all.Items[at+1].Past == false {
		t.Errorf("the agent that finished weeks ago is at %d, want it among the past ones", at)
	}
	if got := refs(work(t, d, "/v1/work?q=tie")); !slices.Equal(got, []string{names[0] + "/tie-b", names[0] + "/tie-c", names[1] + "/tie-a"}) {
		t.Errorf("the tie is ordered %v", got)
	}

	// A past agent's row has its verdict, report, media and changes, read
	// for the page at once; a brief one only what a row shows.
	full := all.Items[slices.Index(refs(all), names[1]+"/agent-0004")]
	if full.Status != api.WorkReviewed || full.Item.Verdict != api.ReviewReviewed || full.Report == nil || full.Report.Summary == "" || full.Task == "" ||
		full.Screenshots != 3 || full.Recordings != 0 || full.Item.Changes == nil || full.Item.Changes.Files != 1 || full.Item.PR == nil || full.Item.PR.State != state.PRMerged {
		t.Errorf("agent-0004's row = %+v (report %+v, changes %+v)", full, full.Report, full.Item.Changes)
	}
	if w := all.Items[slices.Index(refs(all), names[1]+"/agent-0010")]; w.Report != nil || w.Recordings != 1 || w.Screenshots != 3 {
		t.Errorf("agent-0010, which filed no report, has %+v, %d screenshots, %d recordings", w.Report, w.Screenshots, w.Recordings)
	}
	brief := work(t, d, "/v1/work?limit=200&brief=1")
	if !slices.Equal(refs(brief), refs(all)) {
		t.Error("brief lists other work")
	}
	b := brief.Items[slices.Index(refs(brief), names[1]+"/agent-0004")]
	if b.Task != "" || b.Report == nil || b.Report.Status != full.Report.Status || b.Report.Summary != "" || len(b.Report.Discoveries) != 0 || b.Item.Summary != "" ||
		b.Screenshots != 3 || b.Item.Changes == nil || b.Status != api.WorkReviewed {
		t.Errorf("agent-0004's brief row = %+v (report %+v)", b, b.Report)
	}

	for _, cursor := range []string{"nope", "12", "12:noslash", "x:a/b"} {
		rec := httptest.NewRecorder()
		d.srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/work?cursor="+cursor, nil))
		if rec.Code == http.StatusOK {
			t.Errorf("the cursor %q was taken", cursor)
		}
	}
}
