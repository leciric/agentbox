package daemon

import (
	"context"

	"agentbox/internal/memory"
)

// A task handed to an agent is done when the agent's pull request merges:
// that is the work landing, so the Tasks tab moves it to Done as implemented,
// with the pull request it landed in. The daemon hears of a merge three ways —
// merged from AgentBox, seen by the pull request watch, or found by the sweep
// that removes finished agents — and each calls tasksImplemented, which is a
// no-op the second and third time because only open tasks are touched. The
// sweep matters most: it destroys the agent, and a task whose agent is gone
// would otherwise drop back to the backlog.

// tasksImplemented closes the open tasks an agent was given as done by a
// merged pull request.
func (s *Server) tasksImplemented(ctx context.Context, project, agentName, url string, number int) {
	if agentName == "" {
		return
	}
	open, err := s.memory().Tasks(ctx, project, memory.TaskFilter{Agent: agentName, OpenOnly: true})
	if err != nil {
		s.logf("memory: tasks of %s/%s: %v", project, agentName, err)
		return
	}
	for _, t := range open {
		out, err := s.memory().UpdateTask(ctx, project, t.ID, memory.TaskPatch{
			Status: ptr(memory.TaskDone), Pull: &memory.TaskPull{URL: url, Number: number},
		})
		if err != nil {
			s.logf("memory: closing task %s as implemented by #%d: %v", t.ID, number, err)
			continue
		}
		s.captureTaskStatus(ctx, out, t.Status)
	}
}
