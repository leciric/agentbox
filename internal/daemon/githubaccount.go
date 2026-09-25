package daemon

import (
	"errors"
	"fmt"

	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/state"
)

// Which GitHub account a project's pull requests are read with, and what to
// say when reading them fails. Pull requests are read with the project's
// account, or the machine's default one — never with the host's own `gh` —
// so a repository that is there for one account is a 404 for another. Saying
// which account was used, and who that is on GitHub, is the difference
// between "pull requests don't work" and "this account isn't in that
// repository".

// pullsErr is a failure to read GitHub as the cache keeps it: its text, and
// what kind of failure it was. The kind is worked out once, where the error
// itself still is, because an answer is served from the cache long after the
// error that produced it is gone. It is comparable, so two answers holding
// the same failure still compare equal (D54).
type pullsErr struct {
	message string
	kind    string // one of the api.GitHub… kinds; empty when nothing failed
}

func newPullsErr(err error) pullsErr {
	if err == nil {
		return pullsErr{}
	}
	return pullsErr{message: err.Error(), kind: githubErrKind(err)}
}

// failed reports whether this is a failure at all.
func (e pullsErr) failed() bool { return e.kind != "" }

// githubErrKind is what kind of failure an error from GitHub is, in the terms
// the app says a sentence in.
func githubErrKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errNoGitHubToken):
		return api.GitHubNoAccount
	case errors.Is(err, github.ErrNoAccess):
		return api.GitHubNoAccess
	case errors.Is(err, github.ErrBadToken):
		return api.GitHubBadToken
	default:
		return api.GitHubOtherErr
	}
}

// githubAccountFor is the stored account a project's pull requests are read
// with, and who that account is on GitHub. Both are empty when nothing
// resolves: no account is stored, or the project points at one that was
// removed. The login is the one remembered when the token was saved, so this
// costs no GitHub call.
func (s *Server) githubAccountFor(p state.Project) (account, login string) {
	m := s.manager(nil)
	account, err := m.GitHubAccountFor(p, "")
	if err != nil || account == "" {
		return "", ""
	}
	login, _ = m.Creds.GitHubLogin(account)
	return account, login
}

// remoteProblemOf is why a project has no GitHub repository to read, when
// that is why: a checkout with no origin remote at all, or an origin that
// doesn't resolve to one, named with any credentials redacted. Neither is a
// GitHubError — nothing was read, and no account is at fault — but they are
// different problems with different fixes, and one message for both is what
// cost a user a long debugging session
// (D57).
func remoteProblemOf(err error) (noOrigin bool, nonGitHubRemote string) {
	var notGitHub *github.NotGitHubError
	switch {
	case errors.Is(err, github.ErrNoOrigin):
		return true, ""
	case errors.As(err, &notGitHub):
		return false, notGitHub.Remote
	default:
		return false, ""
	}
}

// githubErrorFor names a failure to read GitHub well enough for the app to
// say a sentence with a fix in it. A project with no GitHub remote is not a
// failure — there is nothing to read, which remoteProblemOf describes instead
// — and neither is no error at all: both answer nil.
func (s *Server) githubErrorFor(p state.Project, repo github.Repo, err error) *api.GitHubError {
	if err == nil || errors.Is(err, github.ErrNoRepo) {
		return nil
	}
	return s.githubErrorFrom(p, repo, newPullsErr(err))
}

// githubErrorFrom is the same, for a failure that has been through the cache
// and is a kind and a message rather than an error.
func (s *Server) githubErrorFrom(p state.Project, repo github.Repo, failure pullsErr) *api.GitHubError {
	if !failure.failed() {
		return nil
	}
	account, login := s.githubAccountFor(p)
	out := &api.GitHubError{Kind: failure.kind, Message: failure.message, Account: account, Login: login}
	if repo.Owner != "" {
		out.Repo = repo.String()
	}
	if out.Kind != api.GitHubNoAccount {
		return out
	}
	if p.GitHubAccount != "" && account == "" {
		// The project picked an account that has since been removed, which is
		// worth saying instead of "no account": nothing else would tell the
		// user their choice is gone.
		out.Account = p.GitHubAccount
		out.Message = fmt.Sprintf("this project's GitHub account %q isn't stored any more", p.GitHubAccount)
		return out
	}
	// Whatever resolved has no token to read with, so naming it would point at
	// an account that isn't the problem. The two cases are the two sentences
	// the app says, so only one of them carries an account.
	out.Account = ""
	out.Message = "no GitHub account is stored"
	return out
}
