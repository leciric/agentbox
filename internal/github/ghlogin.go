package github

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// LoginScopes are what agents' gh needs to do what it does on a developer's
// own machine: gh's own defaults (repo, read:org, gist) and workflow, which
// pushing a change to .github/workflows takes.
var LoginScopes = []string{"repo", "read:org", "workflow", "gist"}

// What ends a sign-in without a token, so a caller can offer the right next
// step: a new code for an expired one, a token for a refusal.
var (
	// ErrLoginExpired is the code timing out before anyone entered it.
	ErrLoginExpired = errors.New("the code expired before it was entered on GitHub: start again for a new one")
	// ErrLoginDenied is the user pressing Cancel on GitHub's page.
	ErrLoginDenied = errors.New("the sign-in was denied on GitHub")
	// ErrNoGH is a machine with no gh to sign in with.
	ErrNoGH = errors.New("gh isn't installed where AgentBox runs: install GitHub CLI there, or use a token instead")
)

// loginHost is the only host a sign-in is for: tokens for GitHub Enterprise
// are pasted.
const loginHost = "github.com"

// codeLifetime is how long GitHub's device codes last (expires_in, 900
// seconds); gh doesn't print it.
const codeLifetime = 15 * time.Minute

// GHLogin signs in to GitHub with `gh auth login --web`: gh prints a one-time
// code, the user types it in at github.com/login/device in any browser, and
// gh receives the token. The code is GitHub CLI's own OAuth App's, so there is
// no app of AgentBox's to set up, and nothing listens on a port, so it works
// from inside a VM. gh keeps the token in a config directory of its own,
// which is removed once the token is read from it: the gh a user may have
// signed in to themselves is never touched.
type GHLogin struct {
	// Bin is gh; empty is gh on PATH.
	Bin string
}

// LoginCode is what the user needs to finish a sign-in.
type LoginCode struct {
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
}

var (
	codeLine = regexp.MustCompile(`one-time code: ([A-Z0-9]{4}-[A-Z0-9]{4})`)
	urlLine  = regexp.MustCompile(`(https://\S+/login/device)\b`)
)

// Path is the gh this signs in with, or ErrNoGH.
func (g GHLogin) Path() (string, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return "", ErrNoGH
	}
	return p, nil
}

// Run signs in and returns the token. It calls code once gh has printed the
// one-time code and the page to enter it on, and then waits for the user,
// as long as gh does. It ends with ErrLoginExpired or ErrLoginDenied when
// the user doesn't finish, and with ctx's error, gh stopped, when it is
// cancelled. gh's output goes to log as it comes.
func (g GHLogin) Run(ctx context.Context, log io.Writer, code func(LoginCode)) (string, error) {
	gh, err := g.Path()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "agentbox-gh-login-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env := loginEnv(dir)

	login := exec.CommandContext(ctx, gh, "auth", "login", "--hostname", loginHost,
		"--git-protocol", "https", "--web", "--insecure-storage",
		"--scopes", strings.Join(LoginScopes, ","))
	login.Env = env
	login.Dir = dir
	// gh says what it's doing on stderr, and the code with it; both go
	// through one pipe so the lines stay in order.
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	login.Stdout, login.Stderr = w, w
	if err := login.Start(); err != nil {
		_ = r.Close()
		_ = w.Close()
		return "", fmt.Errorf("starting gh: %w", err)
	}
	_ = w.Close()
	var (
		out  strings.Builder
		wg   sync.WaitGroup
		seen LoginCode
	)
	wg.Go(func() {
		defer func() { _ = r.Close() }()
		lines := bufio.NewScanner(r)
		for lines.Scan() {
			line := lines.Text()
			if log != nil {
				_, _ = fmt.Fprintln(log, line)
			}
			out.WriteString(line + "\n")
			if m := codeLine.FindStringSubmatch(line); m != nil && seen.UserCode == "" {
				seen.UserCode = m[1]
			}
			if m := urlLine.FindStringSubmatch(line); m != nil && seen.VerificationURI == "" {
				seen.VerificationURI = m[1]
			}
			if seen.UserCode != "" && seen.VerificationURI != "" && seen.ExpiresAt.IsZero() {
				seen.ExpiresAt = time.Now().Add(codeLifetime)
				if code != nil {
					code(seen)
				}
			}
		}
	})
	waitErr := login.Wait()
	wg.Wait()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if waitErr != nil {
		return "", loginError(out.String(), waitErr)
	}

	tokenCmd := exec.CommandContext(ctx, gh, "auth", "token", "--hostname", loginHost)
	tokenCmd.Env = env
	tokenCmd.Dir = dir
	var stderr strings.Builder
	tokenCmd.Stderr = &stderr
	token, err := tokenCmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh signed in, but gh auth token failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	t := strings.TrimSpace(string(token))
	if t == "" {
		return "", errors.New("gh signed in, but gave no token")
	}
	return t, nil
}

// loginEnv is the daemon's environment for gh, with the config directory
// dir and none of what would make gh use another account: a GH_TOKEN, as the
// daemon's own may have, makes `gh auth login` refuse. Git's global config
// goes into dir too, so nothing gh does reaches the user's ~/.gitconfig.
func loginEnv(dir string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
			"GH_HOST", "GH_CONFIG_DIR", "GIT_CONFIG_GLOBAL", "GH_FORCE_TTY":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GH_CONFIG_DIR="+dir, "GIT_CONFIG_GLOBAL="+dir+"/gitconfig",
		"GH_NO_UPDATE_NOTIFIER=1", "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
}

// loginError is how gh's sign-in failed, from what it printed: gh exits 1
// whatever went wrong.
func loginError(out string, err error) error {
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "access_denied") || strings.Contains(lower, "denied"):
		return ErrLoginDenied
	case strings.Contains(lower, "timed out") || strings.Contains(lower, "expired"):
		return ErrLoginExpired
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return fmt.Errorf("gh auth login failed: %s", last)
	}
	return fmt.Errorf("gh auth login failed: %w", err)
}
