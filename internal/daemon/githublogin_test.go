package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/github"
)

// fakeGH writes a gh whose `auth login --web` prints a code and ends as
// ending says (token, denied, expired, wait or anything else for an error),
// and whose `auth token` prints gho_device.
func fakeGH(t *testing.T, ending string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gh")
	script := `#!/bin/sh
case "$1 $2" in
"auth login")
	echo "! First copy your one-time code: ABCD-1234" >&2
	echo "Open this URL to continue in your web browser: https://github.com/login/device" >&2
	case ` + ending + ` in
	token) echo gho_device >"$GH_CONFIG_DIR/token" ;;
	denied) echo "failed to authenticate via web browser: access_denied" >&2; exit 1 ;;
	expired) echo "failed to authenticate via web browser: authentication timed out" >&2; exit 1 ;;
	wait) while :; do sleep 0.05; done ;;
	*) echo "error connecting to github.com" >&2; exit 1 ;;
	esac ;;
"auth token") cat "$GH_CONFIG_DIR/token" ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// githubUser is api.github.com's /user for the token gho_device.
var githubUser = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer gho_device" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	_, _ = w.Write([]byte(`{"login":"octocat"}`))
})

// startGitHubLoginDaemon is a daemon that signs in with the gh at bin.
func startGitHubLoginDaemon(t *testing.T, bin string) testDaemon {
	t.Helper()
	stub := httptest.NewServer(githubUser)
	t.Cleanup(stub.Close)
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{setup: func(s *Server) {
		s.githubGH = github.GHLogin{Bin: bin}
		s.installGH = false
	}})
	d.setGitHub(t, stub.URL)
	return d
}

func waitGitHubLogin(t *testing.T, d testDaemon, job string) api.GitHubLogin {
	t.Helper()
	var got api.GitHubLogin
	waitFor(t, "the GitHub sign-in to end", func() bool {
		var err error
		got, err = d.client.GitHubLogin(context.Background(), job)
		return err == nil && got.Status != api.JobRunning
	})
	return got
}

func TestGitHubLoginStoresTheTokenAsTheDefault(t *testing.T) {
	t.Parallel()
	d := startGitHubLoginDaemon(t, fakeGH(t, "token"))
	ctx := context.Background()

	j, err := d.client.StartGitHubLogin(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	got := waitGitHubLogin(t, d, j.ID)
	if got.Status != api.JobSucceeded || got.User != "octocat" || got.Account != credentials.DefaultAccount || got.Outcome != "" {
		t.Fatalf("GitHubLogin() = %+v", got)
	}
	if got.UserCode != "ABCD-1234" || got.VerificationURI != "https://github.com/login/device" || got.ExpiresAt == nil {
		t.Errorf("the code shown = %+v", got)
	}
	auth, err := d.client.Auth(ctx)
	if err != nil || len(auth.GitHubAccounts) != 1 {
		t.Fatalf("Auth() = %+v, %v", auth, err)
	}
	if acc := auth.GitHubAccounts[0]; acc.Name != "default" || !acc.Default || acc.Login != "octocat" {
		t.Errorf("the stored account = %+v", acc)
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if token, _ := creds.GitHubToken("default"); token != "gho_device" {
		t.Errorf("stored token = %q", token)
	}

	// A second account doesn't take the default from the first.
	j, err = d.client.StartGitHubLogin(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitGitHubLogin(t, d, j.ID); got.Status != api.JobSucceeded || got.Account != "work" {
		t.Fatalf("second GitHubLogin() = %+v", got)
	}
	if def, _ := creds.DefaultGitHubAccount(); def != "default" {
		t.Errorf("default after a second sign-in = %q", def)
	}
}

func TestGitHubLoginEndings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ending, outcome string
	}{
		{"expired", api.GitHubLoginExpired},
		{"denied", api.GitHubLoginDenied},
		{"offline", ""},
	} {
		t.Run(tc.ending, func(t *testing.T) {
			t.Parallel()
			d := startGitHubLoginDaemon(t, fakeGH(t, tc.ending))
			ctx := context.Background()
			j, err := d.client.StartGitHubLogin(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			got := waitGitHubLogin(t, d, j.ID)
			if got.Status != api.JobFailed || got.Outcome != tc.outcome || got.Error == "" {
				t.Errorf("GitHubLogin() = %+v, want failed with outcome %q", got, tc.outcome)
			}
			if auth, _ := d.client.Auth(ctx); len(auth.GitHubAccounts) != 0 {
				t.Errorf("a failed sign-in stored %+v", auth.GitHubAccounts)
			}
		})
	}
}

// Cancelling stops gh, and only one sign-in runs at a time.
func TestGitHubLoginCancel(t *testing.T) {
	t.Parallel()
	d := startGitHubLoginDaemon(t, fakeGH(t, "wait"))
	ctx := context.Background()
	j, err := d.client.StartGitHubLogin(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the code", func() bool {
		got, err := d.client.GitHubLogin(ctx, j.ID)
		return err == nil && got.UserCode == "ABCD-1234"
	})
	if _, err := d.client.StartGitHubLogin(ctx, "work"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("a second sign-in while one runs = %v", err)
	}
	if j, err = d.client.CancelJob(ctx, j.ID); err != nil || j.Status != api.JobCancelled {
		t.Fatalf("CancelJob() = %+v, %v", j, err)
	}
	if got, err := d.client.GitHubLogin(ctx, j.ID); err != nil || got.Status != api.JobCancelled || got.UserCode != "ABCD-1234" {
		t.Errorf("GitHubLogin() after cancelling = %+v, %v", got, err)
	}
	// It is over, so another may start.
	if _, err := d.client.StartGitHubLogin(ctx, "work"); err != nil {
		t.Errorf("a sign-in after cancelling = %v", err)
	}
}

func TestGitHubLoginNeedsGH(t *testing.T) {
	t.Parallel()
	d := startGitHubLoginDaemon(t, filepath.Join(t.TempDir(), "gh"))
	j, err := d.client.StartGitHubLogin(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitGitHubLogin(t, d, j.ID); got.Status != api.JobFailed || !strings.Contains(got.Error, "use a token instead") {
		t.Errorf("GitHubLogin() with no gh = %+v", got)
	}
}
