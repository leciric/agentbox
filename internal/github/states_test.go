package github

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// One request asks for the numbered pull requests and the ones opened from
// each branch, and leaves out what GitHub doesn't know.
func TestPullRequestStates(t *testing.T) {
	var query string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		query = string(b)
		_, _ = w.Write([]byte(`{"data":{"repository":{
			"pr234":{"number":234,"title":"fix: skills","state":"MERGED","headRefName":"agentbox/fix-skills","closedAt":"2026-10-09T10:00:00Z"},
			"pr235":{"number":235,"title":"feat: reload","state":"OPEN","headRefName":"agentbox/reload","closedAt":null},
			"pr9":null,
			"b3":{"nodes":[{"number":240,"title":"feat: update","state":"CLOSED","headRefName":"agentbox/feat-x","closedAt":"2026-10-08T10:00:00Z"}]},
			"b4":{"nodes":[]}}},
			"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a PullRequest with the number of 9."}]}`))
	}))
	defer stub.Close()
	c := Client{Token: "tok", BaseURL: stub.URL}
	prs, branches, err := c.PullRequestStates(context.Background(), Repo{"acme", "hello"},
		[]int{234, 235, 9}, []string{"agentbox/feat-x", "agentbox/none"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, `b3: pullRequests(headRefName: \"agentbox/feat-x\"`) {
		t.Errorf("the query didn't ask for the branch: %s", query)
	}
	if got := prs[234]; got.State != "merged" || !got.ClosedAt.Equal(time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("#234 = %+v", got)
	}
	if got := prs[235]; got.State != "open" || !got.ClosedAt.IsZero() {
		t.Errorf("#235 = %+v", got)
	}
	if _, ok := prs[9]; ok {
		t.Error("a number GitHub doesn't know came back")
	}
	if got := branches["agentbox/feat-x"]; len(got) != 1 || got[0].State != "closed" {
		t.Errorf("branch = %+v", got)
	}
	if _, ok := branches["agentbox/none"]; ok {
		t.Error("a branch with no pull request came back")
	}
}
