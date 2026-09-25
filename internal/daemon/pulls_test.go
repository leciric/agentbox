package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/testutil"
)

// githubRepoStub gives a project's checkout a GitHub origin and a stored
// token, so githubFor resolves a client instead of quietly returning none.
func githubRepoStub(t *testing.T, d testDaemon, repo string) {
	t.Helper()
	testutil.Git(t, repo, "remote", "add", "origin", "git@github.com:acme/hello-stack.git")
	githubTokenStub(t, d)
}

// githubTokenStub stores a token without touching the checkout's remotes, for
// the cases where the remote itself is what's under test.
func githubTokenStub(t *testing.T, d testDaemon) {
	t.Helper()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveGitHubToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
}

// pullsOf reads a project's pull requests, waiting out the first read of the
// repository. The daemon answers from its cache and re-reads GitHub behind
// the answer (D54), so the very first request for a repository is the empty
// one that starts the read.
func pullsOf(t *testing.T, d testDaemon, project string) api.ProjectPullRequests {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := d.client.ProjectPullRequests(ctx, project)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Refreshing {
			return out // nothing in flight: this is the settled answer
		}
		if time.Now().After(deadline) {
			t.Fatalf("GitHub was never read for %s: %+v", project, out)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The Pull requests tab lists the repository's pull requests, not only the
// ones with an agent behind them, but marks the one that does — that link is
// the thing AgentBox uniquely knows.
func TestProjectPullRequestsListsAndLinksTheAgentBehindOne(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	// The link comes from the stored branch, named after the work, never from
	// the agent's name.
	if a.Branch != "agentbox/reminders-page" {
		t.Fatalf("the agent's branch is %q, want agentbox/reminders-page", a.Branch)
	}

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/hello-stack/pulls" && r.Method == http.MethodGet:
			fmt.Fprintf(w, `[
				{"number":9,"title":"Reminders page","state":"open","html_url":"https://github.com/acme/hello-stack/pull/9","draft":false,"updated_at":"2026-09-15T10:00:00Z","base":{"ref":"main"},"head":{"ref":%q,"sha":"def"}},
				{"number":3,"title":"Old work","state":"closed","merged_at":"2026-09-01T10:00:00Z","html_url":"https://github.com/acme/hello-stack/pull/3","base":{"ref":"main"},"head":{"ref":"agentbox/agent-99","sha":"aaa"}}
			]`, a.Branch)
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9":
			w.Write([]byte(`{"additions":5,"deletions":1,"comments":2}`))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			w.Write([]byte(`{"total_count":0}`))
		case r.URL.Path == "/repos/acme/hello-stack":
			w.Write([]byte(`{"default_branch":"main","allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":true,"permissions":{"push":true}}`))
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer stub.Close()
	t.Setenv("AGENTBOX_GITHUB_API", stub.URL)

	out := pullsOf(t, d, "hello-stack")
	if out.GitHub != "acme/hello-stack" || len(out.PullRequests) != 2 {
		t.Fatalf("ProjectPullRequests() = %+v", out)
	}
	if !out.CanMerge || !out.CanMergeKnown {
		t.Errorf("CanMerge/CanMergeKnown = %v/%v, want true/true", out.CanMerge, out.CanMergeKnown)
	}
	if want := []string{"merge", "squash", "rebase"}; !slices.Equal(out.MergeMethods, want) {
		t.Errorf("MergeMethods = %v, want %v", out.MergeMethods, want)
	}
	open := out.PullRequests[0]
	if open.Number != 9 || open.Agent != "agent-01" || open.Additions != 5 || open.Comments != 2 {
		t.Errorf("open pull request = %+v", open)
	}
	old := out.PullRequests[1]
	if old.Agent != "" {
		t.Errorf("a pull request from an unknown branch got linked to an agent: %+v", old)
	}
}

// A project whose repository isn't on GitHub has no pull requests to read,
// and that is not an error: the tab still works, same as the fleet.
func TestProjectPullRequestsWithoutAGitHubRemoteIsQuiet(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	out, err := d.client.ProjectPullRequests(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if out.GitHub != "" || out.GitHubError != nil || len(out.PullRequests) != 0 {
		t.Errorf("ProjectPullRequests() = %+v, want quiet without a GitHub remote", out)
	}
}

// A repository with no pull requests wire-encodes as an empty JSON array, not
// null: the app's TypeScript type for pullRequests isn't optional, and
// null.length throws where [].length doesn't.
func TestProjectPullRequestsEmptyListIsNotJSONNull(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/hello-stack/pulls":
			w.Write([]byte(`[]`))
		case r.URL.Path == "/repos/acme/hello-stack":
			w.Write([]byte(`{"default_branch":"main","permissions":{"push":true}}`))
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer stub.Close()
	t.Setenv("AGENTBOX_GITHUB_API", stub.URL)

	pullsOf(t, d, "hello-stack") // wait out the first read, so the list is really empty
	res, err := d.client.HTTPClient().Get("http://agentbox/v1/projects/hello-stack/pulls")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"pullRequests":[]`) {
		t.Errorf("response body = %s, want an empty array, not null", body)
	}
}

// Merging re-reads the pull request first, so a stale UI can't merge a draft
// or one that has since been closed, then sends the chosen method.
func TestMergePullRequestConfirmsThenMerges(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)

	var merged bool
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9" && r.Method == http.MethodGet:
			w.Write([]byte(`{"number":9,"title":"Reminders page","state":"open","draft":false,"html_url":"https://github.com/acme/hello-stack/pull/9","base":{"ref":"main"},"head":{"ref":"agentbox/agent-01","sha":"def"}}`))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			w.Write([]byte(`{"total_count":0}`))
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9/merge" && r.Method == http.MethodPut:
			merged = true
			var body struct {
				MergeMethod string `json:"merge_method"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.MergeMethod != "squash" {
				t.Errorf("merge_method = %q, want squash", body.MergeMethod)
			}
			w.Write([]byte(`{"sha":"def","merged":true,"message":"Pull Request successfully merged"}`))
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer stub.Close()
	t.Setenv("AGENTBOX_GITHUB_API", stub.URL)

	pr, err := d.client.MergePullRequest(ctx, "hello-stack", 9, "squash")
	if err != nil {
		t.Fatal(err)
	}
	if !merged {
		t.Error("never called GitHub's merge endpoint")
	}
	if pr.State != "merged" {
		t.Errorf("State = %q, want merged", pr.State)
	}
}

// A draft pull request is never merged, even if the request asks: the UI
// shouldn't offer it, but the daemon checks again before touching GitHub.
func TestMergePullRequestRejectsADraft(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9" && r.Method == http.MethodGet:
			w.Write([]byte(`{"number":9,"title":"WIP","state":"open","draft":true,"html_url":"https://github.com/acme/hello-stack/pull/9","base":{"ref":"main"},"head":{"ref":"agentbox/agent-01","sha":"def"}}`))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			w.Write([]byte(`{"total_count":0}`))
		case strings.HasSuffix(r.URL.Path, "/merge"):
			t.Error("merged a draft pull request")
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer stub.Close()
	t.Setenv("AGENTBOX_GITHUB_API", stub.URL)

	if _, err := d.client.MergePullRequest(ctx, "hello-stack", 9, "merge"); err == nil || !strings.Contains(err.Error(), "draft") {
		t.Errorf("MergePullRequest() = %v, want a draft error", err)
	}
}

// A pull request that has since closed, or already merged, is refused too.
func TestMergePullRequestRejectsOneThatIsNotOpen(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9" && r.Method == http.MethodGet:
			w.Write([]byte(`{"number":9,"title":"Done","state":"closed","merged_at":"2026-09-01T10:00:00Z","draft":false,"html_url":"https://github.com/acme/hello-stack/pull/9","base":{"ref":"main"},"head":{"ref":"agentbox/agent-01","sha":"def"}}`))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			w.Write([]byte(`{"total_count":0}`))
		case strings.HasSuffix(r.URL.Path, "/merge"):
			t.Error("merged a pull request that was already merged")
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer stub.Close()
	t.Setenv("AGENTBOX_GITHUB_API", stub.URL)

	if _, err := d.client.MergePullRequest(ctx, "hello-stack", 9, "merge"); err == nil || !strings.Contains(err.Error(), "merged") {
		t.Errorf("MergePullRequest() = %v, want it to say it's already merged", err)
	}
}

// When GitHub refuses a merge, the real reason reaches the user: a missing
// review, a failing check, a protected branch — not just a status code.
func TestMergePullRequestSurfacesGitHubsRefusal(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9" && r.Method == http.MethodGet:
			w.Write([]byte(`{"number":9,"title":"Reminders page","state":"open","draft":false,"html_url":"https://github.com/acme/hello-stack/pull/9","base":{"ref":"main"},"head":{"ref":"agentbox/agent-01","sha":"def"}}`))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			w.Write([]byte(`{"total_count":0}`))
		case strings.HasSuffix(r.URL.Path, "/merge"):
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]string{"message": "At least 1 approving review is required by reviewers with write access."})
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer stub.Close()
	t.Setenv("AGENTBOX_GITHUB_API", stub.URL)

	_, err := d.client.MergePullRequest(ctx, "hello-stack", 9, "merge")
	if err == nil || !strings.Contains(err.Error(), "approving review") {
		t.Errorf("MergePullRequest() = %v, want GitHub's own reason", err)
	}
}

// A merge method AgentBox doesn't recognize is refused before GitHub is ever
// asked.
func TestMergePullRequestRejectsAnInvalidMethod(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.MergePullRequest(ctx, "hello-stack", 9, "rewrite-history"); err == nil || !strings.Contains(err.Error(), "merge, squash or rebase") {
		t.Errorf("MergePullRequest() = %v, want a validation error", err)
	}
}

// Without a shared GitHub token, merging says so plainly and names the fix,
// rather than failing as though the pull request just wasn't found.
func TestMergePullRequestWithoutTokenSaysSo(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	// The repository is on GitHub; only the token is missing.
	testutil.Git(t, repo, "remote", "add", "origin", "git@github.com:acme/hello-stack.git")
	if _, err := d.client.MergePullRequest(ctx, "hello-stack", 9, "merge"); err == nil || !strings.Contains(err.Error(), "agentbox auth github") {
		t.Errorf("MergePullRequest() = %v, want a message naming the fix", err)
	}
}

// Pull requests are read with the project's account, or the machine's default
// one, and the answer says which — the tab shows it, because a repository one
// account can't see is another account's everyday repository. When the read
// fails, what kind of failure it was decides the sentence the app can say, so
// the daemon names it rather than passing GitHub's words through.
func TestPullRequestsNameTheirAccountAndFailure(t *testing.T) {
	// One short root for every case: a subtest's own t.TempDir() carries its
	// name, and the daemon's socket has 107 bytes to fit in.
	roots := t.TempDir()
	setup := func(t *testing.T, name string) testDaemon {
		t.Helper()
		root := filepath.Join(roots, name)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		d := startTestDaemon(t, root, fakeIncus)
		repo := testutil.FixtureRepo(t, "hello-stack")
		if _, err := d.client.AddProject(context.Background(), api.AddProjectRequest{Path: repo}); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, repo, "remote", "add", "origin", "git@github.com:acme/hello-stack.git")
		return d
	}
	// Two accounts, so the one being used is a choice rather than the only
	// option, and the project picks the second.
	accounts := func(t *testing.T, d testDaemon) credentials.Store {
		t.Helper()
		creds := credentials.Store{Dir: d.paths.Credentials()}
		for account, login := range map[string]string{"personal": "leciric", "work": "leciric-work"} {
			if err := creds.SaveGitHubToken(account, "gho_"+account); err != nil {
				t.Fatal(err)
			}
			if err := creds.SaveGitHubLogin(account, login); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := d.client.UpdateProject(context.Background(), "hello-stack", api.UpdateProjectRequest{GitHubAccount: ptr("work")}); err != nil {
			t.Fatal(err)
		}
		return creds
	}
	// answering is a GitHub that refuses everything with one status.
	answering := func(t *testing.T, status int, message string) {
		t.Helper()
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"message":%q}`, message)
		}))
		t.Cleanup(stub.Close)
		t.Setenv("AGENTBOX_GITHUB_API", stub.URL)
	}

	t.Run("no account is stored at all", func(t *testing.T) {
		d := setup(t, "a")
		out := pullsOf(t, d, "hello-stack")
		if out.GitHubAccount != "" || out.GitHubError == nil || out.GitHubError.Kind != api.GitHubNoAccount {
			t.Fatalf("ProjectPullRequests() = %+v, %+v; want a noAccount error", out, out.GitHubError)
		}
		if out.GitHubError.Repo != "acme/hello-stack" {
			t.Errorf("error = %+v, want it to name the repository it couldn't read", out.GitHubError)
		}
	})

	t.Run("the account can't see the repository", func(t *testing.T) {
		d := setup(t, "b")
		accounts(t, d)
		answering(t, http.StatusNotFound, "Not Found")
		out := pullsOf(t, d, "hello-stack")
		if out.GitHubAccount != "work" {
			t.Errorf("GitHubAccount = %q, want the project's account", out.GitHubAccount)
		}
		got := out.GitHubError
		if got == nil || got.Kind != api.GitHubNoAccess {
			t.Fatalf("GitHubError = %+v, want noAccess", got)
		}
		if got.Account != "work" || got.Login != "leciric-work" || got.Repo != "acme/hello-stack" {
			t.Errorf("GitHubError = %+v, want it to name the account, its login and the repository", got)
		}
	})

	t.Run("GitHub refused the token", func(t *testing.T) {
		d := setup(t, "c")
		accounts(t, d)
		answering(t, http.StatusUnauthorized, "Bad credentials")
		out := pullsOf(t, d, "hello-stack")
		if out.GitHubError == nil || out.GitHubError.Kind != api.GitHubBadToken || out.GitHubError.Account != "work" {
			t.Fatalf("GitHubError = %+v, want badToken for the account work", out.GitHubError)
		}
	})

	t.Run("anything else is GitHub's own words", func(t *testing.T) {
		d := setup(t, "d")
		accounts(t, d)
		answering(t, http.StatusInternalServerError, "Server Error")
		out := pullsOf(t, d, "hello-stack")
		if out.GitHubError == nil || out.GitHubError.Kind != api.GitHubOtherErr {
			t.Fatalf("GitHubError = %+v, want other", out.GitHubError)
		}
		if !strings.Contains(out.GitHubError.Message, "Server Error") {
			t.Errorf("GitHubError = %+v, want GitHub's own message", out.GitHubError)
		}
	})

	t.Run("the account the project picked was removed", func(t *testing.T) {
		d := setup(t, "e")
		creds := accounts(t, d)
		if err := creds.RemoveGitHubAccount("work"); err != nil {
			t.Fatal(err)
		}
		out := pullsOf(t, d, "hello-stack")
		got := out.GitHubError
		if got == nil || got.Kind != api.GitHubNoAccount || got.Account != "work" {
			t.Fatalf("GitHubError = %+v, want noAccount naming the account that is gone", got)
		}
		if !strings.Contains(got.Message, "isn't stored any more") {
			t.Errorf("GitHubError = %+v, want it to say the account is gone rather than that none exists", got)
		}
	})
}

// The two ways a project has no GitHub repository are different problems with
// different fixes, so the response tells them apart instead of leaving the app
// to guess. Neither is a GitHubError: nothing was read, and no account is at
// fault. A silent, wrong explanation here cost a user a long debugging
// session.
func TestPullRequestsWithNoOrigin(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubTokenStub(t, d)

	out := pullsOf(t, d, "hello-stack")
	if !out.NoOrigin || out.NonGitHubRemote != "" || out.GitHubError != nil {
		t.Errorf("ProjectPullRequests() = %+v, want NoOrigin alone", out)
	}
}

// An origin that isn't GitHub is quoted back, so the app can say which remote
// it is rather than claiming there is none — with any password redacted, since
// this string reaches a response and a log.
func TestPullRequestsWithAnotherForge(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "remote", "add", "origin", "https://someone:hunter2@gitlab.com/acme/hello-stack.git")
	githubTokenStub(t, d)

	out := pullsOf(t, d, "hello-stack")
	if out.NoOrigin || out.GitHubError != nil {
		t.Errorf("ProjectPullRequests() = %+v, want the remote, not NoOrigin", out)
	}
	if !strings.Contains(out.NonGitHubRemote, "gitlab.com/acme/hello-stack.git") {
		t.Errorf("NonGitHubRemote = %q, want it to name the origin", out.NonGitHubRemote)
	}
	if strings.Contains(out.NonGitHubRemote, "hunter2") {
		t.Errorf("NonGitHubRemote = %q, want the password redacted", out.NonGitHubRemote)
	}
}

// A remote through an SSH host alias — `Host github.com-work` in
// ~/.ssh/config, how people keep two GitHub accounts on two keys — is the
// GitHub repository it resolves to, so the tab fills in as usual.
func TestPullRequestsThroughAnSSHHostAlias(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "remote", "add", "origin", "git@github.com-daemon:acme/hello-stack.git")
	githubTokenStub(t, d)
	sshConfigStub(t, "github.com-daemon")

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/acme/hello-stack/pulls" {
			w.Write([]byte(`[]`))
			return
		}
		w.Write([]byte(`{"default_branch":"main","permissions":{"push":true}}`))
	}))
	defer stub.Close()
	t.Setenv("AGENTBOX_GITHUB_API", stub.URL)

	out := pullsOf(t, d, "hello-stack")
	if out.GitHub != "acme/hello-stack" || out.NoOrigin || out.NonGitHubRemote != "" {
		t.Errorf("ProjectPullRequests() = %+v, want the alias resolved to acme/hello-stack", out)
	}
}

// sshConfigStub puts a fake `ssh` on PATH that reports each alias as
// github.com, standing in for a ~/.ssh/config the daemon's ssh would read.
func sshConfigStub(t *testing.T, aliases ...string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$2\" in\n"
	for _, alias := range aliases {
		script += alias + ") echo 'hostname github.com';;\n"
	}
	script += "*) echo \"hostname $2\";;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
