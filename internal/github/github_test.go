package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/github"
	"agentbox/internal/testutil"
)

// sshStub puts a fake `ssh` on PATH that answers `ssh -G <host>` from hosts,
// the way OpenSSH does, and records every host it is asked about so a test
// can tell what did and didn't shell out. A host it doesn't know resolves to
// itself, as an unaliased host does.
func sshStub(t *testing.T, hosts map[string]string) func() []string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "asked")
	script := "#!/bin/sh\necho \"$2\" >> " + log + "\ncase \"$2\" in\n"
	for alias, host := range hosts {
		script += fmt.Sprintf("%s) echo 'hostname %s';;\n", alias, host)
	}
	script += "*) echo \"hostname $2\";;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		out, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Fields(string(out))
	}
}

// ParseRemote reads the URL forms git writes, and resolves an SSH host alias
// — `Host github.com-work` in ~/.ssh/config, how people keep two GitHub
// accounts on two keys — to the host it really points at.
func TestParseRemote(t *testing.T) {
	asked := sshStub(t, map[string]string{
		"github.com-work": "github.com",
		"gh-personal":     "github.com",
		"work.github":     "codeberg.org",
	})

	for _, tc := range []struct {
		name   string
		remote string
		want   string // the repository, or "" when the remote isn't one
	}{
		{"canonical https", "https://github.com/acme/pawly.git", "acme/pawly"},
		{"https without .git", "https://github.com/acme/pawly", "acme/pawly"},
		{"https with an embedded token", "https://x-access-token:ghs_secret@github.com/acme/pawly.git", "acme/pawly"},
		{"scp-like", "git@github.com:acme/pawly.git", "acme/pawly"},
		{"scp-like with the newline git leaves on", "git@github.com:acme/pawly\n", "acme/pawly"},
		{"ssh URL", "ssh://git@github.com/acme/pawly.git", "acme/pawly"},
		{"ssh URL with a port", "ssh://git@github.com:22/acme/pawly.git", "acme/pawly"},
		{"a trailing slash and dots in the name", "https://github.com/acme/deep.name.repo/", "acme/deep.name.repo"},
		{"an alias resolving to github.com", "git@github.com-work:acme-work/widget.git", "acme-work/widget"},
		{"an alias that doesn't look like github.com at all", "gh-personal:acme/pawly.git", "acme/pawly"},
		{"an alias resolving elsewhere", "git@work.github:acme/pawly.git", ""},
		{"another forge", "git@gitlab.com:acme/pawly.git", ""},
		{"a local path", "/srv/git/pawly.git", ""},
		{"no remote at all", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, err := github.ParseRemote(tc.remote)
			if tc.want == "" {
				if !errors.Is(err, github.ErrNoRepo) {
					t.Fatalf("ParseRemote(%q) = %q, %v; want ErrNoRepo", tc.remote, repo, err)
				}
				return
			}
			if err != nil || repo.String() != tc.want {
				t.Fatalf("ParseRemote(%q) = %q, %v; want %q", tc.remote, repo, err, tc.want)
			}
		})
	}

	// The fast path matters: a remote already on github.com is the common
	// case, and it must not pay for a subprocess.
	if slices.Contains(asked(), "github.com") {
		t.Errorf("ssh -G was asked about github.com itself: %v", asked())
	}
}

// An empty origin says so as ErrNoOrigin, which is a different thing from an
// origin pointing somewhere that isn't GitHub.
func TestParseRemoteTellsNoOriginFromNotGitHub(t *testing.T) {
	sshStub(t, nil)
	if _, err := github.ParseRemote("  \n"); !errors.Is(err, github.ErrNoOrigin) {
		t.Errorf("ParseRemote(\"\") = %v, want ErrNoOrigin", err)
	}
	_, err := github.ParseRemote("git@gitlab.com:acme/pawly.git")
	var notGitHub *github.NotGitHubError
	if !errors.As(err, &notGitHub) || errors.Is(err, github.ErrNoOrigin) {
		t.Errorf("ParseRemote(gitlab) = %v, want a NotGitHubError and not ErrNoOrigin", err)
	}
	if notGitHub != nil && notGitHub.Remote != "git@gitlab.com:acme/pawly.git" {
		t.Errorf("Remote = %q, want the origin itself", notGitHub.Remote)
	}
}

