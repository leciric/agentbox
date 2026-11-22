package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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

// With events asked for, the same request reads who the account is, the
// default branch's head and its checks, the issues with the labels named,
// and who opened each pull request and whom a review is asked of.
func TestWatchRepositoryEvents(t *testing.T) {
	var query string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		query = string(b)
		_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"me"},"repository":{
			"open":{"nodes":[{"number":9,"state":"OPEN","author":null,"createdAt":"2026-10-20T10:00:00Z",
				"reviewRequests":{"nodes":[{"requestedReviewer":{"__typename":"User","login":"me"}},{"requestedReviewer":{"__typename":"Team"}}]},"commits":{"nodes":[]}}]},
			"defaultBranchRef":{"name":"trunk","target":{"oid":"abc","url":"u","messageHeadline":"Bump","statusCheckRollup":{"state":"ERROR",
				"contexts":{"nodes":[{"__typename":"StatusContext","context":"ci/build","state":"ERROR","targetUrl":"https://ci/b"}]}}}},
			"issues":{"nodes":[{"number":4,"title":"Crash","url":"i4","body":"b","author":{"login":"rep"},"labels":{"nodes":[{"name":"bug"}]}}]}}}}`))
	}))
	defer stub.Close()
	c := Client{Token: "tok", BaseURL: stub.URL}
	w, err := c.WatchRepository(context.Background(), Repo{"acme", "hello"}, nil, WatchOptions{Events: true, Labels: []string{"bug", "p\"1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, `labels: [\"bug\",\"p\\\"1\"]`) || !strings.Contains(query, "viewer { login }") {
		t.Errorf("query = %s", query)
	}
	ev := w.Events
	if ev == nil || ev.Viewer != "me" || ev.DefaultBranch != "trunk" || ev.Head.SHA != "abc" || ev.Head.Checks != "failing" ||
		len(ev.Head.Failing) != 1 || ev.Head.Failing[0].Name != "ci/build" {
		t.Fatalf("events = %+v", ev)
	}
	if len(ev.Issues) != 1 || ev.Issues[0].Author != "rep" || ev.Issues[0].Labels[0] != "bug" {
		t.Errorf("issues = %+v", ev.Issues)
	}
	if pr := w.Open[0]; pr.Author != "" || len(pr.ReviewRequested) != 1 || pr.ReviewRequested[0] != "me" || pr.CreatedAt.IsZero() {
		t.Errorf("pull request = %+v", pr)
	}
	// Without events, none of it is asked for.
	if _, err := c.WatchPullRequests(context.Background(), Repo{"acme", "hello"}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query, `\n  viewer {`) || strings.Contains(query, "defaultBranchRef") || strings.Contains(query, "issues(") {
		t.Errorf("a plain watch asked for events: %s", query)
	}
}

// check builds a check run as GitHub's GraphQL answers with one: a run of the
// workflow wf (its node ID), numbered n, or an app's check with no workflow
// when wf is "".
func check(name, status, conclusion, wf string, n int) map[string]any {
	c := map[string]any{"__typename": "CheckRun", "name": name, "status": status, "detailsUrl": "https://ci/" + name}
	if conclusion != "" {
		c["conclusion"] = conclusion
	}
	suite := map[string]any{"workflowRun": nil}
	if wf != "" {
		suite["workflowRun"] = map[string]any{"runNumber": n, "workflow": map[string]any{"id": wf}}
	}
	c["checkSuite"] = suite
	return c
}

func done(name, conclusion, wf string, n int) map[string]any {
	return check(name, "COMPLETED", conclusion, wf, n)
}

