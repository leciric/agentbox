package daemon

import (
	"errors"
	"net/http"
	"time"

	"agentbox/internal/api"
)

// errNoGitHubAccount is the first run asking for repositories before any
// GitHub account was added.
var errNoGitHubAccount = errors.New("no GitHub account yet: sign in to GitHub first")

// githubRepos lists the repositories the default GitHub account can push to,
// most recently pushed first, for the first run's first project.
func (s *Server) githubRepos(w http.ResponseWriter, r *http.Request) error {
	creds := s.manager(nil).Creds
	account, err := creds.DefaultGitHubAccount()
	if err != nil {
		return err
	}
	if account == "" {
		return errNoGitHubAccount
	}
	token, err := creds.GitHubToken(account)
	if err != nil {
		return err
	}
	if token == "" {
		return errNoGitHubAccount
	}
	repos, err := s.gitHub(token).UserRepos(r.Context())
	if err != nil {
		return err
	}
	out := make([]api.GitHubRepo, 0, len(repos))
	for _, repo := range repos {
		one := api.GitHubRepo{FullName: repo.FullName, Description: repo.Description, Private: repo.Private, CloneURL: repo.CloneURL}
		if !repo.PushedAt.IsZero() {
			one.PushedAt = repo.PushedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, one)
	}
	return writeJSON(w, http.StatusOK, out)
}