// A token in an origin URL never reaches an error, a log or a response.
// There are real origins of the form https://x-access-token:<token>@…
func TestNotGitHubErrorRedactsCredentials(t *testing.T) {
	sshStub(t, nil)
	_, err := github.ParseRemote("https://x-access-token:ghs_realsecret@gitlab.com/acme/pawly.git")
	var notGitHub *github.NotGitHubError
	if !errors.As(err, &notGitHub) {
		t.Fatalf("ParseRemote() = %v, want a NotGitHubError", err)
	}
	if strings.Contains(err.Error(), "ghs_realsecret") || strings.Contains(notGitHub.Remote, "ghs_realsecret") {
		t.Errorf("error = %q, still carries the token", err)
	}
	if !strings.Contains(notGitHub.Remote, "gitlab.com/acme/pawly.git") {
		t.Errorf("Remote = %q, want the URL itself, minus the password", notGitHub.Remote)
	}
}

// The lookup is cached per host: RepoOf runs on every fleet and pull requests
// poll, and `ssh -G` is cheap but not free.
func TestAnAliasIsResolvedOnce(t *testing.T) {
	asked := sshStub(t, map[string]string{"github.com-cached": "github.com"})
	for range 3 {
		if _, err := github.ParseRemote("git@github.com-cached:acme/pawly.git"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(asked()); n != 1 {
		t.Errorf("ssh -G ran %d times, want 1: the per-host cache isn't holding", n)
	}
}

// Without ssh on PATH an alias is simply not GitHub — what AgentBox did
// before it resolved aliases at all — rather than an error.
func TestAnAliasWithoutSSHFallsBack(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := github.ParseRemote("git@github.com-nossh:acme/pawly.git"); !errors.Is(err, github.ErrNoRepo) {
		t.Errorf("ParseRemote() = %v, want ErrNoRepo", err)
	}
	if _, err := github.ParseRemote("git@github.com:acme/pawly.git"); err != nil {
		t.Errorf("ParseRemote() = %v, want github.com itself to need no ssh at all", err)
	}
}

// RepoOf reads the checkout's own origin, and tells a checkout with no origin
// from one whose origin isn't GitHub.
func TestRepoOf(t *testing.T) {
	sshStub(t, map[string]string{"github.com-repoof": "github.com"})
	testutil.GitEnv(t)
	root := t.TempDir()
	testutil.Git(t, root, "init", "-q", "-b", "main")

	if _, err := github.RepoOf(root); !errors.Is(err, github.ErrNoOrigin) {
		t.Errorf("RepoOf() with no origin = %v, want ErrNoOrigin", err)
	}
	testutil.Git(t, root, "remote", "add", "origin", "git@github.com-repoof:acme/pawly.git")
	repo, err := github.RepoOf(root)
	if err != nil || repo.String() != "acme/pawly" {
		t.Errorf("RepoOf() = %q, %v; want acme/pawly", repo, err)
	}
}

// stub answers the three calls a pull request lookup makes.
func stub(t *testing.T, pulls string, checks string) github.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			if r.URL.Path != "/repos/acme/pawly/commits/abc/pulls" {
				t.Errorf("asked %s, want the pull requests of commit abc", r.URL.Path)
			}
			if pulls == "" {
				// What GitHub says about a commit that was never pushed.
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"No commit found for SHA: abc"}`))
				return
			}
			_, _ = w.Write([]byte(pulls))
		case strings.Contains(r.URL.Path, "/check-runs"):
			_, _ = w.Write([]byte(checks))
		case r.URL.Path == "/user":
			_, _ = w.Write([]byte(`{"login":"someone"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return github.Client{Token: "test-token", BaseURL: srv.URL}
}

func TestPullRequestsWithCommit(t *testing.T) {
	ctx := context.Background()
	repo := github.Repo{Owner: "acme", Name: "pawly"}
	const open = `[{"number":7,"title":"Reminders page","state":"open","html_url":"https://github.com/acme/pawly/pull/7","draft":true,"comments":2,"updated_at":"2026-09-14T10:00:00Z","head":{"ref":"feat/reminders","sha":"abc"}}]`

	t.Run("checks all green", func(t *testing.T) {
		prs, err := stub(t, open, `{"total_count":2,"check_runs":[{"status":"completed","conclusion":"success"},{"status":"completed","conclusion":"success"}]}`).
			PullRequestsWithCommit(ctx, repo, "abc")
		if err != nil || len(prs) != 1 {
			t.Fatalf("PullRequestsWithCommit() = %+v, %v", prs, err)
		}
		pr := prs[0]
		if pr.Number != 7 || pr.State != "open" || !pr.Draft || pr.Checks != "passing" || pr.Comments != 2 || pr.HeadSHA != "abc" || pr.HeadBranch != "feat/reminders" {
			t.Errorf("PullRequestsWithCommit() = %+v", pr)
		}
	})

	t.Run("one failure makes it failing", func(t *testing.T) {
		prs, _ := stub(t, open, `{"total_count":2,"check_runs":[{"status":"completed","conclusion":"success"},{"status":"completed","conclusion":"failure"}]}`).
			PullRequestsWithCommit(ctx, repo, "abc")
		if pr := prs[0]; pr.Checks != "failing" {
			t.Errorf("Checks = %q, want failing", pr.Checks)
		}
	})

	t.Run("anything still running is pending", func(t *testing.T) {
		prs, _ := stub(t, open, `{"total_count":2,"check_runs":[{"status":"in_progress"},{"status":"completed","conclusion":"success"}]}`).
			PullRequestsWithCommit(ctx, repo, "abc")
		if pr := prs[0]; pr.Checks != "pending" {
			t.Errorf("Checks = %q, want pending", pr.Checks)
		}
	})

	t.Run("no checks is not a failure", func(t *testing.T) {
		prs, _ := stub(t, open, `{"total_count":0,"check_runs":[]}`).PullRequestsWithCommit(ctx, repo, "abc")
		if pr := prs[0]; pr.Checks != "" {
			t.Errorf("Checks = %q, want empty", pr.Checks)
		}
	})

	t.Run("a merged pull request says so", func(t *testing.T) {
		merged := strings.Replace(open, `"state":"open"`, `"state":"closed","merged_at":"2026-09-14T11:00:00Z"`, 1)
		prs, _ := stub(t, merged, `{"total_count":0}`).PullRequestsWithCommit(ctx, repo, "abc")
		if pr := prs[0]; pr.State != "merged" {
			t.Errorf("State = %q, want merged", pr.State)
		}
	})

	t.Run("a commit with no pull request is not an error", func(t *testing.T) {
		prs, err := stub(t, `[]`, `{}`).PullRequestsWithCommit(ctx, repo, "abc")
		if err != nil || len(prs) != 0 {
			t.Errorf("PullRequestsWithCommit() = %+v, %v; want none, nil", prs, err)
		}
	})

	t.Run("a commit GitHub never saw is not an error", func(t *testing.T) {
		prs, err := stub(t, "", `{}`).PullRequestsWithCommit(ctx, repo, "abc")
		if err != nil || len(prs) != 0 {
			t.Errorf("PullRequestsWithCommit() = %+v, %v; want none, nil", prs, err)
		}
	})

	t.Run("the most recently updated comes first", func(t *testing.T) {
		two := `[{"number":1,"state":"closed","updated_at":"2026-09-01T10:00:00Z","head":{"sha":"old"}},{"number":2,"state":"open","updated_at":"2026-09-20T10:00:00Z","head":{"sha":"abc"}}]`
		prs, _ := stub(t, two, `{"total_count":0}`).PullRequestsWithCommit(ctx, repo, "abc")
		if len(prs) != 2 || prs[0].Number != 2 {
			t.Errorf("PullRequestsWithCommit() = %+v, want #2 first", prs)
		}
	})
}

func TestRefusedTokenSaysWhatToDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Bad credentials"})
	}))
	defer srv.Close()
	_, err := github.Client{Token: "stale", BaseURL: srv.URL}.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "agentbox auth github") {
		t.Errorf("Login() = %v, want it to name the command that fixes it", err)
	}
}

