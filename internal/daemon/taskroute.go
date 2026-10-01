package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// Where a task of the Tasks tab goes when it starts. It goes to a new agent of
// its own, which is what a task always did, or to the project's lead, as a
// message the user could have typed, so the lead can split it across several
// agents. Each task follows the installation's "tasks go to" setting
// (state.SettingTaskTarget) until somebody chooses for that task
// (memory.Task.Route).
//
// A task for the lead takes no slot, since the lead has no machine, but it
// still keeps its place in the queue: queued, it waits until every agent its
// project queued before it has started, and then goes. With the agent queue
// off nothing waits, here as for agents.

// taskTarget is the installation's "tasks go to": "agent" unless it says
// "lead".
func (s *Server) taskTarget(ctx context.Context) (string, error) {
	value, err := s.store.Setting(ctx, state.SettingTaskTarget)
	if err != nil {
		return "", err
	}
	return memory.EffectiveTaskRoute(memory.TaskRouteFollow, value), nil
}

// startTask sends one of the user's tasks where it goes: to a new agent, made
// or queued the way a create is, or to the project's lead, now or once its
// turn in the queue comes.
func (s *Server) startTask(ctx context.Context, project, id string, req api.StartTaskRequest) (api.StartTaskResponse, error) {
	t, err := s.memory().Task(ctx, project, id)
	if err != nil {
		return api.StartTaskResponse{}, err
	}
	setting, err := s.taskTarget(ctx)
	if err != nil {
		return api.StartTaskResponse{}, err
	}
	if err := startable(t); err != nil {
		return api.StartTaskResponse{}, err
	}
	target := memory.EffectiveTaskRoute(t.Route, setting)
	if target == memory.TaskRouteAgent {
		queue := req.Queue
		j, err := s.createAgentJob(ctx, api.CreateAgentRequest{Project: project, TaskID: t.ID, Queue: &queue, AI: req.AI}, false)
		if err != nil {
			return api.StartTaskResponse{}, err
		}
		if t, err = s.memory().Task(ctx, project, id); err != nil {
			return api.StartTaskResponse{}, err
		}
		return api.StartTaskResponse{Target: target, Task: apiTask(t), Job: &j}, nil
	}
	// Under the queue's lock, so the queue and a second start can't hand the
	// same task over while this one does.
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if t, err = s.memory().Task(ctx, project, id); err != nil {
		return api.StartTaskResponse{}, err
	}
	if err := startable(t); err != nil {
		return api.StartTaskResponse{}, err
	}
	if queueOn, _ := s.store.Flag(ctx, state.SettingAgentQueue); req.Queue && queueOn {
		now := time.Now()
		if t, err = s.memory().UpdateTask(ctx, project, id, memory.TaskPatch{LeadQueuedAt: &now}); err != nil {
			return api.StartTaskResponse{}, err
		}
		// It goes at once when nothing was queued before it.
		s.kickQueue()
	} else if t, err = s.taskToLead(ctx, t); err != nil {
		return api.StartTaskResponse{}, err
	}
	return api.StartTaskResponse{Target: target, Task: apiTask(t)}, nil
}

// startable refuses a task that is already somebody's, or on its way to the
// lead: starting it again would hand the same work over twice.
func startable(t memory.Task) error {
	switch {
	case !t.Open():
		return fmt.Errorf("task %s is %s: only an open task can be started", t.ID, t.Status)
	case !t.LeadQueuedAt.IsZero():
		return fmt.Errorf("task %s is queued for the lead already", t.ID)
	case t.Agent != "":
		return fmt.Errorf("task %s is already %s's", t.ID, t.Agent)
	}
	return nil
}

