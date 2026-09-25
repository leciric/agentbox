package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
)

// fakeClaude stands in for `claude setup-token`. It does the three things the
// daemon's side of the login depends on: it refuses to run without a terminal,
// it opens the sign-in page through $BROWSER and prints the fallback one, and
// it prints a token once a code has been typed at it (D59).
const fakeClaude = `#!/bin/sh
test "$1" = setup-token || { echo "unexpected: $*" >&2; exit 2; }
test -t 1 || { echo "stdout is not a terminal" >&2; exit 3; }
test -n "$CLAUDE_CODE_OAUTH_TOKEN" && { echo "the daemon's own token leaked in" >&2; exit 4; }
test "$HOME" = "$AGENTBOX_TEST_HOME" && { echo "ran in the caller's HOME" >&2; exit 5; }
echo "$HOME" > "$AGENTBOX_TEST_OUT/home"
"$BROWSER" 'https://claude.com/cai/oauth/authorize?code=true&redirect_uri=http%3A%2F%2Flocalhost%3A45001%2Fcallback'
echo "Browser didn't open? Use the url below to sign in"
echo 'https://claude.com/cai/oauth/authorize?code=true&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback'
read code
echo "Your OAuth token (valid for 1 year):"
echo "sk-ant-oat01-${code}-KQhTdXAgZnJvbSB0aGUgcHR5IQAAAAAAAAAA"
echo "Store this token securely. You won't be able to see it again."
sleep 5
`

const browserURL = "https://claude.com/cai/oauth/authorize?code=true&redirect_uri=http%3A%2F%2Flocalhost%3A45001%2Fcallback"

// TestClaudeLoginAPI drives a login the way the Setup page does: start it, take
// the page to approve, hand back the code from the fallback page, and find the
// token stored under the account. Nothing here talks to Anthropic.
func TestClaudeLoginAPI(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	out := t.TempDir()
	// AgentBox's own copy of Claude Code, which the login prefers to the PATH.
	claude := filepath.Join(d.paths.Tools(), ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(strings.NewReplacer(
		"$AGENTBOX_TEST_OUT", out,
		"$AGENTBOX_TEST_HOME", os.Getenv("HOME"),
	).Replace(fakeClaude)), 0o755); err != nil {
		t.Fatal(err)
	}

	job, err := d.client.StartClaudeLogin(ctx, api.ClaudeLoginRequest{Account: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != "claude-login" || job.Target != "work" {
		t.Errorf("StartClaudeLogin() = %+v", job)
	}

	// A second login would share this machine's browser and the first one's
	// prompt, so it is refused rather than left to confuse it.
	if _, err := d.client.StartClaudeLogin(ctx, api.ClaudeLoginRequest{}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("a second login while one runs = %v, want it refused", err)
	}

	var login api.ClaudeLogin
	waitFor(t, "the page to approve", func() bool {
		login, _ = d.client.ClaudeLogin(ctx, job.ID)
		return login.URL != "" && login.CodeURL != ""
	})
	if login.Account != "work" || login.Status != api.JobRunning {
		t.Errorf("ClaudeLogin() = %+v", login)
	}
	// The page to open is the one Claude Code asked the browser for, which
	// comes back to this machine; the printed one is the code fallback.
	if login.URL != browserURL {
		t.Errorf("URL = %q, want %q", login.URL, browserURL)
	}
	if !strings.Contains(login.CodeURL, "platform.claude.com") {
		t.Errorf("CodeURL = %q, want the page that ends in a code", login.CodeURL)
	}

	if err := d.client.ClaudeLoginCode(ctx, job.ID, "  approved  "); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the login to finish", func() bool {
		login, _ = d.client.ClaudeLogin(ctx, job.ID)
		return login.Status != api.JobRunning
	})
	if login.Status != api.JobSucceeded {
		t.Fatalf("ClaudeLogin() = %+v", login)
	}

	creds := credentials.Store{Dir: d.paths.Credentials()}
	token, err := creds.ClaudeToken("work")
	if err != nil {
		t.Fatal(err)
	}
	if want := "sk-ant-oat01-approved-KQhTdXAgZnJvbSB0aGUgcHR5IQAAAAAAAAAA"; token != want {
		t.Errorf("stored token = %q, want %q", token, want)
	}
	auth, err := d.client.Auth(ctx)
	if err != nil || len(auth.ClaudeAccounts) != 1 || auth.ClaudeAccounts[0].Name != "work" {
		t.Errorf("Auth() = %+v, %v", auth, err)
	}

	// The HOME it ran in was its own, below AgentBox's tools directory, and it
	// is gone: nothing a login writes outlives it (D6).
	home, err := os.ReadFile(filepath.Join(out, "home"))
	if err != nil {
		t.Fatal(err)
	}
	loginHome := strings.TrimSpace(string(home))
	if !strings.HasPrefix(loginHome, d.paths.Tools()+string(filepath.Separator)) {
		t.Errorf("the login ran with HOME=%q, want it under %q", loginHome, d.paths.Tools())
	}
	if _, err := os.Stat(loginHome); !os.IsNotExist(err) {
		t.Errorf("the login's HOME %q is still there: %v", loginHome, err)
	}

	// A finished login takes no more codes, and doesn't block the next one.
	if err := d.client.ClaudeLoginCode(ctx, job.ID, "again"); err == nil {
		t.Error("a finished login took a code")
	}
}

// TestClaudeLoginUnknownJob checks the two ways of asking about a login that
// isn't there, so neither answers as though it were.
func TestClaudeLoginUnknownJob(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.ClaudeLogin(ctx, "nope"); err == nil {
		t.Error("ClaudeLogin() of an unknown job succeeded")
	}
	if err := d.client.ClaudeLoginCode(ctx, "nope", "code"); err == nil {
		t.Error("ClaudeLoginCode() of an unknown job succeeded")
	}
}