// PullRequests lists a repository's pull requests in one call, and enriches
// only the open ones with what the list endpoint doesn't carry: checks, diff
// size and comment count. A closed one is left as the list has it, since
// nobody is about to act on its history.
func TestPullRequestsListsAndEnrichesOnlyOpenOnes(t *testing.T) {
	ctx := context.Background()
	repo := github.Repo{Owner: "acme", Name: "pawly"}
	const list = `[
		{"number":9,"title":"Open one","state":"open","html_url":"https://github.com/acme/pawly/pull/9","draft":false,"updated_at":"2026-09-15T10:00:00Z","base":{"ref":"main"},"head":{"ref":"agentbox/agent-02","sha":"def"}},
		{"number":7,"title":"Merged one","state":"closed","merged_at":"2026-09-14T11:00:00Z","html_url":"https://github.com/acme/pawly/pull/7","base":{"ref":"main"},"head":{"ref":"agentbox/agent-01","sha":"abc"}}
	]`
	var sawDetail, sawChecksForClosed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/pawly/pulls":
			if got := r.URL.Query().Get("state"); got != "all" {
				t.Errorf("state = %q, want all", got)
			}
			_, _ = w.Write([]byte(list))
		case "/repos/acme/pawly/pulls/9":
			sawDetail = true
			_, _ = w.Write([]byte(`{"additions":12,"deletions":3,"comments":4}`))
		case "/repos/acme/pawly/pulls/7":
			t.Errorf("fetched detail for a closed pull request")
		case "/repos/acme/pawly/commits/def/check-runs":
			_, _ = w.Write([]byte(`{"total_count":1,"check_runs":[{"status":"completed","conclusion":"success"}]}`))
		case "/repos/acme/pawly/commits/abc/check-runs":
			sawChecksForClosed = true
			_, _ = w.Write([]byte(`{"total_count":0}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	prs, err := github.Client{Token: "test-token", BaseURL: srv.URL}.PullRequests(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 {
		t.Fatalf("PullRequests() = %d pull requests, want 2", len(prs))
	}
	open := prs[0]
	if open.Number != 9 || open.Checks != "passing" || open.Additions != 12 || open.Deletions != 3 || open.Comments != 4 {
		t.Errorf("open pull request = %+v", open)
	}
	if open.BaseBranch != "main" || open.HeadBranch != "agentbox/agent-02" {
		t.Errorf("open pull request branches = %+v", open)
	}
	if !sawDetail {
		t.Error("never fetched the open pull request's detail")
	}
	merged := prs[1]
	if merged.Number != 7 || merged.State != "merged" || merged.Checks != "" || merged.Additions != 0 {
		t.Errorf("merged pull request = %+v, want no enrichment", merged)
	}
	if sawChecksForClosed {
		t.Error("fetched checks for a closed pull request")
	}
}

// Info reports whether the token can push, when GitHub says so, and which
// merge methods the repository accepts.
func TestInfoReportsPushAccessAndMergeMethods(t *testing.T) {
	ctx := context.Background()
	repo := github.Repo{Owner: "acme", Name: "pawly"}

	t.Run("known", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"default_branch":"main","allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":false,"permissions":{"push":true}}`))
		}))
		defer srv.Close()
		info, err := github.Client{Token: "test-token", BaseURL: srv.URL}.Info(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		if !info.CanPush || !info.CanPushKnown || info.DefaultBranch != "main" {
			t.Errorf("Info() = %+v", info)
		}
		if !info.AllowMergeCommit || !info.AllowSquashMerge || info.AllowRebaseMerge {
			t.Errorf("Info() merge methods = %+v", info)
		}
	})

	t.Run("unknown when GitHub omits permissions", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		}))
		defer srv.Close()
		info, err := github.Client{Token: "test-token", BaseURL: srv.URL}.Info(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		if info.CanPushKnown {
			t.Errorf("Info() = %+v, want CanPushKnown false without a permissions field", info)
		}
	})
}

