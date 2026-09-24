package memory

import (
	"context"
	"strings"
	"time"
)

// AppendEvent records one thing that happened. Events are append-only: there
// is no update and no delete, because the point of raw history is that nothing
// rewrote it. An event with no id gets one, and an event with no time happened
// now.
func (s *Store) AppendEvent(ctx context.Context, e Event) (Event, error) {
	if err := requireProject(e.Project); err != nil {
		return Event{}, err
	}
	e.Type = strings.TrimSpace(e.Type)
	if e.Type == "" {
		return Event{}, errNoType
	}
	payload, err := jsonDocument("the event's payload", e.Payload)
	if err != nil {
		return Event{}, err
	}
	e.Payload = []byte(payload)
	if e.ID == "" {
		e.ID = newID("ev")
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	e.At = stamp(e.At)
	var artifact any
	if e.ArtifactID != "" {
		artifact = e.ArtifactID
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO events (id, project, agent, session, at, type, payload, artifact_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Project, e.Agent, e.Session, e.At.UnixMilli(), e.Type, payload, artifact)
	if err != nil {
		return Event{}, err
	}
	return e, nil
}

// Events are a project's, newest first, narrowed by the filter.
func (s *Store) Events(ctx context.Context, project string, f EventFilter) ([]Event, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	where := []string{"events.project = ?"}
	args := []any{project}
	if f.Agent != "" {
		where = append(where, "events.agent = ?")
		args = append(args, f.Agent)
	}
	if len(f.Types) > 0 {
		where = append(where, "events.type IN ("+placeholders(len(f.Types))+")")
		for _, t := range f.Types {
			args = append(args, t)
		}
	}
	if !f.Since.IsZero() {
		where = append(where, "events.at >= ?")
		args = append(args, f.Since.UnixMilli())
	}
	// rowid breaks ties between events appended in the same millisecond, so
	// two events of one turn always read back in the order they happened.
	args = append(args, limitOf(f.Limit))
	return s.queryEvents(ctx, `WHERE `+strings.Join(where, " AND ")+` ORDER BY events.at DESC, events.rowid DESC LIMIT ?`, args...)
}

// Event is one event by id.
func (s *Store) Event(ctx context.Context, project, id string) (Event, error) {
	events, err := s.queryEvents(ctx, `WHERE events.project = ? AND events.id = ?`, project, id)
	if err != nil {
		return Event{}, err
	}
	if len(events) == 0 {
		return Event{}, notFound("event", id)
	}
	return events[0], nil
}

// The columns are qualified, because Search joins this table with an FTS5
// index that has a type and a payload column of its own.
const eventColumns = `events.id, events.project, events.agent, events.session, events.at, events.type,
	events.payload, events.artifact_id`

func (s *Store) queryEvents(ctx context.Context, clause string, args ...any) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+eventColumns+` FROM events `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func scanEvent(rows scanner) (Event, error) {
	var e Event
	var at int64
	var payload string
	var artifact *string
	if err := rows.Scan(&e.ID, &e.Project, &e.Agent, &e.Session, &at, &e.Type, &payload, &artifact); err != nil {
		return Event{}, err
	}
	e.At = attime(at)
	e.Payload = []byte(payload)
	if artifact != nil {
		e.ArtifactID = *artifact
	}
	return e, nil
}

// scanner is what *sql.Rows and *sql.Row both are, so one scan function serves
// a listing and a single lookup.
type scanner interface{ Scan(dest ...any) error }

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
