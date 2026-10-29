package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/github"
)

// The account's hourly limit running out keeps the list that was read, says
// so once with when GitHub is read again, and sends nothing more until then.
// What was spent, and on what, is in agentbox github usage.
func TestARateLimitKeepsTheListAndSaysWhenItEnds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)

	reset := time.Now().Add(40 * time.Minute).Truncate(time.Second)
	var limited atomic.Bool
	var hits atomic.Int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(reset.Unix()))
		w.Header().Set("X-RateLimit-Resource", "core")
		if r.URL.Path == "/graphql" {
			w.Header().Set("X-RateLimit-Resource", "graphql")
		}
		if limited.Load() {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Used", "5000")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded for user ID 1."}`))
			return
		}
		w.Header().Set("X-RateLimit-Remaining", "4000")
		w.Header().Set("X-RateLimit-Used", "1000")
		switch r.URL.Path {
		case "/repos/acme/hello-stack/pulls":
			_, _ = w.Write([]byte(`[{"number":9,"title":"Reminders","state":"open","html_url":"https://github.com/acme/hello-stack/pull/9","updated_at":"2026-10-01T10:00:00Z","base":{"ref":"main"},"head":{"ref":"feat/reminders","sha":"abc"}}]`))
		case "/graphql":
			_, _ = w.Write([]byte(`{"data":{"repository":{"pr9":{"additions":1,"deletions":0,"comments":{"totalCount":0},"commits":{"nodes":[]}}}}}`))
		case "/repos/acme/hello-stack":
			_, _ = w.Write([]byte(`{"default_branch":"main","permissions":{"push":true}}`))
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer stub.Close()
	d.setGitHub(t, stub.URL)

	if out := pullsOf(t, d, "hello-stack"); len(out.PullRequests) != 1 || out.GitHubError != nil {
		t.Fatalf("before the limit: %+v", out)
	}

	limited.Store(true)
	d.srv.pulls.invalidate("acme/hello-stack")
	out := pullsOf(t, d, "hello-stack")
	if e := out.GitHubError; e == nil || e.Kind != api.GitHubRateLimited || e.ResetAt == nil || !e.ResetAt.Equal(reset) {
		t.Fatalf("GitHubError = %+v, want rateLimited until %v", out.GitHubError, reset)
	}
	if len(out.PullRequests) != 1 {
		t.Errorf("the list read before the limit is gone: %+v", out.PullRequests)
	}

	sent := hits.Load()
	d.srv.pulls.invalidate("acme/hello-stack")
	if out := pullsOf(t, d, "hello-stack"); out.GitHubError == nil || out.GitHubError.Kind != api.GitHubRateLimited {
		t.Errorf("GitHubError = %+v, want still rateLimited", out.GitHubError)
	}
	if hits.Load() != sent {
		t.Errorf("%d more requests while the limit was spent, want none", hits.Load()-sent)
	}

	usage, err := d.client.GitHubUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Tests running alongside read their own stubs with the same token, each
	// an account of its own here: this one's is the one whose list was held.
	var list *api.GitHubCallCount
	named := false
	for _, u := range usage.Accounts {
		if u.Token != github.Fingerprint("test-token") {
			continue
		}
		named = named || u.Account != ""
		for i, c := range u.Calls {
			if c.Caller == "pulls" && c.Endpoint == "/repos/{owner}/{repo}/pulls" && c.Held > 0 {
				list = &u.Calls[i]
			}
		}
	}
	if !named {
		t.Errorf("the token isn't named by its stored account: %+v", usage.Accounts)
	}
	if list == nil || list.Sent < 2 {
		t.Errorf("the list's count = %+v, want it sent and then held", list)
	}
}
