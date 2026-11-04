package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/github"
)

// githubLogin is one GitHub sign-in through gh: the code to show while it
// waits, then whose the token turned out to be.
type githubLogin struct {
	account string

	mu      sync.Mutex
	code    github.LoginCode
	user    string
	outcome string
}

func (l *githubLogin) snapshot() (github.LoginCode, string, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.code, l.user, l.outcome
}

// startGitHubLogin signs in to GitHub with `gh auth login --web`, run where
// the daemon runs (github.GHLogin), so adding an account needs no pasted
// token, nothing on the user's own machine and no OAuth App of AgentBox's:
// the app shows gh's code, the user enters it at github.com/login/device, and
// the token is stored as a pasted one is. It waits for the user, so it is a
// job, which cancelling stops gh.
func (s *Server) startGitHubLogin(w http.ResponseWriter, r *http.Request) error {
	var req api.GitHubLoginRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	account := strings.TrimSpace(req.Account)
	if account == "" {
		account = credentials.DefaultAccount
	}
	if err := credentials.ValidateAccount(account); err != nil {
		return err
	}
	job, err := s.claimGitHubLogin(&githubLogin{account: account})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, job)
}

// claimGitHubLogin starts the sign-in's job. Only one runs at a time: two
// codes on the screen, for two accounts, would be easy to enter into the
// wrong browser. Finished sign-ins are forgotten here, as claimClaudeLogin
// does.
func (s *Server) claimGitHubLogin(login *githubLogin) (api.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.githubLogins {
		if j, ok := s.jobs.get(id); ok && !j.snapshot().Done() {
			return api.Job{}, fmt.Errorf("a GitHub sign-in is already running (job %s): finish it, or cancel it, before starting another", id)
		}
		delete(s.githubLogins, id)
	}
	registered := make(chan struct{})
	j, err := s.jobs.start("github-login", login.account, func(ctx context.Context, log io.Writer) (any, error) {
		<-registered
		return s.runGitHubLogin(ctx, log, login)
	})
	if err != nil {
		return api.Job{}, err
	}
	s.githubLogins[j.snapshot().ID] = login
	close(registered)
	return j.snapshot(), nil
}

func (s *Server) runGitHubLogin(ctx context.Context, log io.Writer, login *githubLogin) (any, error) {
	status := func(detail string) { _, _ = fmt.Fprintf(log, "==> %s\n", detail) }
	status("Signing in to GitHub as the account " + login.account)
	if err := s.ensureGH(ctx, log); err != nil {
		return nil, err
	}
	token, err := s.githubGH.Run(ctx, log, func(code github.LoginCode) {
		login.mu.Lock()
		login.code = code
		login.mu.Unlock()
		status(fmt.Sprintf("Enter the code %s at %s", code.UserCode, code.VerificationURI))
	})
	if err != nil {
		outcome := ""
		switch {
		case errors.Is(err, github.ErrLoginExpired):
			outcome = api.GitHubLoginExpired
		case errors.Is(err, github.ErrLoginDenied):
			outcome = api.GitHubLoginDenied
		}
		login.mu.Lock()
		login.outcome = outcome
		login.mu.Unlock()
		return nil, err
	}
	status("Saving the token for agents")
	user, err := s.storeGitHubToken(ctx, login.account, token)
	if err != nil {
		return nil, err
	}
	login.mu.Lock()
	login.user = user
	login.mu.Unlock()
	status("Signed in as " + user)
	return map[string]string{"account": login.account, "user": user}, nil
}

// ensureGH installs gh when it is missing from AgentBox's own VM. A VM made
// before gh was among its packages (chv's packages, Lima's provisioning, the
// WSL distro's configure.sh) has none, and its user has passwordless sudo
// there; elsewhere, on a machine of the user's own, nothing is installed and
// the sign-in says gh is missing.
func (s *Server) ensureGH(ctx context.Context, log io.Writer) error {
	if _, err := s.githubGH.Path(); err == nil || !s.installGH {
		return err
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return github.ErrNoGH
	}
	_, _ = fmt.Fprintln(log, "==> Installing GitHub CLI (gh) in AgentBox's VM")
	for _, args := range [][]string{
		{"-n", "apt-get", "update", "-q"},
		{"-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "-q", "--no-install-recommends", "gh"},
	} {
		cmd := exec.CommandContext(ctx, "sudo", args...)
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("installing gh: %w (%w)", err, github.ErrNoGH)
		}
	}
	_, err := s.githubGH.Path()
	return err
}

// githubLoginStatus is what the app and the CLI poll while a sign-in runs.
func (s *Server) githubLoginStatus(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("job")
	info, _, err := s.jobs.lookup(r.Context(), id)
	if err != nil {
		return err
	}
	if info.Kind != "github-login" {
		return fmt.Errorf("job %s isn't a GitHub sign-in", id)
	}
	out := api.GitHubLogin{Job: info.ID, Account: info.Target, Status: info.Status, Error: info.Error}
	s.mu.Lock()
	login := s.githubLogins[id]
	s.mu.Unlock()
	if login != nil {
		code, user, outcome := login.snapshot()
		out.UserCode, out.VerificationURI, out.User, out.Outcome = code.UserCode, code.VerificationURI, user, outcome
		if !code.ExpiresAt.IsZero() {
			out.ExpiresAt = &code.ExpiresAt
		}
	}
	return writeJSON(w, http.StatusOK, out)
}
