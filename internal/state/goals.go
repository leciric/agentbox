package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"agentbox/internal/api"
)

// AgentGoal is an agent's goal as the database keeps it (agent_goals): the
// experimental agent goals' one row per agent.
type AgentGoal struct {
	Project, Agent string
	api.AgentGoal
}

const goalColumns = `project, agent, objective, source, status, checks, checked, last_unmet, budget, continuations, updated_at`

func scanGoal(row interface{ Scan(...any) error }) (AgentGoal, error) {
	var g AgentGoal
	var checks string
	var at int64
	if err := row.Scan(&g.Project, &g.Agent, &g.Objective, &g.Source, &g.Status, &checks, &g.Checked, &g.LastUnmet, &g.Budget, &g.Continuations, &at); err != nil {
		return AgentGoal{}, err
	}
	// A list that doesn't read is no checks: the goal still shows.
	_ = json.Unmarshal([]byte(checks), &g.Checks)
	g.UpdatedAt = time.UnixMilli(at)
	return g, nil
}

// SetAgentGoal stores an agent's goal, replacing the one it had.
func (s *Store) SetAgentGoal(ctx context.Context, g AgentGoal) error {
	checks := []byte("[]")
	if len(g.Checks) > 0 {
		var err error
		if checks, err = json.Marshal(g.Checks); err != nil {
			return err
		}
	}
	if g.UpdatedAt.IsZero() {
		g.UpdatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_goals (`+goalColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, agent) DO UPDATE SET objective = excluded.objective, source = excluded.source,
			status = excluded.status, checks = excluded.checks, checked = excluded.checked,
			last_unmet = excluded.last_unmet, budget = excluded.budget,
			continuations = excluded.continuations, updated_at = excluded.updated_at`,
		g.Project, g.Agent, g.Objective, g.Source, g.Status, string(checks), g.Checked, g.LastUnmet, g.Budget, g.Continuations, g.UpdatedAt.UnixMilli())
	return err
}

// AgentGoalOf is an agent's goal; ok is false when it has none.
func (s *Store) AgentGoalOf(ctx context.Context, project, agent string) (g AgentGoal, ok bool, err error) {
	g, err = scanGoal(s.db.QueryRowContext(ctx, `SELECT `+goalColumns+` FROM agent_goals WHERE project = ? AND agent = ?`, project, agent))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentGoal{}, false, nil
	}
	return g, err == nil, err
}

// AgentGoals is every agent's goal, by ref (project/agent).
func (s *Store) AgentGoals(ctx context.Context) (map[string]AgentGoal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+goalColumns+` FROM agent_goals`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]AgentGoal{}
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out[g.Project+"/"+g.Agent] = g
	}
	return out, rows.Err()
}

// DeleteAgentGoal forgets an agent's goal.
func (s *Store) DeleteAgentGoal(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_goals WHERE project = ? AND agent = ?`, project, agent)
	return err
}
