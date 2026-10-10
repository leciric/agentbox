package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/testutil"
)

// labelGitHub is a stub GitHub with one open pull request, #9, whose labels
// can be read and replaced, and a repository with three labels.
type labelGitHub struct {
	mu     sync.Mutex
	labels []string // on #9
	puts   int
	fail   bool // refuse the next PUT
}

var repoLabels = map[string]string{"ci:full": "0e8a16", "nightly": "5319e7", "deploy-dev": "fbca04"}

func (g *labelGitHub) onPR() string {
	var items []string
	for _, name := range g.labels {
		b, _ := json.Marshal(map[string]string{"name": name, "color": repoLabels[name]})
		items = append(items, string(b))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func (g *labelGitHub) serve(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch {
		case r.URL.Path == "/repos/acme/hello-stack/pulls":
			_, _ = w.Write([]byte(`[{"number":9,"title":"Reminders","state":"open","html_url":"https://github.com/acme/hello-stack/pull/9","updated_at":"2026-10-01T10:00:00Z","user":{"login":"octocat"},"base":{"ref":"main"},"head":{"ref":"feat/reminders","sha":"abc"},"labels":` + g.onPR() + `}]`))
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9":
			_, _ = w.Write([]byte(`{"additions":1,"deletions":0,"comments":0}`))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			_, _ = w.Write([]byte(`{"total_count":0}`))
		case r.URL.Path == "/repos/acme/hello-stack/labels":
			_, _ = w.Write([]byte(`[{"name":"ci:full","color":"0e8a16","description":"Run every CI job"},{"name":"nightly","color":"5319e7"},{"name":"deploy-dev","color":"fbca04"}]`))
		case r.URL.Path == "/repos/acme/hello-stack/issues/9/labels" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(g.onPR()))
		case r.URL.Path == "/repos/acme/hello-stack/issues/9/labels" && r.Method == http.MethodPut:
			g.puts++
			if g.fail {
				g.fail = false
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"Must have admin rights to Repository."}`))
				return
			}
			var body struct{ Labels []string }
			data, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(data, &body); err != nil {
				t.Errorf("PUT labels body %s: %v", data, err)
			}
			g.labels = body.Labels
			_, _ = w.Write([]byte(g.onPR()))
		case r.URL.Path == "/repos/acme/hello-stack":
			_, _ = w.Write([]byte(`{"default_branch":"main","permissions":{"push":true}}`))
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}
}

// The tab shows each pull request's labels with GitHub's colours, lists the
// repository's labels for the picker, and edits a pull request's labels with
// the project's account: the list shows the edit at once, from the cache,
// with no wait for a re-read of GitHub.
func TestPullRequestLabelsAreShownListedAndEdited(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	pullsProject(t, d)
	gh := &labelGitHub{labels: []string{"ci:full", "deploy-dev"}}
	stub := httptest.NewServer(gh.serve(t))
	defer stub.Close()
	d.setGitHub(t, stub.URL)

	out := pullsOf(t, d, "hello-stack")
	if len(out.PullRequests) != 1 {
		t.Fatalf("ProjectPullRequests() = %+v", out)
	}
	want := []api.Label{{Name: "ci:full", Color: "0e8a16"}, {Name: "deploy-dev", Color: "fbca04"}}
	if got := out.PullRequests[0].Labels; !slices.Equal(got, want) {
		t.Errorf("labels = %+v, want %+v", got, want)
	}

	all, err := d.client.ProjectLabels(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Labels) != 3 || all.Labels[0].Description != "Run every CI job" {
		t.Errorf("ProjectLabels() = %+v", all)
	}

	// nightly on, ci:full off; deploy-dev, which nobody touched, stays.
	got, err := d.client.EditPullRequestLabels(ctx, "hello-stack", 9, api.EditLabelsRequest{Add: []string{"nightly"}, Remove: []string{"ci:full"}})
	if err != nil {
		t.Fatal(err)
	}
	want = []api.Label{{Name: "deploy-dev", Color: "fbca04"}, {Name: "nightly", Color: "5319e7"}}
	if !slices.Equal(got, want) {
		t.Errorf("EditPullRequestLabels() = %+v, want %+v", got, want)
	}
	now, err := d.client.ProjectPullRequests(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if got := now.PullRequests[0].Labels; !slices.Equal(got, want) {
		t.Errorf("labels right after the edit = %+v, want %+v", got, want)
	}

	// Taking off one that is already off, and putting on one already on, is
	// no error: two edits that crossed each other both land.
	if _, err := d.client.EditPullRequestLabels(ctx, "hello-stack", 9, api.EditLabelsRequest{Add: []string{"nightly"}, Remove: []string{"ci:full"}}); err != nil {
		t.Errorf("a repeated edit failed: %v", err)
	}

	// GitHub's refusal comes back as it said it, and the cache keeps what
	// GitHub has.
	gh.mu.Lock()
	gh.fail = true
	gh.mu.Unlock()
	_, err = d.client.EditPullRequestLabels(ctx, "hello-stack", 9, api.EditLabelsRequest{Remove: []string{"nightly"}})
	if err == nil || !strings.Contains(err.Error(), "admin rights") || strings.Contains(err.Error(), "can't see") {
		t.Errorf("a refused edit = %v, want GitHub's reason", err)
	}
	now, _ = d.client.ProjectPullRequests(ctx, "hello-stack")
	if got := now.PullRequests[0].Labels; !slices.Equal(got, want) {
		t.Errorf("labels after a refused edit = %+v, want %+v", got, want)
	}
}

// The Mine filter matches authors against who the project's account is on
// GitHub, which the list carries.
func TestProjectPullRequestsCarryTheAccountsLogin(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	pullsProject(t, d)
	stub := httptest.NewServer((&labelGitHub{}).serve(t))
	defer stub.Close()
	d.setGitHub(t, stub.URL)
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveGitHubLogin(credentials.DefaultAccount, "octocat"); err != nil {
		t.Fatal(err)
	}
	out := pullsOf(t, d, "hello-stack")
	if out.GitHubLogin != "octocat" || out.PullRequests[0].Author != "octocat" {
		t.Errorf("login %q, author %q, want octocat for both", out.GitHubLogin, out.PullRequests[0].Author)
	}
}

// Editing labels without a GitHub account says how to add one.
func TestEditLabelsWithoutTokenSaysSo(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "remote", "add", "origin", "git@github.com:acme/hello-stack.git")
	_, err := d.client.EditPullRequestLabels(ctx, "hello-stack", 9, api.EditLabelsRequest{Add: []string{"nightly"}})
	if err == nil || !strings.Contains(err.Error(), "agentbox auth github") {
		t.Errorf("EditPullRequestLabels() without a token = %v", err)
	}
}

// A label edit overtakes a refresh in flight, which read GitHub before it,
// without marking the rest of the list stale; invalidate marks it stale, so
// the next request re-reads GitHub whatever its age.
func TestPullsCacheLabelEditsAndInvalidate(t *testing.T) {
	t.Parallel()
	c := newPullsCache()
	gen, _ := c.claim("acme/x", nil)
	c.finish("acme/x", gen, pullsEntry{prs: []api.PullRequest{{Number: 1, State: "open"}}})

	gen, _ = c.claim("acme/x", nil) // a refresh nobody asked for still counts
	c.labelled("acme/x", 1, []api.Label{{Name: "nightly", Color: "5319e7"}})
	if _, stored, _ := c.finish("acme/x", gen, pullsEntry{prs: []api.PullRequest{{Number: 1, State: "open"}}}); stored {
		t.Error("a refresh that a label edit overtook was stored")
	}
	e, _ := c.state("acme/x")
	if e.stale || len(e.prs[0].Labels) != 1 {
		t.Errorf("after the edit: stale %v, labels %+v", e.stale, e.prs[0].Labels)
	}
	if _, ok := c.claim("acme/x", nil); ok {
		t.Error("a label edit made a fresh entry re-read")
	}

	c.readFor("acme/x", "hello")
	if got := c.reposOf("hello"); !slices.Equal(got, []string{"acme/x"}) {
		t.Errorf("reposOf() = %v", got)
	}
	c.invalidate("acme/x")
	if _, ok := c.claim("acme/x", nil); !ok {
		t.Error("an invalidated entry wasn't re-read")
	}
}

// Labels count in whether two answers are the same.
func TestSamePullRequestComparesLabels(t *testing.T) {
	t.Parallel()
	a := api.PullRequest{Number: 1, Labels: []api.Label{{Name: "nightly"}}}
	b := api.PullRequest{Number: 1}
	if samePullRequest(a, b) {
		t.Error("a label put on was not a change")
	}
	b.Labels = []api.Label{{Name: "nightly"}}
	if !samePullRequest(a, b) {
		t.Error("the same labels were a change")
	}
}
