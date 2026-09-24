package state

import (
	"context"
	"time"
)

// AgentEvent is one thing an agent of a project reported — it was created, it
// finished, it asked, its question was answered — kept so the app's threads
// survive a reload. Like a chat item, the row holds what it is about (project,
// agent, when) and the rest as JSON that package daemon decides the shape of:
// what the app renders is api.AgentEvent, and this table shouldn't have to
// change every time a field is added to it.
type AgentEvent struct {
	ID        string
	Project   string
	Agent     string
	CreatedAt time.Time
	Data      []byte // JSON
}

// keepAgentEvents is how many of a project's events are served. A thread is
// for reading the last few things an agent did, not its whole history, and
// the rail holds all of them in memory.
const keepAgentEvents = 500

func (s *Store) AddAgentEvent(ctx context.Context, ev AgentEvent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_events (id, project, agent, created_at, data) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET data = excluded.data`,
		ev.ID, ev.Project, ev.Agent, ev.CreatedAt.UnixMilli(), string(ev.Data))
	return err
}

// AgentEvents returns a project's events, newest first, at most keepAgentEvents
// of them.
func (s *Store) AgentEvents(ctx context.Context, project string) ([]AgentEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project, agent, created_at, data FROM agent_events WHERE project = ?
		ORDER BY created_at DESC, rowid DESC LIMIT ?`, project, keepAgentEvents)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []AgentEvent
	for rows.Next() {
		var ev AgentEvent
		var created int64
		var data string
		if err := rows.Scan(&ev.ID, &ev.Project, &ev.Agent, &created, &data); err != nil {
			return nil, err
		}
		ev.CreatedAt, ev.Data = time.UnixMilli(created), []byte(data)
		events = append(events, ev)
	}
	return events, rows.Err()
}

// removeAgentEvents drops an agent's events with the agent, the way its
// conversation goes: a thread about an agent that no longer exists is a row
// in the rail with nothing behind it.
func (s *Store) removeAgentEvents(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_events WHERE project = ? AND agent = ?`, project, agent)
	return err
}