// What GitHub rolls a head's checks up to isn't believed when it says
// failing: a run cancelled because a newer run of its workflow took its place
// on the same commit (a pull request opened, then labelled or marked ready) has
// not failed, and a run waiting for approval is pending. A run cancelled with
// no newer one, and any real failure, still fail.
func TestWatchChecksJudgedApartFromTheRollup(t *testing.T) {
	status := func(state string) map[string]any {
		return map[string]any{"__typename": "StatusContext", "context": "ci/legacy", "state": state, "targetUrl": "https://ci/legacy"}
	}
	for _, c := range []struct {
		name     string
		rollup   string // GitHub's word
		total    int    // how many checks GitHub has, when more than are read
		contexts []map[string]any
		checks   string   // the watch's word
		failing  []string // what it names
	}{
		{
			name:   "cancelled by a newer run that passed",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("CI / test", "CANCELLED", "ci", 1), done("CI / lint", "CANCELLED", "ci", 1),
				done("CI / test", "SUCCESS", "ci", 2), done("CI / lint", "SUCCESS", "ci", 2),
			},
			checks: "passing",
		},
		{
			name:   "cancelled by a newer run still going",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("CI / test", "CANCELLED", "ci", 1),
				check("CI / test", "IN_PROGRESS", "", "ci", 2),
			},
			checks: "pending",
		},
		{
			name:   "cancelled by a newer run that is queued",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("CI / test", "CANCELLED", "ci", 1),
				check("CI / test", "QUEUED", "", "ci", 2),
			},
			checks: "pending",
		},
		{
			name:   "cancelled by a newer run that failed names only the newer one's failure",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("CI / test", "CANCELLED", "ci", 1), done("CI / lint", "CANCELLED", "ci", 1),
				done("CI / test", "FAILURE", "ci", 2), done("CI / lint", "SUCCESS", "ci", 2),
			},
			checks:  "failing",
			failing: []string{"CI / test"},
		},
		{
			name:     "cancelled with no newer run",
			rollup:   "FAILURE",
			contexts: []map[string]any{done("CI / test", "CANCELLED", "ci", 1), done("CI / lint", "SUCCESS", "ci", 1)},
			checks:   "failing",
			failing:  []string{"CI / test"},
		},
		{
			name:   "cancelled in the newest run, with an older one that passed",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("CI / test", "SUCCESS", "ci", 1),
				done("CI / test", "CANCELLED", "ci", 2),
			},
			checks:  "failing",
			failing: []string{"CI / test"},
		},
		{
			name:   "cancelled while a newer run of another workflow exists",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("CI / test", "CANCELLED", "ci", 1),
				done("Docs / build", "SUCCESS", "docs", 7),
			},
			checks:  "failing",
			failing: []string{"CI / test"},
		},
		{
			name:   "cancelled within the same run as the others, a job cancelled by hand",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("CI / test", "CANCELLED", "ci", 3), done("CI / lint", "SUCCESS", "ci", 3),
			},
			checks:  "failing",
			failing: []string{"CI / test"},
		},
		{
			name:   "cancelled check an app reported, beside a workflow's newer run",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("deploy-preview", "CANCELLED", "", 0),
				done("CI / test", "SUCCESS", "ci", 2),
			},
			checks:  "failing",
			failing: []string{"deploy-preview"},
		},
		{
			name:     "waiting for approval",
			rollup:   "FAILURE",
			contexts: []map[string]any{done("CI / test", "ACTION_REQUIRED", "ci", 1), done("CI / lint", "SUCCESS", "ci", 1)},
			checks:   "pending",
		},
		{
			name:   "waiting for approval, and a real failure",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("CI / test", "ACTION_REQUIRED", "ci", 1),
				done("Docs / build", "TIMED_OUT", "docs", 1),
			},
			checks:  "failing",
			failing: []string{"Docs / build"},
		},
		{
			name:   "cancelled by a newer run, and another run waiting for approval",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("CI / test", "CANCELLED", "ci", 1), done("CI / test", "SUCCESS", "ci", 2),
				done("Release / build", "ACTION_REQUIRED", "release", 1),
			},
			checks: "pending",
		},
		{
			name:   "outright failures all fail",
			rollup: "FAILURE",
			contexts: []map[string]any{
				done("a", "FAILURE", "ci", 1), done("b", "TIMED_OUT", "ci", 1), done("c", "STARTUP_FAILURE", "ci", 1),
				status("ERROR"),
			},
			checks:  "failing",
			failing: []string{"a", "b", "c", "ci/legacy"},
		},
		{
			name:     "a commit status that failed",
			rollup:   "FAILURE",
			contexts: []map[string]any{done("CI / test", "CANCELLED", "ci", 1), done("CI / test", "SUCCESS", "ci", 2), status("FAILURE")},
			checks:   "failing",
			failing:  []string{"ci/legacy"},
		},
		{
			name:     "a commit status still pending",
			rollup:   "FAILURE",
			contexts: []map[string]any{done("CI / test", "CANCELLED", "ci", 1), done("CI / test", "SUCCESS", "ci", 2), status("PENDING")},
			checks:   "pending",
		},
		{
			name:   "checks not all read: the failure may be in the ones left out",
			rollup: "FAILURE",
			total:  60,
			contexts: []map[string]any{
				done("CI / test", "CANCELLED", "ci", 1), done("CI / test", "SUCCESS", "ci", 2),
			},
			checks: "failing",
		},
		{
			name:   "checks not all read, and one read failed",
			rollup: "FAILURE",
			total:  60,
			contexts: []map[string]any{
				done("CI / test", "FAILURE", "ci", 2),
			},
			checks:  "failing",
			failing: []string{"CI / test"},
		},
		{
			name:     "a roll-up that says pending stays pending",
			rollup:   "PENDING",
			contexts: []map[string]any{done("CI / test", "SUCCESS", "ci", 1), check("CI / lint", "IN_PROGRESS", "", "ci", 1)},
			checks:   "pending",
		},
		{
			name:     "a roll-up that says passing stays passing",
			rollup:   "SUCCESS",
			contexts: []map[string]any{done("CI / test", "CANCELLED", "ci", 1), done("CI / test", "SUCCESS", "ci", 2)},
			checks:   "passing",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			total := c.total
			if total == 0 {
				total = len(c.contexts)
			}
			var query string
			stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				query = string(b)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{
					"open": map[string]any{"nodes": []any{map[string]any{
						"number": 9, "state": "OPEN", "headRefOid": "abc", "mergeable": "MERGEABLE",
						"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"oid": "abc", "statusCheckRollup": map[string]any{
							"state": c.rollup, "contexts": map[string]any{"totalCount": total, "nodes": c.contexts},
						}}}}},
					}}},
				}}})
			}))
			defer stub.Close()
			w, err := Client{BaseURL: stub.URL}.WatchPullRequests(context.Background(), Repo{"acme", "hello"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Without the workflow run in the query, GitHub would never say which
			// run a cancelled check belongs to.
			if !strings.Contains(query, "workflowRun { runNumber workflow { id } }") || !strings.Contains(query, "totalCount") {
				t.Errorf("the query didn't ask which workflow run each check is, or how many there are: %s", query)
			}
			if len(w.Open) != 1 {
				t.Fatalf("Open = %+v", w.Open)
			}
			pr := w.Open[0]
			if pr.Checks != c.checks {
				t.Errorf("Checks = %q, want %q", pr.Checks, c.checks)
			}
			var failing []string
			for _, f := range pr.Failing {
				failing = append(failing, f.Name)
			}
			if !slices.Equal(failing, c.failing) {
				t.Errorf("Failing = %v, want %v", failing, c.failing)
			}
		})
	}
}
