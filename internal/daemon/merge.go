package daemon

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// mergeAgent merges an agent's branch into the branch it started from, in
// the project's repository on this machine (api.MergeAgentRequest): how a
// project with no remote, which has no pull requests, lands an agent's work.
// gitrepo.Merge does the merge; this picks the branch and refuses what
// shouldn't be merged yet.
func (s *Server) mergeAgent(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.MergeAgentRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p, err := s.store.Project(r.Context(), a.Project)
	if err != nil {
		return err
	}
	repo, err := gitrepo.Open(p.Root)
	if err != nil {
		return err
	}
	into, err := mergeTarget(repo, a)
	if err != nil {
		return err
	}
	if _, err := os.Stat(a.Worktree); err == nil {
		if dirty, err := gitrepo.Dirty(a.Worktree); err != nil {
			return err
		} else if dirty {
			return fmt.Errorf("%s has uncommitted changes, which a merge leaves behind: ask it to commit them first", a.Ref())
		}
	}
	m, err := repo.Merge(a.Branch, into, strings.TrimSpace(req.Message))
	if err != nil {
		return err
	}
	s.refreshAgentsBehind()
	return writeJSON(w, http.StatusOK, api.MergeAgentResult{Branch: a.Branch, Into: into, Commit: m.Commit, FastForward: m.FastForward, UpToDate: m.UpToDate})
}

// mergeTarget is the branch an agent's work merges into: its base when that's
// a branch here (a stacked agent's is its parent's), else the branch checked
// out in the project's main checkout, where new agents start.
func mergeTarget(repo gitrepo.Repo, a state.Agent) (string, error) {
	if base := strings.TrimPrefix(a.BaseRef, "refs/heads/"); base != "" && base != "HEAD" && base != a.Branch && repo.BranchExists(base) {
		return base, nil
	}
	if branch := repo.CurrentBranch(); branch != "HEAD" && branch != a.Branch {
		return branch, nil
	}
	return "", fmt.Errorf("%s has no branch checked out to merge %s into: check one out (git switch main)", repo.Root, a.Ref())
}
