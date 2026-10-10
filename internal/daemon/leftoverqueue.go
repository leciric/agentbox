package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// What an earlier release left queued. It had an agent queue: a create could
// wait for one of its project's slots, with its agent's name, title, branch
// and task given at once and its machine later, and a task of the Tasks tab
// could wait for the lead behind the agents queued before it. Nothing queues
// any more, so as the daemon starts it starts every agent left queued, the
// way the queue would have once a slot came free, and hands the lead every
// task left waiting for it.

// queuedRequest is what a queued agent is started from: the create request as
// it was made, and whether the project's chat made it, which the chat is still
// waiting to hear about.
type queuedRequest struct {
	Request api.CreateAgentRequest `json:"request"`
	ByLead  bool                   `json:"byLead,omitempty"`
}

// startLeftoverQueue starts what an earlier release left queued.
func (s *Server) startLeftoverQueue(ctx context.Context) {
	queue, err := s.store.LeftoverQueue(ctx, "")
	if err != nil {
		s.logf("agents left queued: %v", err)
	}
	for _, q := range queue {
		if err := s.startQueuedAgent(ctx, q); err != nil {
			s.logf("agents left queued: starting %s: %v", q.Ref(), err)
		}
	}
	tasks, err := s.memory().LeadQueue(ctx, "")
	if err != nil {
		s.logf("tasks left queued for the lead: %v", err)
		return
	}
	for _, t := range tasks {
		if _, err := s.taskToLead(ctx, t); err != nil {
			s.logf("tasks left queued for the lead: sending task %s to %s's chat: %v", t.ID, t.Project, err)
		}
	}
}

// startQueuedAgent starts the job that makes a queued agent, and tells the
// project's chat, in one line, that it's on its way.
func (s *Server) startQueuedAgent(ctx context.Context, q state.QueuedAgent) error {
	var req queuedRequest
	if err := json.Unmarshal(q.Request, &req); err != nil {
		return fmt.Errorf("reading its request: %w", err)
	}
	a, err := s.store.Agent(ctx, q.Project, q.Name)
	if err != nil {
		return err
	}
	create := s.createJob(req.Request, req.ByLead, q.Name)
	if _, err := s.jobs.start("create", q.Project, func(ctx context.Context, log io.Writer) (any, error) {
		s.logf("starting %s, left queued by an earlier release", q.Ref())
		out, err := create(ctx, log)
		if err != nil {
			// Failing before it left the queue — its tool isn't logged in,
			// the base image went — would fail again every time it was
			// tried, so it goes the way a failed create leaves nothing
			// behind; failing after, the create has removed it.
			if cur, getErr := s.store.Agent(ctx, q.Project, q.Name); getErr == nil && cur.Status == state.AgentQueued {
				_ = s.store.RemoveAgent(context.WithoutCancel(ctx), q.Project, q.Name)
			}
			s.releaseQueuedTasks(context.WithoutCancel(ctx), a)
		}
		return out, err
	}); err != nil {
		return err
	}
	s.tellLead(ctx, q.Project, fmt.Sprintf("Queued agent %s%s is starting: AgentBox no longer queues agents.", q.Name, withTitle(a.Title)), false)
	return nil
}

func withTitle(title string) string {
	if title == "" {
		return ""
	}
	return fmt.Sprintf(" (%q)", title)
}
