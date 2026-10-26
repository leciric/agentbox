package github_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/internal/github"
)

func TestIssuesLeavesOutPullRequestsAndFiltersByLabel(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/pawly/issues" {
			t.Errorf("asked %s", r.URL.Path)
		}
		asked = r.URL.RawQuery
		_, _ = w.Write([]byte(`[
			{"number": 7, "title": "Reminders crash", "html_url": "https://github.com/acme/pawly/issues/7", "state": "open",
			 "comments": 2, "labels": [{"name": "agentbox", "color": "0e8a16"}], "user": {"login": "ana"}},
			{"number": 8, "title": "A pull request", "pull_request": {"url": "x"}}
		]`))
	}))
	defer srv.Close()
	c := github.Client{Token: "t", BaseURL: srv.URL}
	issues, err := c.Issues(context.Background(), github.Repo{Owner: "acme", Name: "pawly"}, "agentbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 7 || issues[0].Author != "ana" || issues[0].Labels[0].Name != "agentbox" {
		t.Fatalf("issues = %+v, want only #7", issues)
	}
	if !strings.Contains(asked, "labels=agentbox") || !strings.Contains(asked, "state=open") {
		t.Errorf("query = %q", asked)
	}
}

func TestIssueReadsItsComments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/pawly/issues/7":
			_, _ = w.Write([]byte(`{"number": 7, "title": "Reminders crash", "body": "It crashes.", "state": "open", "comments": 1}`))
		case "/repos/acme/pawly/issues/7/comments":
			_, _ = w.Write([]byte(`[{"body": "On Android too.", "user": {"login": "bo"}}]`))
		case "/repos/acme/pawly/issues/8":
			_, _ = w.Write([]byte(`{"number": 8, "pull_request": {"url": "x"}}`))
		default:
			t.Errorf("asked %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := github.Client{Token: "t", BaseURL: srv.URL}
	repo := github.Repo{Owner: "acme", Name: "pawly"}
	issue, comments, err := c.Issue(context.Background(), repo, 7)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Body != "It crashes." || len(comments) != 1 || comments[0].Author != "bo" {
		t.Fatalf("issue = %+v, comments = %+v", issue, comments)
	}
	if _, _, err := c.Issue(context.Background(), repo, 8); err == nil {
		t.Error("a pull request's number was read as an issue")
	}
}
