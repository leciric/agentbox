package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// Finished agents are removed on their own. An agent is one task, and its
// deliverable is its branch: once the branch's pull request is merged or
// closed, or the agent finished having changed nothing, the machine and the
// worktree are only disk and a row in the rail. What decides it is written
// out in removeReason; the one rule over all of them is that work that exists
// only in the agent — uncommitted, or committed and never pushed — is never
// destroyed by anything but a person.

// cleanupInterval is how often the daemon looks for agents to remove. A pull
// request merged a few minutes ago is no hurry, and each pass lists every
// project's machines.
const cleanupInterval = 5 * time.Minute

// cleanupDelay holds the first pass back after the daemon starts, so the
// pull request cache has been asked once before anything is judged by it.
const cleanupDelay = time.Minute

// finishedGrace is how long an agent that finished with no changes and no
// pull request is kept, in case somebody still wants to ask it something.
const finishedGrace = 24 * time.Hour

// sweepFinishedAgents periodically removes the agents removeReason says are
// done with.
func (s *Server) sweepFinishedAgents(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(cleanupDelay):
	}
	s.removeFinishedAgents(ctx, time.Now())
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.removeFinishedAgents(ctx, time.Now())
		}
	}
}

// cleanupFacts is what removeReason decides from, gathered by
// removeFinishedAgents so that the rules themselves are a plain function.
type cleanupFacts struct {
	// Busy is a chat that is running, starting or waiting on somebody.
	Busy bool
	// Attended is an agent whose activity AgentBox can't see: one worked on
	// its own command line, with its machine running.
	Attended bool
	Dirty    bool
	// Unpushed is a branch with commits that are neither merged, nor on a
	// remote, nor in its pull request's head.
	Unpushed bool
	// Changed is a diff against the commit the agent started from.
	Changed   bool
	PR        *api.PullRequest
	CreatedAt time.Time
	// FinishedAt is when it last genuinely finished a task; nil if it never
	// has. LastActive is its conversation's last movement.
	FinishedAt, LastActive *time.Time
}

// removeReason says why an agent should be removed now, or "" to keep it.
func removeReason(f cleanupFacts, now time.Time) string {
	if f.Busy || f.Attended || f.Dirty || f.Unpushed {
		return ""
	}
	if pr := f.PR; pr != nil {
		// A pull request last touched before this agent existed is an older
		// one on a branch name used again, and says nothing about this agent.
		if pr.UpdatedAt != nil && pr.UpdatedAt.Before(f.CreatedAt) {
			return ""
		}
		switch pr.State {
		case "merged", "closed":
			return fmt.Sprintf("its pull request #%d was %s", pr.Number, pr.State)
		}
		return ""
	}
	if f.Changed || f.FinishedAt == nil {
		return ""
	}
	since := *f.FinishedAt
	if f.LastActive != nil && f.LastActive.After(since) {
		since = *f.LastActive
	}
	if now.Sub(since) < finishedGrace {
		return ""
	}
	return "it finished with no changes and no pull request more than a day ago"
}

// removeFinishedAgents does one pass over every project, as of now.
func (s *Server) removeFinishedAgents(ctx context.Context, now time.Time) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		s.logf("remove finished agents: %v", err)
		return
	}
	removed := 0
	for _, p := range projects {
		removed += s.removeFinishedIn(ctx, p, now)
	}
	if removed > 0 {
		s.refreshAgents(ctx)
	}
}

func (s *Server) removeFinishedIn(ctx context.Context, p state.Project, now time.Time) int {
	m := s.manager(s.cfg.Log)
	statuses, err := m.List(ctx, p.Name)
	if err != nil {
		s.logf("remove finished agents in %s: %v", p.Name, err)
		return 0
	}
	var candidates []agent.Status
	var branches []string
	for _, st := range statuses {
		if st.IsLead() || st.Status != state.AgentReady || st.Branch == "" {
			continue
		}
		candidates = append(candidates, st)
		branches = append(branches, st.Branch)
	}
	if len(candidates) == 0 {
		return 0
	}
	// From the cache, which refreshes itself behind this: a pull request
	// merged since the last refresh is caught by the next pass.
	var prs map[string]*api.PullRequest
	if _, entry, _, err := s.projectPulls(p, branches); err == nil && !entry.listErr.failed() {
		prs = entry.byBranch(branches)
	}
	repo, err := gitrepo.Open(p.Root)
	if err != nil {
		return 0
	}
	finished := s.lastFinished(ctx, p.Name)

	removed := 0
	for _, st := range candidates {
		changes := changesOf(st.Agent)
		busy, _, last, _ := s.idleOf(ctx, st, changes)
		f := cleanupFacts{
			Busy:       busy,
			Attended:   st.Interface == state.InterfaceCLI && st.State == "running",
			Dirty:      changes.Dirty,
			Changed:    changes.Files > 0,
			PR:         prs[st.Branch],
			CreatedAt:  st.CreatedAt,
			LastActive: last,
		}
		if at, ok := finished[st.Name]; ok {
			f.FinishedAt = &at
		}
		f.Unpushed = unpushed(repo, st.Agent, f.PR)
		reason := removeReason(f, now)
		if reason == "" {
			continue
		}
		s.logf("removing %s: %s", st.Ref(), reason)
		if err := s.destroyAgentNow(ctx, m, st.Agent, agent.DestroyOptions{}); err != nil {
			s.logf("removing %s: %v", st.Ref(), err)
			continue
		}
		s.captureEvent(ctx, st.Project, st.Name, "agent_retired", map[string]any{"how": "auto", "branch": st.Branch, "reason": reason}, "")
		removed++
	}
	return removed
}

// unpushed reports whether an agent's branch has commits that exist only in
// this repository: not merged into the project's branch, not on a remote,
// and not in the head of its pull request as GitHub last saw it.
func unpushed(repo gitrepo.Repo, a state.Agent, pr *api.PullRequest) bool {
	tip, err := repo.ResolveCommit("refs/heads/" + a.Branch)
	if err != nil {
		return false // no branch left, so nothing on it to lose
	}
	if repo.Pushed(tip) || agent.BranchDisposable(repo, a) {
		return false
	}
	return pr == nil || pr.HeadSHA == "" || !repo.IsAncestor(tip, pr.HeadSHA)
}

// lastFinished is when each of a project's agents last genuinely finished,
// from the events the rail's threads are made of.
func (s *Server) lastFinished(ctx context.Context, project string) map[string]time.Time {
	out := map[string]time.Time{}
	events, err := s.store.AgentEvents(ctx, project)
	if err != nil {
		return out
	}
	for _, row := range events { // newest first
		if _, seen := out[row.Agent]; seen {
			continue
		}
		var ev api.AgentEvent
		if json.Unmarshal(row.Data, &ev) == nil && ev.Kind == api.AgentFinished {
			out[row.Agent] = row.CreatedAt
		}
	}
	return out
}

// destroyAgentNow stops what the daemon runs for an agent and destroys it,
// the one sequence every destroy goes through — by hand, retired, or removed
// here. The caller records why.
func (s *Server) destroyAgentNow(ctx context.Context, m *agent.Manager, a state.Agent, opts agent.DestroyOptions) error {
	if err := m.Destroy(ctx, a, opts); err != nil {
		return err
	}
	s.chat.Stop(a.Ref(), "the agent was destroyed")
	s.removeActiveAgent(ctx, a.Project, a.Name)
	s.chat.Forget(a.Ref())
	s.stopAgentAPI(a.Instance)
	s.removeBrowserSockets(a.Instance)
	return nil
}