// Merge sends the chosen method, and surfaces GitHub's own reason when it
// refuses: a conflict, a missing review, a protected branch.
func TestMerge(t *testing.T) {
	ctx := context.Background()
	repo := github.Repo{Owner: "acme", Name: "pawly"}

	t.Run("success sends the merge method", func(t *testing.T) {
		var gotMethod string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("method = %s, want PUT", r.Method)
			}
			var body struct {
				MergeMethod string `json:"merge_method"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotMethod = body.MergeMethod
			_, _ = w.Write([]byte(`{"sha":"abc","merged":true,"message":"Pull Request successfully merged"}`))
		}))
		defer srv.Close()
		err := github.Client{Token: "test-token", BaseURL: srv.URL}.Merge(ctx, repo, 9, github.MergeSquash)
		if err != nil {
			t.Fatal(err)
		}
		if gotMethod != "squash" {
			t.Errorf("merge_method = %q, want squash", gotMethod)
		}
	})

	t.Run("refusal explains why", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "At least 1 approving review is required by reviewers with write access."})
		}))
		defer srv.Close()
		err := github.Client{Token: "test-token", BaseURL: srv.URL}.Merge(ctx, repo, 9, github.MergeCommit)
		if err == nil || !strings.Contains(err.Error(), "approving review") {
			t.Errorf("Merge() = %v, want GitHub's own reason", err)
		}
	})

	t.Run("a token without write access says so", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Resource not accessible by personal access token"})
		}))
		defer srv.Close()
		err := github.Client{Token: "read-only", BaseURL: srv.URL}.Merge(ctx, repo, 9, github.MergeCommit)
		if err == nil || !strings.Contains(err.Error(), "not accessible") || !strings.Contains(err.Error(), "agentbox auth github") {
			t.Errorf("Merge() = %v, want the token's own reason and the fix", err)
		}
	})
}

func TestMergeMethodValid(t *testing.T) {
	for method, want := range map[github.MergeMethod]bool{
		github.MergeCommit: true, github.MergeSquash: true, github.MergeRebase: true, "rewrite-history": false, "": false,
	} {
		if got := method.Valid(); got != want {
			t.Errorf("%q.Valid() = %v, want %v", method, got, want)
		}
	}
}

// What GitHub answers decides what a caller can say about it: 401 is the
// token, 404 and 403 are this account not being able to see the repository —
// GitHub hides a private repository rather than admitting it exists — and a
// rate limit is neither, since waiting fixes it and changing account doesn't.
func TestRefusalsAreToldApartByStatus(t *testing.T) {
	ctx := context.Background()
	repo := github.Repo{Owner: "acme", Name: "app"}
	for _, tc := range []struct {
		name      string
		status    int
		message   string
		rateLimit bool
		want      error
	}{
		{name: "a revoked token", status: http.StatusUnauthorized, message: "Bad credentials", want: github.ErrBadToken},
		{name: "a private repository of another org", status: http.StatusNotFound, message: "Not Found", want: github.ErrNoAccess},
		{name: "a repository the account is blocked from", status: http.StatusForbidden, message: "Resource not accessible by personal access token", want: github.ErrNoAccess},
		{name: "a rate limit", status: http.StatusForbidden, message: "API rate limit exceeded", rateLimit: true},
		{name: "anything else", status: http.StatusInternalServerError, message: "Server Error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.rateLimit {
					w.Header().Set("X-RateLimit-Remaining", "0")
				}
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": tc.message})
			}))
			defer srv.Close()
			_, err := github.Client{Token: "test-token", BaseURL: srv.URL}.PullRequests(ctx, repo)
			if err == nil {
				t.Fatal("PullRequests() = nil, want an error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("PullRequests() = %v, want it to wrap %v", err, tc.want)
			}
			if tc.want == nil && (errors.Is(err, github.ErrNoAccess) || errors.Is(err, github.ErrBadToken)) {
				t.Errorf("PullRequests() = %v, want it to blame neither the token nor the account", err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("PullRequests() = %v, want GitHub's own message in it", err)
			}
		})
	}
}
