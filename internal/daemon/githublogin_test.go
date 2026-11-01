package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/github"
)

// fakeGitHubWeb is github.com's device flow and api.github.com's /user, in
// one stub: each poll is answered with the next of polls, and the last one
// again after that.
type fakeGitHubWeb struct {
	mu    sync.Mutex
	polls []string
	asked int
}

func (f *fakeGitHubWeb) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/login/device/code":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dev-1", "user_code": "ABCD-1234", "verification_uri": "https://github.com/login/device",
			"expires_in": 900, "interval": 5,
		})
	case "/login/oauth/access_token":
		answer := f.polls[min(f.asked, len(f.polls)-1)]
		f.asked++
		if answer == "token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "gho_device", "token_type": "bearer"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"error": answer})
	case "/user":
		if r.Header.Get("Authorization") != "Bearer gho_device" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	default:
		http.NotFound(w, r)
	}
}

// startGitHubLoginDaemon is a daemon whose device flow goes to fake, polling
// as soon as wait lets it.
func startGitHubLoginDaemon(t *testing.T, fake *fakeGitHubWeb, wait func(context.Context, time.Duration) error) testDaemon {
	t.Helper()
	stub := httptest.NewServer(fake)
	t.Cleanup(stub.Close)
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{setup: func(s *Server) {
		s.githubDevice = github.DeviceFlow{ClientID: "Iv1.test", BaseURL: stub.URL, Wait: wait}
	}})
	d.setGitHub(t, stub.URL)
	return d
}

func noWait(context.Context, time.Duration) error { return nil }

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
	fake := &fakeGitHubWeb{polls: []string{"authorization_pending", "slow_down", "token"}}
	d := startGitHubLoginDaemon(t, fake, noWait)
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
		answer, outcome string
	}{
		{"expired_token", api.GitHubLoginExpired},
		{"access_denied", api.GitHubLoginDenied},
		{"unsupported_grant_type", ""},
	} {
		t.Run(tc.answer, func(t *testing.T) {
			t.Parallel()
			d := startGitHubLoginDaemon(t, &fakeGitHubWeb{polls: []string{"authorization_pending", tc.answer}}, noWait)
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

// Cancelling stops the polling, and only one sign-in runs at a time.
func TestGitHubLoginCancel(t *testing.T) {
	t.Parallel()
	polling := make(chan struct{}, 1)
	d := startGitHubLoginDaemon(t, &fakeGitHubWeb{polls: []string{"authorization_pending"}}, func(ctx context.Context, _ time.Duration) error {
		select {
		case polling <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	})
	ctx := context.Background()
	j, err := d.client.StartGitHubLogin(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	<-polling
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

func TestGitHubLoginNeedsAClientID(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{setup: func(s *Server) { s.githubDevice = github.DeviceFlow{} }})
	if _, err := d.client.StartGitHubLogin(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "AGENTBOX_GITHUB_CLIENT_ID") {
		t.Errorf("StartGitHubLogin() with no client ID = %v", err)
	}
}
