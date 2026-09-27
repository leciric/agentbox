package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// One request reads the open pull requests and the ones asked for by number,
// with their checks folded into a word and the failing ones named, whichever
// kind of check — a check run or a commit status — reported them.
func TestWatchPullRequests(t *testing.T) {
	var query string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("request = %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		query = string(b)
		_, _ = w.Write([]byte(`{"data":{"rateLimit":{"remaining":4990,"resetAt":"2026-09-27T13:00:00Z"},"repository":{
			"open":{"nodes":[{"number":9,"title":"Reminders","url":"u9","state":"OPEN","headRefName":"feat/r","headRefOid":"abc","baseRefName":"main",
				"mergeable":"CONFLICTING","reviewDecision":"CHANGES_REQUESTED","commits":{"nodes":[{"commit":{"oid":"abc","statusCheckRollup":{"state":"FAILURE",
				"contexts":{"nodes":[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://ci/test"},
					{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS"},
					{"__typename":"StatusContext","context":"ci/legacy","state":"ERROR","targetUrl":"https://ci/legacy"}]}}}}]}}]},
			"pr4":{"number":4,"state":"MERGED","headRefOid":"def","mergeable":"UNKNOWN","commits":{"nodes":[]}},
			"pr5":null}}}`))
	}))
	defer stub.Close()
	c := Client{Token: "tok", BaseURL: stub.URL}
	w, err := c.WatchPullRequests(context.Background(), Repo{"acme", "hello"}, []int{5, 4})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "pr4: pullRequest(number: 4)") || !strings.Contains(query, "pr5: pullRequest(number: 5)") {
		t.Errorf("the query didn't ask for the followed numbers: %s", query)
	}
	if len(w.Open) != 1 {
		t.Fatalf("Open = %+v", w.Open)
	}
	pr := w.Open[0]
	if pr.Checks != "failing" || pr.Mergeable != "conflicting" || pr.Review != "changes_requested" || pr.State != "open" {
		t.Errorf("open pull request = %+v", pr)
	}
	if len(pr.Failing) != 2 || pr.Failing[0] != (FailingCheck{"test", "https://ci/test"}) || pr.Failing[1].Name != "ci/legacy" {
		t.Errorf("Failing = %+v", pr.Failing)
	}
	if got := w.Asked[4]; got.State != "merged" {
		t.Errorf("Asked[4] = %+v", got)
	}
	if _, ok := w.Asked[5]; ok {
		t.Error("a number GitHub doesn't know came back")
	}
	if w.Remaining != 4990 || !w.Reset.Equal(time.Date(2026, 9, 27, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("budget = %d until %v", w.Remaining, w.Reset)
	}
}

// GitHub saying to slow down is a RateLimitError that says until when, in
// each of the ways it says it.
func TestWatchPullRequestsRateLimits(t *testing.T) {
	for _, c := range []struct {
		name    string
		answer  func(w http.ResponseWriter)
		atLeast time.Duration
	}{
		{"secondary limit", func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusForbidden)
		}, 119 * time.Second},
		{"budget spent", func(w http.ResponseWriter) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "4102444800") // 2100
			w.WriteHeader(http.StatusForbidden)
		}, 24 * time.Hour},
		{"graphql says so", func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"data":{"rateLimit":{"remaining":0,"resetAt":"2100-01-01T00:00:00Z"},"repository":null},"errors":[{"type":"RATE_LIMITED","message":"limit"}]}`))
		}, 24 * time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { c.answer(w) }))
			defer stub.Close()
			_, err := Client{BaseURL: stub.URL}.WatchPullRequests(context.Background(), Repo{"acme", "hello"}, nil)
			var limited *RateLimitError
			if !errors.As(err, &limited) || !errors.Is(err, ErrRateLimited) {
				t.Fatalf("err = %v, want a RateLimitError", err)
			}
			if time.Until(limited.Until) < c.atLeast {
				t.Errorf("Until = %v, want at least %v from now", limited.Until, c.atLeast)
			}
		})
	}
}

// A repository the account can't see comes back as null, not a 404.
func TestWatchPullRequestsNoAccess(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a Repository"}]}`))
	}))
	defer stub.Close()
	_, err := Client{BaseURL: stub.URL}.WatchPullRequests(context.Background(), Repo{"acme", "hello"}, nil)
	if !errors.Is(err, ErrNoAccess) {
		t.Fatalf("err = %v, want ErrNoAccess", err)
	}
}
