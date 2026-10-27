package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The agent queue: an agent whose clusters are all full waits here, and the
// daemon starts it as soon as one has room. It is the shape the earlier
// release's queue had — an agents row with status queued and a row in
// agent_queue that carries the create request — so an agent that release left
// queued is started the same way, by the same loop.
//
// A create that doesn't ask to wait fails instead (api.CreateAgentRequest.Queue):
// an agent that silently doesn't start is worse than an error that says what is
// full and how to wait for it.

// queueEvery is how often the queue is tried. It is quicker than the capacity
// loop, because the number placement reads is measured on demand when it is
// stale: the tick is only how long a freed slot waits for the next agent.
const queueEvery = 10 * time.Second

// queuedRequest is what a queued agent is started from: the create request as
// it was made, and whether the project's chat made it, which the chat is still
// waiting to hear about.
type queuedRequest struct {
	Request api.CreateAgentRequest `json:"request"`
	ByLead  bool                   `json:"byLead,omitempty"`
}

// runQueue is the loop Run starts: it tries the queue at once, for what an
// earlier daemon left waiting, then every queueEvery.
func (s *Server) runQueue(ctx context.Context) {
	ticker := time.NewTicker(queueEvery)
	defer ticker.Stop()
	for {
		s.startQueuedAgents(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// startQueuedAgents starts every queued agent a cluster has room for, oldest
// first. A *NoRoomError means that agent is still waiting, which is not an
// error: the next pass tries it again. Anything else is logged and leaves it
// queued, because a cluster that can't be reached this minute may answer the
// next.
func (s *Server) startQueuedAgents(ctx context.Context) {
	queue, err := s.store.LeftoverQueue(ctx, "")
	if err != nil {
		if ctx.Err() == nil {
			s.logf("reading the agent queue: %v", err)
		}
		return
	}
	for _, q := range queue {
		if ctx.Err() != nil {
			return
		}
		if err := s.startQueuedAgent(ctx, q); err != nil {
			var noRoom *NoRoomError
			if errors.As(err, &noRoom) {
				continue
			}
			s.logf("queued agent %s: %v", q.Ref(), err)
		}
	}
}

// startQueuedAgent decides where a queued agent runs now, and starts the job
// that makes it. It returns a *NoRoomError while nothing has room for it.
func (s *Server) startQueuedAgent(ctx context.Context, q state.QueuedAgent) error {
	var req queuedRequest
	if err := json.Unmarshal(q.Request, &req); err != nil {
		// Nothing can start from a request that can't be read, and it would
		// fail every pass: it leaves the queue the way a failed create leaves
		// nothing behind.
		s.logf("queued agent %s: reading its request: %v", q.Ref(), err)
		if err := s.store.RemoveAgent(context.WithoutCancel(ctx), q.Project, q.Name); err != nil {
			s.logf("removing queued agent %s: %v", q.Ref(), err)
		}
		return nil
	}
	p, err := s.store.Project(ctx, q.Project)
	if err != nil {
		return err
	}
	cluster, err := s.placeAgent(ctx, p, req.Request.Cluster)
	if err != nil {
		return err
	}
	a, err := s.store.Agent(ctx, q.Project, q.Name)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// Its row is gone: an entry naming an agent nothing will make is
			// not a queue, and would be logged every pass forever.
			return s.store.Dequeue(context.WithoutCancel(ctx), q.Project, q.Name)
		}
		return err
	}
	// The concrete cluster goes with the job: it was admitted here, and the
	// job uses a name it is given rather than asking again.
	req.Request.Cluster = cluster
	if _, err := s.jobs.start("create", q.Project, func(ctx context.Context, log io.Writer) (any, error) {
		s.logf("starting %s, queued until a cluster had room", q.Ref())
		out, err := s.createJob(req.Request, req.ByLead, q.Name)(ctx, log)
		if err != nil {
			// A slot that filled between the pass and the job leaves it
			// queued: this is the one failure that is worth trying again.
			var noRoom *NoRoomError
			if errors.As(err, &noRoom) {
				return out, err
			}
			// Failing before it left the queue — its tool isn't logged in,
			// the base image went — would fail again every time it was
			// tried, so it goes the way a failed create leaves nothing
			// behind; failing after, the create has removed it.
			if cur, getErr := s.store.Agent(ctx, q.Project, q.Name); getErr == nil && cur.Status == state.AgentQueued {
				_ = s.store.RemoveAgent(context.WithoutCancel(ctx), q.Project, q.Name)
			}
		}
		return out, err
	}); err != nil {
		return err
	}
	// Before the pod is there, so the next queued agent doesn't take the same
	// slot.
	if cluster != "" {
		if c, err := s.store.Cluster(ctx, cluster); err == nil {
			s.reserve(c)
		}
	}
	s.tellLead(ctx, q.Project, fmt.Sprintf("Queued agent %s%s is starting: %s has room for it now.", q.Name, withTitle(a.Title), cmp.Or(cluster, "this machine")), false)
	return nil
}

// queueCreate accepts an agent that can't start yet: it takes the name, writes
// the row and the request, and answers with a finished job whose result is the
// queued agent, so every caller that waits on a create job sees it.
func (s *Server) queueCreate(ctx context.Context, req api.CreateAgentRequest, byLead bool, reason string) (api.Job, error) {
	p, err := s.store.Project(ctx, req.Project)
	if err != nil {
		return api.Job{}, err
	}
	name := req.Name
	if name == "" {
		if name, err = s.manager(nil).NextName(ctx, p); err != nil {
			return api.Job{}, err
		}
	}
	autonomous := req.Autonomous == nil || *req.Autonomous
	a := state.Agent{
		Project:          p.Name,
		Name:             name,
		Title:            req.Title,
		Instance:         agent.InstanceName(p.Name, name),
		AI:               req.AI,
		Autonomous:       autonomous,
		Branch:           req.Branch,
		Worktree:         "",
		Status:           state.AgentQueued,
		CreatedAt:        time.Now(),
		ClaudeAccount:    req.ClaudeAccount,
		CodexAccount:     req.CodexAccount,
		OpenCodeAccounts: req.OpenCodeAccounts,
		CursorAccount:    req.CursorAccount,
		GitHubAccount:    req.GitHubAccount,
		Interface:        req.Interface,
		FinishNotice:     req.FinishNotice,
		Connectors:       connectorsOf(req),
	}
	request, err := json.Marshal(queuedRequest{Request: req, ByLead: byLead})
	if err != nil {
		return api.Job{}, err
	}
	if err := s.store.QueueAgent(ctx, a, request, reason); err != nil {
		return api.Job{}, err
	}
	s.logf("Queued %s: %s", a.Ref(), reason)
	s.events.publish(api.EventAgent, api.AgentChange{Ref: a.Ref(), State: state.AgentQueued})
	describe := func(ctx context.Context, _ io.Writer) (any, error) {
		return s.describe(context.WithoutCancel(ctx), a)
	}
	return s.launchJob("create", p.Name, describe)
}

// connectorsOf is the request's connector limit, copied: the request outlives
// the call and the queue keeps it as JSON.
func connectorsOf(req api.CreateAgentRequest) []string {
	if req.Connectors == nil {
		return nil
	}
	return append([]string{}, *req.Connectors...)
}

func withTitle(title string) string {
	if title == "" {
		return ""
	}
	return fmt.Sprintf(" (%q)", title)
}

// queuedInfo fills in the queue's own fields on an agent that is waiting: its
// place in its project's line, and why it is there.
func (s *Server) queuedInfo(ctx context.Context, info *api.Agent) {
	if info.State != state.AgentQueued {
		return
	}
	queue, err := s.store.LeftoverQueue(ctx, info.Project)
	if err != nil {
		return
	}
	for _, q := range queue {
		if q.Name != info.Name {
			continue
		}
		info.QueuePosition, info.QueueReason = q.Position, q.Reason
		return
	}
}
