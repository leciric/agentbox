package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agentbox/internal/api"
)

// An agent's task, from the moment its row is added until its AI tool has
// it. The daemon hands an agent its task as the first message of its chat
// once its machine is made, which takes minutes; until then the task lived
// only in the create job's memory, and a daemon killed or a VM going down in
// between lost it for good: the agent came up idle, and the message that
// woke it ("carry on") was all it ever heard (agent-861 and agent-864, 20
// and 21 Nov 2026). Kept here instead, it survives: the next daemon sends it
// (daemon/agenttasks.go), and the first prompt any of the agent's sessions
// is sent carries it if nothing has yet, which is when it is deleted
// (package chat, tasks.go).

// AgentTask is an agent's task that hasn't reached its AI tool yet.
type AgentTask struct {
	Project, Agent string
	// Text is the agent's first message as the daemon sends it: its task,
	// and its goal's brief after it when it was given one.
	Text string
	// Goal is the goal it was given with its task, if any, for a daemon
	// that sends the task after the one that made the agent stopped before
	// setting it.
	Goal *api.GoalSpec
	// ByLead says a project's chat asked for the agent, and so waits to hear
	// how its task went.
	ByLead    bool
	CreatedAt time.Time
}

// Ref is the agent's project/name.
func (t AgentTask) Ref() string { return t.Project + "/" + t.Agent }

// SaveAgentTask keeps an agent's task until its AI tool has it, replacing
// any it had.
func (s *Store) SaveAgentTask(ctx context.Context, t AgentTask) error {
	if t.Text == "" {
		return fmt.Errorf("%s's task is empty", t.Ref())
	}
	goal := ""
	if t.Goal != nil {
		b, err := json.Marshal(t.Goal)
		if err != nil {
			return err
		}
		goal = string(b)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_tasks (project, agent, text, goal, by_lead, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, agent) DO UPDATE SET text = excluded.text, goal = excluded.goal,
			by_lead = excluded.by_lead, created_at = excluded.created_at`,
		t.Project, t.Agent, t.Text, goal, t.ByLead, t.CreatedAt.UnixMilli())
	return err
}

// AgentTaskOf is an agent's task that hasn't reached its AI tool, if it has
// one.
func (s *Store) AgentTaskOf(ctx context.Context, project, agent string) (AgentTask, bool, error) {
	tasks, err := s.queryAgentTasks(ctx, `WHERE project = ? AND agent = ?`, project, agent)
	if err != nil || len(tasks) == 0 {
		return AgentTask{}, false, err
	}
	return tasks[0], true, nil
}

// AgentTasks lists every agent's task that hasn't reached its AI tool,
// oldest first.
func (s *Store) AgentTasks(ctx context.Context) ([]AgentTask, error) {
	return s.queryAgentTasks(ctx, `ORDER BY created_at, project, agent`)
}

// DeleteAgentTask forgets an agent's task: its AI tool has it, or the agent
// is gone.
func (s *Store) DeleteAgentTask(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_tasks WHERE project = ? AND agent = ?`, project, agent)
	return err
}

func (s *Store) queryAgentTasks(ctx context.Context, clause string, args ...any) ([]AgentTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, agent, text, goal, by_lead, created_at FROM agent_tasks `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AgentTask
	for rows.Next() {
		var t AgentTask
		var goal string
		var at int64
		if err := rows.Scan(&t.Project, &t.Agent, &t.Text, &goal, &t.ByLead, &at); err != nil {
			return nil, err
		}
		if goal != "" {
			var g api.GoalSpec
			if err := json.Unmarshal([]byte(goal), &g); err != nil {
				return nil, fmt.Errorf("%s's task's goal: %w", t.Ref(), err)
			}
			t.Goal = &g
		}
		t.CreatedAt = time.UnixMilli(at)
		out = append(out, t)
	}
	return out, rows.Err()
}

// agentTaskText is an agent's task that never reached its AI tool, for
// ArchiveAgent: "" for none.
func agentTaskText(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, project, agent string) (string, error) {
	var text string
	err := q.QueryRowContext(ctx, `SELECT text FROM agent_tasks WHERE project = ? AND agent = ?`, project, agent).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return text, err
}
