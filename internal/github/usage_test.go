package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// usageOf is what Usage says of c's account.
func usageOf(t *testing.T, c Client) AccountUsage {
	t.Helper()
	return c.account().snapshot()
}

func callOf(u AccountUsage, caller, endpoint string) CallCount {
	for _, c := range u.Calls {
		if c.Caller == caller && c.Endpoint == endpoint {
			return c
		}
	}
	return CallCount{}
}

func budget(w http.ResponseWriter, remaining, used int) {
	w.Header().Set("X-RateLimit-Limit", "5000")
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Used", strconv.Itoa(used))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	w.Header().Set("X-RateLimit-Resource", "core")
}

// A read GitHub answered before is asked for with its ETag, and the 304 —
// which GitHub doesn't count — is answered from what was kept.
func TestReadsAreConditional(t *testing.T) {
	var hits, conditional atomic.Int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		budget(w, 4000, 1000)
		if r.Header.Get("If-None-Match") == `"v1"` {
			conditional.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	}))
	defer stub.Close()
	c := Client{Token: "etag-token", BaseURL: stub.URL}
	ctx := WithCaller(context.Background(), "setup")
	for range 3 {
		login, err := c.Login(ctx)
		if err != nil || login != "octocat" {
			t.Fatalf("Login() = %q, %v", login, err)
		}
	}
	if hits.Load() != 3 || conditional.Load() != 2 {
		t.Errorf("%d requests, %d conditional; want 3 and 2", hits.Load(), conditional.Load())
	}
	u := usageOf(t, c)
	n := callOf(u, "setup", "/user")
	if n.Sent != 3 || n.NotModified != 2 || n.Counted() != 1 {
		t.Errorf("count = %+v, want 3 sent, 2 not modified", n)
	}
	if len(u.Rates) != 1 || u.Rates[0].Ours != 1 || u.Rates[0].Others != 999 {
		t.Errorf("rates = %+v, want 1 of ours and the other 999 someone else's", u.Rates)
	}
}

// Reads of the same thing at once are one request.
func TestReadsInFlightAreShared(t *testing.T) {
	var hits atomic.Int32
	release := make(chan struct{})
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	}))
	defer stub.Close()
	c := Client{Token: "flight-token", BaseURL: stub.URL}
	ctx := WithCaller(context.Background(), "view")
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := c.Login(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	// Every reader is in before the one request is answered.
	deadline := time.Now().Add(5 * time.Second)
	for hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	n := callOf(usageOf(t, c), "view", "/user")
	if int(hits.Load()) != n.Sent || n.Sent+n.Shared != 5 || n.Sent >= 5 {
		t.Errorf("%d requests for 5 reads, counted %+v", hits.Load(), n)
	}
}

// Once GitHub says the budget is spent, nothing more is sent until it resets.
func TestASpentBudgetSendsNothingUntilReset(t *testing.T) {
	var hits atomic.Int32
	reset := time.Now().Add(40 * time.Minute)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded for user ID 1."}`))
	}))
	defer stub.Close()
	c := Client{Token: "spent-token", BaseURL: stub.URL}
	repo := Repo{"acme", "app"}
	for range 3 {
		_, err := c.PullRequestByNumber(WithCaller(context.Background(), "merge"), repo, 224)
		var limited *RateLimitError
		if !errors.As(err, &limited) || limited.Until.Unix() != reset.Unix() {
			t.Fatalf("err = %v, want a RateLimitError until the reset", err)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("%d requests, want only the one GitHub refused", hits.Load())
	}
	if n := callOf(usageOf(t, c), "merge", "/repos/{owner}/{repo}/pulls/{n}"); n.Held != 2 {
		t.Errorf("count = %+v, want 2 held", n)
	}
}

// A background poll gives way while little is left; a person's click doesn't,
// and pollers are told to spread out before it comes to that.
func TestBackgroundReadsStandAsideWhenTheBudgetIsLow(t *testing.T) {
	var hits atomic.Int32
	var remaining atomic.Int32
	remaining.Store(900)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		left := int(remaining.Load())
		budget(w, left, 5000-left)
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	}))
	defer stub.Close()
	c := Client{Token: "low-token", BaseURL: stub.URL}
	if got := c.Slowdown(); got != 1 {
		t.Errorf("Slowdown() before anything is known = %v, want 1", got)
	}
	if _, err := c.Login(InBackground(context.Background(), "poll")); err != nil {
		t.Fatal(err)
	}
	if got := c.Slowdown(); got < 1.1 || got > 1.2 {
		t.Errorf("Slowdown() at 900 of 5000 = %v, want about 1.1", got)
	}
	remaining.Store(100)
	if _, err := c.Login(WithCaller(context.Background(), "click")); err != nil {
		t.Fatal(err)
	}
	if got := c.Slowdown(); got != maxSlowdown {
		t.Errorf("Slowdown() at 100 of 5000 = %v, want %v", got, maxSlowdown)
	}
	_, err := c.Login(InBackground(context.Background(), "poll"))
	var limited *RateLimitError
	if !errors.As(err, &limited) || !limited.Low {
		t.Fatalf("a background read at 100 left = %v, want it held", err)
	}
	if _, err := c.Login(WithCaller(context.Background(), "click")); err != nil {
		t.Errorf("a click at 100 left = %v, want it sent", err)
	}
	if hits.Load() != 3 {
		t.Errorf("%d requests, want 3", hits.Load())
	}
}

func TestEndpointOf(t *testing.T) {
	for path, want := range map[string]string{
		"/repos/stefanini-applications/onestop-app/pulls/224":                         "/repos/{owner}/{repo}/pulls/{n}",
		"/repos/acme/app/pulls?state=open&per_page=100&page=2":                        "/repos/{owner}/{repo}/pulls",
		"/repos/acme/app/commits/0123456789abcdef0123456789abcdef01234567/check-runs": "/repos/{owner}/{repo}/commits/{sha}/check-runs",
		"/repos/acme/app/commits/feat%2Fx/pulls":                                      "/repos/{owner}/{repo}/commits/{sha}/pulls",
		"/repos/acme/app/pulls/9/files?per_page=100&page=1":                           "/repos/{owner}/{repo}/pulls/{n}/files",
		"/graphql": "/graphql",
		"/user":    "/user",
	} {
		if got := endpointOf(path); got != want {
			t.Errorf("endpointOf(%q) = %q, want %q", path, got, want)
		}
	}
}
