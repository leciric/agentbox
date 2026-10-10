package daemon

import (
	"context"
	"fmt"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// Agents made for a task of the Tasks tab (taskroute.go).

// taskForAgent fills in a create request made for a task of the plan: the
// task must be open and nobody's, and its words are the agent's task and
// title unless the request has its own.
func (s *Server) taskForAgent(ctx context.Context, req *api.CreateAgentRequest) error {
	t, err := s.memory().Task(ctx, req.Project, req.TaskID)
	if err != nil {
		return err
	}
	if !t.Open() {
		return fmt.Errorf("task %s is %s: only an open task can be given to an agent", t.ID, t.Status)
	}
	if !t.LeadQueuedAt.IsZero() {
		return fmt.Errorf("task %s is with the lead", t.ID)
	}
	if t.Agent != "" {
		return fmt.Errorf("task %s is already %s's", t.ID, t.Agent)
	}
	if strings.TrimSpace(req.Task) == "" {
		req.Task = t.Goal
		if t.Detail != "" && t.Detail != t.Goal {
			req.Task += "\n\n" + t.Detail
		}
	}
	if strings.TrimSpace(req.Title) == "" {
		req.Title = titleFromTask(t.Goal)
	}
	return nil
}

// titleFromTask makes an agent's title from a task's words: its first line,
// cut to fit agent.CleanTitle's 80 characters at a word.
func titleFromTask(goal string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(goal), "\n")
	line = strings.Join(strings.Fields(line), " ")
	if len([]rune(line)) <= 80 {
		return line
	}
	r := []rune(line)[:79]
	if i := strings.LastIndex(string(r), " "); i > 40 {
		return string(r)[:i] + "…"
	}
	return string(r) + "…"
}

// assignTask gives a task of the plan to an agent. A failure only loses the
// link, which the task tab shows, so it's logged rather than failing a create.
func (s *Server) assignTask(ctx context.Context, project, id, agentName string) {
	if _, err := s.memory().UpdateTask(ctx, project, id, memory.TaskPatch{Agent: &agentName}); err != nil {
		s.logf("memory: giving task %s to %s/%s: %v", id, project, agentName, err)
	}
}

// releaseQueuedTasks hands back the open tasks a queued agent that didn't
// start was given (leftoverqueue.go), so they can be started again.
func (s *Server) releaseQueuedTasks(ctx context.Context, a state.Agent) {
	open, err := s.memory().Tasks(ctx, a.Project, memory.TaskFilter{Agent: a.Name, OpenOnly: true})
	if err != nil {
		return
	}
	nobody := ""
	for _, t := range open {
		if _, err := s.memory().UpdateTask(ctx, a.Project, t.ID, memory.TaskPatch{Agent: &nobody}); err != nil {
			s.logf("memory: handing back task %s: %v", t.ID, err)
		}
	}
}

// deleteTask takes one of the user's tasks off their list. An agent running
// on it keeps running; only the row goes.
func (s *Server) deleteTask(ctx context.Context, project, id string) error {
	if _, err := s.memory().Task(ctx, project, id); err != nil {
		return err
	}
	return s.memory().DeleteTask(ctx, project, id)
}
