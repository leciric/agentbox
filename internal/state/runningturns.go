package state

import (
	"context"
	"time"
)

// Kinds of chat a RunningTurn belongs to.
const (
	TurnOfAgent = "agent" // an agent's, which runs in its machine
	TurnOfLead  = "lead"  // a project's chat
	TurnOfHome  = "home"  // the Home chat, across every project
)

// RunningTurn is a chat turn that had started and not yet ended, as the
// store has it: kept from the moment the turn starts until it ends on its
// own, so a daemon that stopped in the middle finds it on its next start.
type RunningTurn struct {
	Project string
	Agent   string
	Kind    string // TurnOfAgent, TurnOfLead or TurnOfHome
	// Turn is the ID of the chat item heading the turn: the user's message,
	// or the notice the chat was reacting to.
	Turn      string
	Prompt    string
	StartedAt time.Time
	// Resumes is how many times AgentBox has already carried this turn on
	// after a restart, counting through the turns that did it.
	Resumes int
}

// Ref is the agent's ref, project/name.
func (t RunningTurn) Ref() string { return t.Project + "/" + t.Agent }

// TurnKind says which kind of chat an agent's turns are.
func TurnKind(a Agent) string {
	switch {
	case a.IsHome():
		return TurnOfHome
	case a.IsLead():
		return TurnOfLead
	default:
		return TurnOfAgent
	}
}

// SaveRunningTurn records that a chat's turn runs, replacing whatever turn
// it had before: a chat runs one at a time.
func (s *Store) SaveRunningTurn(ctx context.Context, t RunningTurn) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO running_turns (project, agent, kind, turn, prompt, started_at, resumes)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, agent) DO UPDATE SET kind = excluded.kind, turn = excluded.turn,
			prompt = excluded.prompt, started_at = excluded.started_at, resumes = excluded.resumes`,
		t.Project, t.Agent, t.Kind, t.Turn, t.Prompt, t.StartedAt.UnixMilli(), t.Resumes)
	return err
}

// EndRunningTurn forgets a chat's running turn, when it is still the one
// named: a later turn's row is left alone.
func (s *Store) EndRunningTurn(ctx context.Context, project, agent, turn string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM running_turns WHERE project = ? AND agent = ? AND turn = ?`, project, agent, turn)
	return err
}

// DropRunningTurn forgets a chat's running turn, whichever it is.
func (s *Store) DropRunningTurn(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM running_turns WHERE project = ? AND agent = ?`, project, agent)
	return err
}

// RunningTurns lists every turn recorded as running, oldest first.
func (s *Store) RunningTurns(ctx context.Context) ([]RunningTurn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, agent, kind, turn, prompt, started_at, resumes FROM running_turns ORDER BY started_at, project, agent`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RunningTurn
	for rows.Next() {
		var t RunningTurn
		var started int64
		if err := rows.Scan(&t.Project, &t.Agent, &t.Kind, &t.Turn, &t.Prompt, &started, &t.Resumes); err != nil {
			return nil, err
		}
		t.StartedAt = time.UnixMilli(started)
		out = append(out, t)
	}
	return out, rows.Err()
}
