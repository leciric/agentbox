package agent

import (
	"context"
	"strings"

	"agentbox/internal/gitrepo"
)

// syncBase brings the project's base branch up to date with its remote
// before an agent is made from it (gitrepo.Repo.SyncBase), and says in the
// log what it did. It is best effort: an agent is still made from what the
// repository has when the remote can't be reached.
func (m *Manager) syncBase(ctx context.Context, repo gitrepo.Repo) {
	synced, err := repo.SyncBase(ctx)
	switch {
	case err != nil:
		m.logf("Couldn't bring %s up to date with %s: %v", synced.Branch, shortRef(synced.Upstream.Ref), err)
	case synced.Moved:
		m.logf("Fast-forwarded %s to %s", synced.Branch, shortRef(synced.Upstream.Ref))
	}
}

// newerBase is where an agent made from the project's base branch starts
// when that branch is behind its remote — a user's main that hasn't been
// pulled, and couldn't be fast-forwarded — rather than from a main that is
// missing what was merged since: the remote's copy, as last fetched, when it
// is strictly ahead. The local branch stays where it is. ok is false for any
// other starting point, and for a base branch with commits of its own.
func newerBase(repo gitrepo.Repo, from string) (string, bool) {
	if from == "" || from == "HEAD" || !repo.BranchExists(from) {
		return "", false
	}
	if base, _, ok := repo.BaseBranch(); from != repo.CurrentBranch() && (!ok || from != base) {
		return "", false
	}
	up, ok := repo.UpstreamOf(from)
	if !ok {
		return "", false
	}
	return repo.Ahead(from, up)
}

// shortRef is a remote-tracking ref as git names it to a person: origin/main.
func shortRef(ref string) string {
	return strings.TrimPrefix(ref, "refs/remotes/")
}
