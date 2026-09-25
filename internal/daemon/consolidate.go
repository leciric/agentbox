package daemon

import (
	"context"
	"errors"
	"net/http"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// When consolidation runs (D76).
//
// There is no periodic job runner in this package and this doesn't build one.
// A general scheduler would be a table, a leader, a retry policy and a way
// for a job to be stuck — for two passes, one of which is a handful of SQL
// statements. What is here instead is the two things the daemon already has:
// a ticker it owns for the length of Run, the way media retention has one,
// and the chokepoint where a lead turn ends, the way capture does.
//
//   - The mechanical pass runs on the ticker, once at startup, and after a
//     lead turn when it hasn't run recently. It costs nothing, so the only
//     thing worth avoiding is doing it twice in a minute.
//   - The distillation pass runs after a lead turn, when the project has
//     gathered more events than its consolidation setting since its
//     watermark.
//
// Neither ever blocks what triggered it: both are called from the goroutine
// the Publish hook already starts off its own stack, and from Run's own
// background context. A pass that fails is a log line.

// consolidationInterval is how often the daemon sweeps every project's
// memories mechanically. Decay is measured in weeks and duplicates appear as
// fast as memories are written, so hourly is far more often than it needs to
// be and still costs nothing worth measuring.
const consolidationInterval = time.Hour

// consolidationMinGap is how soon after a mechanical pass another one is
// worth running. A busy project finishes a dozen agents in an hour, and
// comparing the same two hundred titles a dozen times says nothing new.
const consolidationMinGap = 10 * time.Minute

// sweepMemories runs the mechanical pass over every project, at startup and
// then on the ticker. Like sweepMedia it runs once immediately, so a daemon
// that is restarted often still gets there.
func (s *Server) sweepMemories(ctx context.Context) {
	s.consolidateAll(ctx)
	ticker := time.NewTicker(consolidationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.consolidateAll(ctx)
		}
	}
}

func (s *Server) consolidateAll(ctx context.Context) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		s.logf("consolidating: listing projects: %v", err)
		return
	}
	for _, p := range projects {
		s.consolidateMechanically(ctx, p, time.Time{})
	}
}

// consolidateMechanically runs the pass for one project, unless it is
// switched off or one ran within notBefore of now. It answers the pass it
// ran, and whether it ran one at all.
func (s *Server) consolidateMechanically(ctx context.Context, p state.Project, notBefore time.Time) (memory.Pass, bool) {
	if p.Consolidation == state.ConsolidationOff {
		return memory.Pass{}, false
	}
	if !notBefore.IsZero() {
		recent, err := s.memory().Passes(ctx, p.Name, consolidationHistory)
		if err != nil {
			s.logf("consolidating %s: reading its passes: %v", p.Name, err)
			return memory.Pass{}, false
		}
		for _, was := range recent {
			if was.Kind == memory.PassMechanical && was.At.After(notBefore) {
				return memory.Pass{}, false
			}
		}
	}
	pass, err := s.memory().Consolidate(ctx, p.Name, memory.ConsolidateOptions{})
	if err != nil {
		s.logf("consolidating %s: %v", p.Name, err)
		return memory.Pass{}, false
	}
	if pass.MemoriesSuperseded+pass.MemoriesDecayed > 0 {
		s.logf("consolidating %s: merged %d memories, aged %d, flagged %d near-duplicates",
			p.Name, pass.MemoriesSuperseded, pass.MemoriesDecayed, pass.DuplicatesFound)
	}
	return pass, true
}

// consolidateAfterLeadTurn is the chokepoint. A lead turn has just ended, so
// the chat's session is idle and warm and nobody is waiting on it: the
// cheapest moment there is to tidy what the project remembers and, when
// enough has happened, to ask the session to make sense of it.
//
// It is called from captureLeadTurn's goroutine, which is already off the
// conversation's lock, and it takes as long as it takes. Nothing waits for it.
func (s *Server) consolidateAfterLeadTurn(ctx context.Context, project string) {
	p, err := s.store.Project(ctx, project)
	if err != nil || p.Consolidation == state.ConsolidationOff {
		return
	}
	s.consolidateMechanically(ctx, p, time.Now().Add(-consolidationMinGap))
	lead, err := s.store.Agent(ctx, project, state.LeadName)
	if err != nil {
		return
	}
	s.distillIfDue(ctx, lead)
}

// askLeadSession is D76's seam behind a distillation: a hidden prompt on the
// project chat's own session. Server.askLead points at it, and a test points
// that field somewhere it can predict.
func (s *Server) askLeadSession(ctx context.Context, a state.Agent, ask string) (string, error) {
	return s.chat.Ask(ctx, a, ask)
}

// askAsideSession is D78's: the same prompt in a session of its own, started
// for it and thrown away afterwards, on the model the project consolidates
// with. It runs the agent's own AI tool through the same launcher its chat
// uses, so there is no second login and no second way to fail to start one.
func (s *Server) askAsideSession(ctx context.Context, a state.Agent, model, ask string) (string, string, error) {
	return s.chat.AskAside(ctx, a, model, ask)
}

// consolidateNow runs the passes on demand, whatever the ticker and the
// watermark would have decided — the same path the daemon takes by itself,
// for a user who wants to see it happen. Distillation is only attempted when
// the request asks for it, because it spends the project's tokens.
func (s *Server) consolidateNow(ctx context.Context, project string, distil bool) ([]memory.Pass, error) {
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return nil, err
	}
	if p.Consolidation == state.ConsolidationOff {
		return nil, errors.New("consolidation is switched off for this project: agentbox consolidation " + project + " <events> turns it back on")
	}
	var passes []memory.Pass
	if pass, ran := s.consolidateMechanically(ctx, p, time.Time{}); ran {
		passes = append(passes, pass)
	}
	if !distil {
		return passes, nil
	}
	// The lead is a row, a worktree and a login, and that is all a
	// distillation needs of it: the pass runs in a session of its own unless
	// the project asked for the chat's (D78). A project nobody has ever
	// chatted with has no lead row at all, and nothing to run the tool as.
	lead, err := s.store.Agent(ctx, project, state.LeadName)
	if err != nil {
		return passes, errors.New("this project's chat has never started, so there is nothing to distil its events with")
	}
	watermark, err := s.memory().Watermark(ctx, project)
	if err != nil {
		return passes, err
	}
	if err := s.distill(ctx, p, lead, watermark); err != nil {
		return passes, err
	}
	distilled, err := s.memory().Passes(ctx, project, 1)
	if err == nil && len(distilled) > 0 && distilled[0].Kind == memory.PassDistill {
		passes = append(passes, distilled[0])
	}
	return passes, nil
}

// consolidateProject is POST /v1/projects/{project}/memory/consolidate.
func (s *Server) consolidateProject(w http.ResponseWriter, r *http.Request, project string) error {
	var req api.ConsolidateRequest
	if r.ContentLength > 0 {
		if err := readJSON(r, &req); err != nil {
			return err
		}
	}
	passes, err := s.consolidateNow(r.Context(), project, req.Distil)
	if err != nil {
		return err
	}
	out := make([]api.ConsolidationPass, 0, len(passes))
	for _, p := range passes {
		out = append(out, apiPass(p))
	}
	return writeJSON(w, http.StatusOK, out)
}
