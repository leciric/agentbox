package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/internal/github"
)

// UserRepos reads the pages GitHub has, up to its limit, and keeps only the
// repositories the account can push to, in GitHub's order.
func TestUserReposKeepsWhatTheAccountCanPushTo(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" || r.URL.Query().Get("sort") != "pushed" {
			t.Errorf("asked %s", r.URL)
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		n := 100
		if page == "2" {
			n = 2
		}
		var repos []string
		for i := range n {
			repos = append(repos, fmt.Sprintf(`{"full_name":"acme/r%s-%d","clone_url":"https://github.com/acme/r%s-%d.git","permissions":{"push":%t}}`, page, i, page, i, i%2 == 0))
		}
		_, _ = w.Write([]byte("[" + strings.Join(repos, ",") + "]"))
	}))
	defer srv.Close()

	got, err := github.Client{Token: "t", BaseURL: srv.URL}.UserRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pages, ",") != "1,2" {
		t.Errorf("read pages %v, want 1 and 2: the second was the last", pages)
	}
	if len(got) != 51 || got[0].FullName != "acme/r1-0" || got[50].FullName != "acme/r2-0" {
		t.Errorf("got %d repositories, first %+v; want the 51 it can push to, in order", len(got), got[0])
	}
}
