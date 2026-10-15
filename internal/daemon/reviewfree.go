package daemon

import (
	"context"
	"fmt"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// An agent the user has reviewed, or whose pull request they merged from the
// review, has nothing more to do with its machine: freeReviewed retires it
// with destroy, so it becomes a past agent that keeps its chat, media and
// pull request. It is done here, on the review path, so the desktop, the
// phone and the API all get it.
//
// The machine is never freed over work that exists only there. With
// uncommitted or unpushed work it is kept (ReviewItem.MachineKept) and the
// lead is told, as a notice that doesn't start a turn. An agent still at work
// is not interrupted: it is remembered (freeWhenIdle) and freed when its turn
// ends (noticeAgentFinished). That memory lasts as long as the daemon does.

// freeReviewed frees a's machine if the work is safe, as above. why says what
// asked for it, for the log; pr is its pull request when the caller has it, else it is looked up.
func (s *Server) freeReviewed(ctx context.Context, a state.Agent, why string, pr *api.PullRequest) {
	if a.IsLead() || a.Status == state.AgentQueued {
		return
	}
	if s.chat.Busy(a) {
		s.setFreeWhenIdle(a.Ref(), true)
		s.logf("%s: %s; its turn is running, so its machine is freed when the turn ends", a.Ref(), why)
		return
	}
	s.setFreeWhenIdle(a.Ref(), false)
	if pr == nil {
		pr = s.prFor(ctx, a)
	}
	reason := s.unsafeToFree(ctx, a, pr)
	if reason != "" {
		s.logf("%s: %s; machine kept: %s", a.Ref(), why, reason)
		if s.setKept(a.Ref(), true) {
			s.events.publish(api.EventReview, a.Project)
			s.tellLead(ctx, a.Project, keptNotice(a, reason), false)
		}
		return
	}
	s.setKept(a.Ref(), false)
	// As retireOne destroys, but with the pull request already read. The
	// branch and the media stay, as there.
	if err := s.destroyAgentNow(ctx, s.manager(s.cfg.Log), a, agent.DestroyOptions{Force: true, PR: archivedPR(pr)}); err != nil {
		s.logf("%s: %s; freeing its machine: %v", a.Ref(), why, err)
		return
	}
	s.captureEvent(ctx, a.Project, a.Name, "agent_retired", map[string]any{"how": api.RetireDestroy, "branch": a.Branch, "reason": why}, "")
	s.removeActiveAgent(ctx, a.Project, a.Name)
	s.logf("%s: %s; machine freed, the agent is now a past agent", a.Ref(), why)
	s.recordAgentFinished(ctx, a, "retired")
	s.events.publish(api.EventReview, a.Project)
	s.refreshAgents(ctx)
}

// unsafeToFree says what stands in the way of freeing a's machine: work that
// is only there. "" means nothing does. When the state can't be read it is
// unsafe, and the machine stays.
func (s *Server) unsafeToFree(ctx context.Context, a state.Agent, pr *api.PullRequest) string {
	if changesOf(a).Dirty {
		return "uncommitted changes"
	}
	p, err := s.store.Project(ctx, a.Project)
	if err != nil {
		return "its project can't be read"
	}
	repo, err := gitrepo.Open(p.Root)
	if err != nil {
		return "its repository can't be read"
	}
	if unpushed(repo, a, pr) {
		return "commits that aren't pushed"
	}
	return ""
}

func keptNotice(a state.Agent, reason string) string {
	return fmt.Sprintf("[AgentBox: the user reviewed %s (%s) but its machine was kept: it has %s. "+
		"Ask it to commit and push, or retire it once that is done.]", a.Name, titleOrNone(a.Title), reason)
}

// setFreeWhenIdle remembers, or forgets, that a's machine is to be freed when
// its turn ends.
func (s *Server) setFreeWhenIdle(ref string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		if s.freeWhenIdle == nil {
			s.freeWhenIdle = map[string]bool{}
		}
		s.freeWhenIdle[ref] = true
		return
	}
	delete(s.freeWhenIdle, ref)
}

// setKept records that a's machine was kept after a review, and reports
// whether that is news.
func (s *Server) setKept(ref string, on bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		if s.machineKept == nil {
			s.machineKept = map[string]bool{}
		}
		changed := !s.machineKept[ref]
		s.machineKept[ref] = true
		return changed
	}
	changed := s.machineKept[ref]
	delete(s.machineKept, ref)
	return changed
}

func (s *Server) isMachineKept(ref string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machineKept[ref]
}

// freeAfterTurn frees a's machine now that its turn ended, if a review asked
// for that while it worked.
func (s *Server) freeAfterTurn(ctx context.Context, a state.Agent) {
	s.mu.Lock()
	pending := s.freeWhenIdle[a.Ref()]
	s.mu.Unlock()
	if !pending {
		return
	}
	fresh, err := s.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		return
	}
	s.freeReviewed(ctx, fresh, "reviewed while it worked", nil)
}