// unqueueTask takes a queued task back to the backlog: one waiting for the
// lead leaves the queue, and one waiting for its agent takes the agent out of
// the queue, which hands the task back.
func (s *Server) unqueueTask(ctx context.Context, project, id string) (memory.Task, error) {
	t, err := s.memory().Task(ctx, project, id)
	if err != nil {
		return memory.Task{}, err
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if !t.LeadQueuedAt.IsZero() {
		// Re-read under the lock: the queue may have just handed it over.
		if t, err = s.memory().Task(ctx, project, id); err != nil {
			return memory.Task{}, err
		}
		if t.LeadQueuedAt.IsZero() {
			return memory.Task{}, fmt.Errorf("task %s has gone to the lead already", id)
		}
		var none time.Time
		return s.memory().UpdateTask(ctx, project, id, memory.TaskPatch{LeadQueuedAt: &none})
	}
	if t.Agent == "" {
		return memory.Task{}, fmt.Errorf("task %s isn't queued", id)
	}
	a, err := s.store.Agent(ctx, project, t.Agent)
	if err != nil || a.Status != state.AgentQueued || s.queueStarting(a.Ref()) {
		return memory.Task{}, fmt.Errorf("task %s isn't queued: %s has started", id, t.Agent)
	}
	if err := s.store.RemoveAgent(ctx, a.Project, a.Name); err != nil {
		return memory.Task{}, err
	}
	s.releaseQueuedTasks(ctx, a)
	s.captureEvent(ctx, a.Project, a.Name, "agent_retired", map[string]any{"how": "unqueued", "branch": a.Branch}, "")
	s.refreshAgents(ctx)
	return s.memory().Task(ctx, project, id)
}

// admitLeadTasks hands the lead every task queued for it that has reached the
// front of its project's queue: no agent queued before it is still waiting.
// With the queue off, every one of them has. The caller holds queueMu.
func (s *Server) admitLeadTasks(ctx context.Context, queue []state.QueuedAgent, queueOn bool) {
	waiting, err := s.memory().LeadQueue(ctx, "")
	if err != nil {
		s.logf("agent queue: %v", err)
		return
	}
	// An agent the queue is starting already has left it, as far as what
	// waits behind it is concerned.
	var still []state.QueuedAgent
	for _, q := range queue {
		if !s.queueStarting(q.Ref()) {
			still = append(still, q)
		}
	}
	for _, t := range waiting {
		if queueOn && agentsAhead(still, t) > 0 {
			continue
		}
		if _, err := s.taskToLead(ctx, t); err != nil {
			s.logf("agent queue: sending task %s to %s's chat: %v", t.ID, t.Project, err)
		}
	}
}

// agentsAhead is how many agents of t's project were queued before it and are
// still waiting.
func agentsAhead(queue []state.QueuedAgent, t memory.Task) int {
	n := 0
	for _, q := range queue {
		// The queue keeps seconds; a task queued in the same second as an
		// agent goes first, which only ever lets the lead start sooner.
		if q.Project == t.Project && q.QueuedAt.Before(t.LeadQueuedAt.Truncate(time.Second)) {
			n++
		}
	}
	return n
}

// taskToLead sends a task to its project's lead as a message from the user,
// starting the lead's session if it has none, and makes the task the lead's:
// active, with the lead as its agent.
func (s *Server) taskToLead(ctx context.Context, t memory.Task) (memory.Task, error) {
	if t.Project == state.HomeProject {
		return memory.Task{}, errors.New("the Main chat has no tasks to take")
	}
	m := s.manager(nil)
	lead, err := m.EnsureLead(ctx, t.Project)
	if err != nil {
		return memory.Task{}, err
	}
	// The same as a message typed in the chat (ensureLeadFromPath): a turn is
	// about to start, which is when a full session may be replaced, and the
	// lead's worktree is brought to the tip of its branch first.
	s.rolloverIfNeeded(ctx, lead)
	if lead, err = m.SyncLead(ctx, lead); err != nil {
		return memory.Task{}, err
	}
	if _, err := s.chat.Send(lead, leadTaskMessage(t)); err != nil {
		return memory.Task{}, err
	}
	owner, active := memory.TaskLead, memory.TaskActive
	var none time.Time
	out, err := s.memory().UpdateTask(ctx, t.Project, t.ID, memory.TaskPatch{Agent: &owner, Status: &active, LeadQueuedAt: &none})
	if err != nil {
		return memory.Task{}, err
	}
	s.captureTaskStatus(ctx, out, t.Status)
	s.countFeature(agentFeature(lead.AI, api.FeatureLeadTurnClaude, api.FeatureLeadTurnCodex, api.FeatureLeadTurnOpenCode))
	return out, nil
}

// leadTaskMessage is a task as the lead reads it: the user's own words, and
// what is expected of it — the lead does no work itself, so it hands the task
// to one agent or splits it across several.
func leadTaskMessage(t memory.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "A task from my Tasks tab (%s), for you to hand out: give it to an agent, or split it across several if it divides into parts that can go ahead side by side.\n\n", t.ID)
	b.WriteString(t.Goal)
	if t.Detail != "" && t.Detail != t.Goal {
		b.WriteString("\n\n")
		b.WriteString(t.Detail)
	}
	return b.String()
}
