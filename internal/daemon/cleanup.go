package daemon

import (
	"cmp"
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
		// A pull request last touched before this agent existed can't be one
		// this agent made, whatever it carries.
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
	var agents []state.Agent
	for _, st := range statuses {
		if st.IsLead() || st.Status != state.AgentReady || st.Branch == "" {
			continue
		}
		candidates = append(candidates, st)
		agents = append(agents, st.Agent)
	}
	if len(candidates) == 0 {
		return 0
	}
	// From the cache, which refreshes itself behind this: a pull request
	// merged since the last refresh is caught by the next pass. It is the
	// agent's by its commits, whatever branch they were pushed to. A project
	// with no GitHub has none; one whose pull requests can't be read now —
	// git failing, GitHub not answering or never asked yet — is left alone
	// until they can, as "no pull request" is half of a reason to remove.
	var prs map[string]*api.PullRequest
	var unread map[string]bool
	switch _, entry, heads, _, err := s.projectPulls(p, agents); {
	case err != nil && !pullsAbsent(err):
		s.logf("remove finished agents in %s: its pull requests can't be read: %v", p.Name, err)
		return 0
	case err == nil && (entry.listErr.failed() || entry.at.IsZero()):
		return 0
	case err == nil:
		prs, unread = entry.byAgent(heads), unreadHeads(heads)
	}
	repo, err := gitrepo.Open(p.Root)
	if err != nil {
		return 0
	}
	finished, err := s.lastFinished(ctx, p.Name)
	if err != nil {
		s.logf("remove finished agents in %s: %v", p.Name, err)
		return 0
	}

	removed := 0
	for _, st := range candidates {
		if unread[st.Name] {
			s.logf("keeping %s for now: its branch can't be read", st.Ref())
			continue
		}
		f, err := s.cleanupFactsOf(ctx, repo, st, prs[st.Name], finished)
		if err != nil {
			s.logf("keeping %s for now: %v", st.Ref(), err)
			continue
		}
		reason := removeReason(f, now)
		if reason == "" {
			continue
		}
		// What was read above is up to several seconds old, and read in bulk:
		// read it all again, this agent alone, right before doing what can't
		// be undone, and keep it on any error or any other answer.
		if again, err := s.recheckRemoval(ctx, m, repo, st, f.PR, now); err != nil || again != reason {
			if err == nil {
				err = fmt.Errorf("a fresh look says %q", cmp.Or(again, "keep it"))
			}
			s.logf("not removing %s (%s): %v", st.Ref(), reason, err)
			continue
		}
		s.logf("removing %s: %s", st.Ref(), reason)
		if err := s.destroyAgentNow(ctx, m, st.Agent, agent.DestroyOptions{PR: archivedPR(f.PR)}); err != nil {
			s.logf("removing %s: %v", st.Ref(), err)
			continue
		}
		s.captureEvent(ctx, st.Project, st.Name, "agent_retired", map[string]any{"how": "auto", "branch": st.Branch, "reason": reason}, "")
		removed++
	}
	return removed
}

// cleanupFactsOf reads what removeReason decides st from, pr being its pull
// request as the caller found it. Any read that fails is an error, never a
// "no": no changes, nothing uncommitted and nothing unpushed are each a
// reason to remove.
func (s *Server) cleanupFactsOf(ctx context.Context, repo gitrepo.Repo, st agent.Status, pr *api.PullRequest, finished map[string]time.Time) (cleanupFacts, error) {
	changes, err := measureChanges(st.Agent)
	if err != nil {
		return cleanupFacts{}, fmt.Errorf("its changes can't be read: %w", err)
	}
	busy, _, last, _ := s.idleOf(ctx, st, changes)
	f := cleanupFacts{
		Busy:       busy,
		Attended:   st.Interface == state.InterfaceCLI && st.State == "running",
		Dirty:      changes.Dirty,
		Changed:    changes.Files > 0,
		PR:         pr,
		CreatedAt:  st.CreatedAt,
		LastActive: last,
	}
	if at, ok := finished[st.Name]; ok {
		f.FinishedAt = &at
	}
	if f.Unpushed, err = unpushed(repo, st.Agent, pr); err != nil {
		return cleanupFacts{}, fmt.Errorf("its branch can't be read: %w", err)
	}
	return f, nil
}

