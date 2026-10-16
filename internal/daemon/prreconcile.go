package daemon

import (
	"context"
	"fmt"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// When the daemon starts, every project's agents' pull requests are read from
// GitHub once, fresh, before Ready to review lists anything: a pull request
// merged or closed while the daemon was down, or while an older version ran,
// is one the PR watch never saw end — it follows only the ones it saw open —
// and the review list would offer its agent as work waiting for the user.
// Each agent whose pull request ended is freed as a merge from the review
// frees it (freeReviewed), and a project's list is held back (prReconciling)
// until its look is over, however it went.
//
// The same pass finishes destroys the last daemon left halfway, rows still
// marked destroying, which the agent lists leave out.

// holdReview marks every project as not yet reconciled. Run calls it before
// it serves, so no review list goes out ahead of the look.
func (s *Server) holdReview(ctx context.Context) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviewHeld = map[string]bool{}
	for _, p := range projects {
		s.reviewHeld[p.Name] = true
	}
}

// prReconciling reports whether a project's review list is still held back.
func (s *Server) prReconciling(project string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reviewHeld[project]
}

// reconcilePullRequests is the startup pass, one project at a time.
func (s *Server) reconcilePullRequests(ctx context.Context) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		s.logf("reconciling pull requests: %v", err)
	}
	for _, p := range projects {
		if ctx.Err() != nil {
			return
		}
		s.reconcileProject(ctx, p)
		s.mu.Lock()
		delete(s.reviewHeld, p.Name)
		s.mu.Unlock()
		s.events.publish(api.EventReview, p.Name)
	}
	s.mu.Lock()
	s.reviewHeld = nil
	s.mu.Unlock()
}

// reconcileProject is one project's look: unfinished destroys finished, then
// GitHub asked about every agent's pull request.
func (s *Server) reconcileProject(ctx context.Context, p state.Project) {
	agents, err := s.store.Agents(ctx, p.Name)
	if err != nil {
		s.logf("reconciling pull requests in %s: %v", p.Name, err)
		return
	}
	var workers []state.Agent
	for _, a := range agents {
		switch {
		case a.Status == state.AgentDestroying:
			s.logf("%s: finishing the destroy the last daemon began", a.Ref())
			if err := s.destroyAgentNow(ctx, s.manager(s.cfg.Log), a, agent.DestroyOptions{Force: true}); err != nil {
				s.logf("%s: %v", a.Ref(), err)
			}
		case !a.IsLead() && a.Status == state.AgentReady && a.Branch != "":
			workers = append(workers, a)
		}
	}
	if len(workers) == 0 {
		return
	}
	client, repo, err := s.githubFor(p)
	if err != nil {
		return // no GitHub to ask
	}
	heads := agentHeads(p.Root, workers)
	key := repo.String()
	s.pulls.readFor(key, p.Name)
	before, _ := s.pulls.state(key)
	fetch, cancel := context.WithTimeout(ctx, pullsFetchTimeout)
	entry := s.fetchPulls(fetch, client, repo, heads, before)
	cancel()
	if entry.listErr.failed() {
		s.logf("reconciling pull requests in %s: %s", p.Name, entry.listErr.message)
		return
	}
	// What was read is the cache's too, so the lists read next agree with it.
	s.pulls.invalidate(key)
	if gen, ok := s.pulls.claim(key, heads); ok {
		s.pulls.finish(key, gen, entry)
	}
	prs := entry.byAgent(heads)
	for _, a := range workers {
		if pr := prs[a.Name]; prEnded(pr, a) {
			s.freeReviewed(ctx, a, fmt.Sprintf("its pull request #%d was %s while AgentBox wasn't watching", pr.Number, pr.State), pr)
		}
	}
}

// prEnded reports whether pr, a's pull request, was merged or closed. One
// last touched before a existed isn't a's, whatever it carries (removeReason).
func prEnded(pr *api.PullRequest, a state.Agent) bool {
	if pr == nil || (pr.State != "merged" && pr.State != "closed") {
		return false
	}
	return pr.UpdatedAt == nil || !pr.UpdatedAt.Before(a.CreatedAt)
}
