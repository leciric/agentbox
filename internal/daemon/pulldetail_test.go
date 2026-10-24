package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agentbox/internal/api"
)

// The pull request view reads one pull request with its description, labels
// and every check run, and marks the agent behind it as the list does.
func TestPullRequestDetailReadsDescriptionLabelsAndChecks(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/hello-stack/pulls/9":
			_, _ = w.Write([]byte(`{"number":9,"title":"Reminders page","state":"open","body":"![shot](https://github.com/user-attachments/assets/x)",
				"labels":[{"name":"ui","color":"a2eeef"}],"base":{"ref":"main"},"head":{"ref":"feat/reminders","sha":"def"}}`))
		case "/repos/acme/hello-stack/commits/def/check-runs":
			_, _ = w.Write([]byte(`{"total_count":1,"check_runs":[{"name":"go","status":"completed","conclusion":"failure","html_url":"https://github.com/acme/hello-stack/runs/1"}]}`))
		default:
			t.Errorf("unexpected GitHub call: %s", r.URL.Path)
		}
	}))
	defer stub.Close()
	d.setGitHub(t, stub.URL)

	pr, err := d.client.PullRequestDetail(ctx, "hello-stack", 9)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Title != "Reminders page" || pr.HeadBranch != "feat/reminders" || !strings.Contains(pr.Body, "user-attachments") {
		t.Errorf("detail = %+v", pr)
	}
	if len(pr.Labels) != 1 || pr.Labels[0].Name != "ui" || pr.Labels[0].Color != "a2eeef" {
		t.Errorf("Labels = %+v", pr.Labels)
	}
	if len(pr.CheckRuns) != 1 || pr.CheckRuns[0].Conclusion != "failure" || pr.Checks != "failing" {
		t.Errorf("CheckRuns = %+v, Checks = %q", pr.CheckRuns, pr.Checks)
	}
}

// A pull request opened twice in a moment — hovered, then clicked — is one
// read of GitHub; merging it through the app forgets it, so the next open
// reads it again.
func TestPullRequestDetailIsKeptBrieflyAndDroppedByAMerge(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)
	var reads atomic.Int32
	var merged atomic.Bool
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9" && r.Method == http.MethodGet:
			reads.Add(1)
			state := `"state":"open"`
			if merged.Load() {
				state = `"state":"closed","merged_at":"2026-10-23T12:00:00Z"`
			}
			_, _ = w.Write([]byte(`{"number":9,"title":"Reminders page",` + state + `,"base":{"ref":"main"},"head":{"ref":"feat/reminders","sha":"def"}}`))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			_, _ = w.Write([]byte(`{"check_runs":[]}`))
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9/merge":
			merged.Store(true)
			_, _ = w.Write([]byte(`{"merged":true}`))
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer stub.Close()
	d.setGitHub(t, stub.URL)

	for range 2 {
		if pr, err := d.client.PullRequestDetail(ctx, "hello-stack", 9); err != nil || pr.State != "open" {
			t.Fatalf("detail = %+v, %v", pr, err)
		}
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("GitHub read %d times for two opens in a moment, want once", n)
	}
	if _, err := d.client.MergePullRequest(ctx, "hello-stack", 9, "merge"); err != nil {
		t.Fatal(err)
	}
	pr, err := d.client.PullRequestDetail(ctx, "hello-stack", 9)
	if err != nil || pr.State != "merged" {
		t.Errorf("detail after the merge = %+v, %v; want it read again, merged", pr, err)
	}
}

// The files are listed without their diffs, and each diff then comes from
// what was read with the list rather than another read of GitHub.
func TestPullRequestFilesThenEachDiff(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)
	var reads atomic.Int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/hello-stack/pulls/9/files" {
			t.Errorf("unexpected GitHub call: %s", r.URL.Path)
			return
		}
		reads.Add(1)
		_, _ = w.Write([]byte(`[{"filename":"main.go","status":"modified","additions":1,"deletions":1,"patch":"@@ -1 +1 @@\n-a\n+b"},
			{"filename":"logo.png","status":"added"}]`))
	}))
	defer stub.Close()
	d.setGitHub(t, stub.URL)

	files, err := d.client.PullRequestFiles(ctx, "hello-stack", 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != 2 || !files.Files[0].HasDiff || files.Files[1].HasDiff || files.Files[0].Additions != 1 {
		t.Fatalf("files = %+v", files)
	}
	diff, err := d.client.PullFileDiff(ctx, "hello-stack", 9, "main.go")
	if err != nil || diff.Patch != "@@ -1 +1 @@\n-a\n+b" {
		t.Errorf("diff = %+v, %v", diff, err)
	}
	if again, err := d.client.PullRequestFiles(ctx, "hello-stack", 9); err != nil || len(again.Files) != 2 {
		t.Errorf("files listed again = %+v, %v", again, err)
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("GitHub read %d times, want once", n)
	}
	if _, err := d.client.PullFileDiff(ctx, "hello-stack", 9, "missing.go"); !api.IsNotFound(err) {
		t.Errorf("a file it doesn't change = %v, want not found", err)
	}
}

// Pictures are read only from GitHub's own hosts: a description is anyone's
// text, and the daemon must not read whatever it names.
func TestPullRequestImageRefusesAnotherHost(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)
	for _, src := range []string{"https://evil.example/x.png", "http://127.0.0.1:1/x.png", "https://github.com/other/repo/assets/x.png"} {
		rec := httptest.NewRecorder()
		d.srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/projects/hello-stack/pulls/image?src="+src, nil))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not an image GitHub hosts") {
			t.Errorf("%s = %d %s, want refused", src, rec.Code, rec.Body)
		}
	}
}