// recheckRemoval is removeReason for st again from fresh reads of its record,
// its finish, its branch and its worktree, with the pull request the pass
// found (the cache's, which a GitHub hiccup can't empty: #447).
func (s *Server) recheckRemoval(ctx context.Context, m *agent.Manager, repo gitrepo.Repo, st agent.Status, pr *api.PullRequest, now time.Time) (string, error) {
	a, err := s.store.Agent(ctx, st.Project, st.Name)
	if err != nil {
		return "", err
	}
	if a.Status != state.AgentReady || a.Branch != st.Branch || a.Worktree != st.Worktree {
		return "", nil // it moved on since the pass listed it
	}
	fresh := st
	fresh.Agent = a
	finished, err := s.lastFinished(ctx, a.Project)
	if err != nil {
		return "", err
	}
	f, err := s.cleanupFactsOf(ctx, repo, fresh, pr, finished)
	if err != nil {
		return "", err
	}
	return removeReason(f, now), nil
}

// unpushed reports whether any of an agent's branches has commits that exist
// only in this repository: not merged into the project's branch, not on a
// remote, and not in the head of its pull request as GitHub last saw it. An
// error is a branch git couldn't read, whose commits may be anywhere.
func unpushed(repo gitrepo.Repo, a state.Agent, pr *api.PullRequest) (bool, error) {
	branches, err := agentBranches(a, nil)
	if err != nil {
		return false, err
	}
	for _, branch := range branches {
		on := a
		on.Branch = branch
		if out, err := branchUnpushed(repo, on, pr); out || err != nil {
			return out, err
		}
	}
	return false, nil
}

// branchUnpushed is unpushed for a.Branch alone. A git command failing past
// the tip only makes it look unpushed, which keeps the agent.
func branchUnpushed(repo gitrepo.Repo, a state.Agent, pr *api.PullRequest) (bool, error) {
	tip, err := repo.BranchTip(a.Branch)
	if err != nil {
		return false, err
	}
	if tip == "" {
		return false, nil // no branch left, so nothing on it to lose
	}
	if repo.Pushed(tip) || agent.BranchDisposable(repo, a) {
		return false, nil
	}
	return pr == nil || pr.HeadSHA == "" || !repo.IsAncestor(tip, pr.HeadSHA), nil
}

// lastFinished is when each of a project's agents last genuinely finished,
// from the events the rail's threads are made of.
func (s *Server) lastFinished(ctx context.Context, project string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	events, err := s.store.AgentEvents(ctx, project)
	if err != nil {
		return nil, err
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
	return out, nil
}

// destroyAgentNow stops what the daemon runs for an agent and destroys it,
// the one sequence every destroy goes through — by hand, retired, or removed
// here. The caller records why.
//
// The agent is archived with its pull request, read while its branch and
// worktree are still there to find it by (prFor); a caller that already has
// it passes it in opts.PR.
//
// Once begun it runs to the end whoever asked: a request whose client gave
// up mustn't stop it between deleting the machine and archiving the agent.
func (s *Server) destroyAgentNow(ctx context.Context, m *agent.Manager, a state.Agent, opts agent.DestroyOptions) error {
	ctx = context.WithoutCancel(ctx)
	// The app moves it to Past agents when it hears it was removed, which is
	// the moment it is archived, not once its worktree is deleted too.
	opts.Archived = func() { s.agentRemoved(a.Ref()) }
	if opts.PR.Number == 0 && a.Status != state.AgentQueued {
		lookup, cancel := context.WithTimeout(ctx, 15*time.Second)
		if pr := s.prFor(lookup, a); pr != nil {
			opts.PR = archivedPR(pr)
		}
		cancel()
	}
	if err := m.Destroy(ctx, a, opts); err != nil {
		return err
	}
	s.chat.Stop(a.Ref(), "the agent was destroyed")
	s.removeActiveAgent(ctx, a.Project, a.Name)
	s.chat.Forget(a.Ref())
	s.disks.forget(a.Ref())
	s.dropHeavy(ctx, a.Ref())
	s.stopAgentAPI(a.Instance)
	s.removeBrowserSockets(a.Instance)
	return nil
}

// archivedPR is a pull request as an archived agent keeps it; nil is none.
func archivedPR(pr *api.PullRequest) state.ArchivedPR {
	if pr == nil {
		return state.ArchivedPR{}
	}
	return state.ArchivedPR{Number: pr.Number, Title: pr.Title, URL: pr.URL, State: pr.State}
}
