package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Retiring an agent frees what it holds when it has finished. An agent is one
// task: its deliverable is its branch, which outlives it, so the cheapest way
// to move on is to retire the agent and make a new one — creating one takes
// 1.4 s from the project's base ([D2], [D12]). Destroying one deletes its
// branch only when that loses nothing (agent.BranchDisposable).

// idleOf works out whether an agent has finished and is holding a machine for
// nothing, and whether retiring it now would lose anything.
func (s *Server) idleOf(ctx context.Context, st agent.Status, changes api.AgentChanges) (busy, idle bool, last *time.Time, advice api.RetireAdvice) {
	advice.Branch = st.Branch
	chat := s.chat.State(st.Ref())
	busy = chat == api.ChatRunning || chat == api.ChatWaiting || chat == api.ChatStarting

	if item, ok, err := s.store.LastChatItem(ctx, st.Project, st.Name); err == nil && ok {
		var stored struct {
			UpdatedAt time.Time `json:"updatedAt"`
		}
		if json.Unmarshal(item.Data, &stored) == nil && !stored.UpdatedAt.IsZero() {
			last = &stored.UpdatedAt
		}
	}

	// Idle means: finished, and still holding a running machine. An agent that
	// is already stopped holds only disk, which the fleet doesn't nag about.
	idle = !busy && st.State == "running"
	switch {
	case busy:
		advice.Reason = "it is still working"
	case st.State != "running":
		advice.Reason = "its machine is already " + st.State
	}
	// Uncommitted work is the one thing destroying an agent would lose. Pausing
	// and stopping keep the worktree either way.
	advice.Safe = !changes.Dirty
	if changes.Dirty {
		advice.Reason = "it has uncommitted work: committing it puts it on " + st.Branch
	}
	return busy, idle, last, advice
}

// skipReason says why an agent shouldn't be retired this way, or "" to go
// ahead. What counts depends on how: pausing or stopping a machine that isn't
// running does nothing, while destroying one still frees its worktree.
func skipReason(req api.RetireRequest, st agent.Status, busy bool, last *time.Time, advice api.RetireAdvice, idleFor time.Duration) string {
	named := len(req.Agents) > 0
	switch {
	// A sweep never interrupts an agent that is working. Stopping one that is
	// takes naming it and meaning it.
	case busy && !(named && req.Force):
		return "it is still working"
	case !advice.Safe && !req.Force:
		return advice.Reason
	case req.How != api.RetireDestroy && st.State != "running":
		return "its machine is already " + st.State
	case named:
		return "" // you named it, so you meant it
	case idleFor > 0 && (last == nil || time.Since(*last) < idleFor):
		return fmt.Sprintf("it was active less than %s ago", req.IdleFor)
	}
	return ""
}

// retire frees what a project's finished agents are holding.
func (s *Server) retire(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	project := r.PathValue("project")
	if _, err := s.store.Project(ctx, project); err != nil {
		return err
	}
	var req api.RetireRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.How == "" {
		req.How = api.RetireStop
	}
	if !slices.Contains([]string{api.RetirePause, api.RetireStop, api.RetireDestroy}, req.How) {
		return fmt.Errorf("unknown way to retire %q: use pause, stop or destroy", req.How)
	}
	var idleFor time.Duration
	if req.IdleFor != "" {
		d, err := time.ParseDuration(req.IdleFor)
		if err != nil {
			return fmt.Errorf("invalid idleFor %q: use a duration like 30m", req.IdleFor)
		}
		idleFor = d
	}

	m := s.manager(s.cfg.Log)
	statuses, err := m.List(ctx, project)
	if err != nil {
		return err
	}
	out := api.RetireResult{How: req.How, DryRun: req.DryRun, Retired: []api.RetiredAgent{}, Skipped: []api.RetiredAgent{}}
	for _, st := range statuses {
		if st.IsLead() {
			continue // the project's chat has no machine to free
		}
		if len(req.Agents) > 0 && !slices.Contains(req.Agents, st.Name) {
			continue
		}
		who := api.RetiredAgent{Name: st.Name, Title: st.Title, Branch: st.Branch}
		changes := changesOf(st.Agent)
		busy, _, last, advice := s.idleOf(ctx, st, changes)
		if who.Reason = skipReason(req, st, busy, last, advice, idleFor); who.Reason != "" {
			out.Skipped = append(out.Skipped, who)
			continue
		}
		if req.DryRun {
			out.Retired = append(out.Retired, who)
			continue
		}
		if err := s.retireOne(ctx, m, st.Agent, req.How); err != nil {
			who.Reason = err.Error()
			out.Skipped = append(out.Skipped, who)
			continue
		}
		out.Retired = append(out.Retired, who)
	}
	s.refreshAgents(ctx)
	return writeJSON(w, http.StatusOK, out)
}

// retireOne frees one agent. The branch is kept unless it is merged or
// pushed: until then it is the work.
func (s *Server) retireOne(ctx context.Context, m *agent.Manager, a state.Agent, how string) error {
	var err error
	switch how {
	case api.RetirePause:
		s.chat.Stop(a.Ref(), "the agent was retired")
		err = m.Pause(ctx, a)
	case api.RetireStop:
		s.chat.Stop(a.Ref(), "the agent was retired")
		err = m.Stop(ctx, a)
	case api.RetireDestroy:
		// Force discards the worktree, which the caller has already agreed to;
		// DeleteBranch and DeleteMedia stay false, so unmerged work and its
		// proof both survive the agent.
		err = s.destroyAgentNow(ctx, m, a, agent.DestroyOptions{Force: true})
	default:
		return errors.New("unknown way to retire")
	}
	if err != nil {
		return err
	}
	s.captureEvent(ctx, a.Project, a.Name, "agent_retired", map[string]any{"how": how, "branch": a.Branch}, "")
	s.removeActiveAgent(ctx, a.Project, a.Name)
	return nil
}
