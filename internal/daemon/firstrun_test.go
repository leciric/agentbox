package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
)

// A daemon on a database made just now has the first run to go through, and
// ending it ends it for good.
func TestFirstRunEndsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	got, err := d.client.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.FirstRun {
		t.Fatal("a new installation has no first run to go through")
	}
	if got, err = d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{FirstRunDone: new(true)}); err != nil {
		t.Fatal(err)
	}
	if got.FirstRun {
		t.Error("the first run is still to do after it was ended")
	}
}

// The first run's repositories are the default GitHub account's, and there
// are none to list before there is an account.
func TestGitHubReposAreTheDefaultAccounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if _, err := d.client.GitHubRepos(ctx); err == nil {
		t.Fatal("listed repositories with no GitHub account")
	}
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghp_mine" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`[{"full_name":"me/pawly","private":true,"pushed_at":"2026-10-01T10:00:00Z","clone_url":"https://github.com/me/pawly.git","permissions":{"push":true}}]`))
	}))
	t.Cleanup(gh.Close)
	d.setGitHub(t, gh.URL)
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveGitHubToken("", "ghp_mine"); err != nil {
		t.Fatal(err)
	}
	repos, err := d.client.GitHubRepos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := api.GitHubRepo{FullName: "me/pawly", Private: true, PushedAt: "2026-10-01T10:00:00Z", CloneURL: "https://github.com/me/pawly.git"}
	if len(repos) != 1 || repos[0] != want {
		t.Errorf("repos = %+v, want [%+v]", repos, want)
	}
}
