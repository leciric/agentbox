package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/github"
)

// githubLogin is one device-flow GitHub sign-in: the code to show while it
// waits, then whose the token turned out to be.
type githubLogin struct {
	account string

	mu      sync.Mutex
	code    github.DeviceCode
	user    string
	outcome string
}

func (l *githubLogin) snapshot() (github.DeviceCode, string, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.code, l.user, l.outcome
}

// startGitHubLogin signs in to GitHub with OAuth's device flow, so adding an
// account needs neither gh on this machine nor a pasted token: the app shows
// a code, the user enters it at github.com/login/device, and the token is
// stored as a pasted one is. It waits for the user, so it is a job, which
// cancelling stops polling.
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
	if s.githubDevice.ClientID == "" {
		return github.ErrNoClientID
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
	code, err := s.githubDevice.Start(ctx, github.DeviceScopes)
	if err != nil {
		return nil, err
	}
	login.mu.Lock()
	login.code = code
	login.mu.Unlock()
	status(fmt.Sprintf("Enter the code %s at %s (it expires at %s)", code.UserCode, code.VerificationURI, code.ExpiresAt.Local().Format(time.Kitchen)))
	token, err := s.githubDevice.Poll(ctx, code)
	if err != nil {
		outcome := ""
		switch {
		case errors.Is(err, github.ErrDeviceExpired):
			outcome = api.GitHubLoginExpired
		case errors.Is(err, github.ErrDeviceDenied):
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
