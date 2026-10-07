package state

import (
	"context"
	"encoding/json"
)

// UsageEvent is one anonymous usage event waiting to be sent.
type UsageEvent struct {
	Seq   int64           `json:"-"`
	ID    string          `json:"id"`
	Day   string          `json:"day"` // UTC, YYYY-MM-DD
	Name  string          `json:"name"`
	Props json.RawMessage `json:"props"`
}

// MaxUsageEvents is the most events kept unsent: past it the oldest go, so a
// server that is down for weeks can't grow state.db without end.
const MaxUsageEvents = 5000

// AddUsageEvent keeps e until it is sent, dropping the oldest past
// MaxUsageEvents.
func (s *Store) AddUsageEvent(ctx context.Context, e UsageEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_events (id, day, name, props) VALUES (?, ?, ?, ?)`,
		e.ID, e.Day, e.Name, string(e.Props)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_events WHERE seq <= (SELECT max(seq) FROM usage_events) - ?`, MaxUsageEvents); err != nil {
		return err
	}
	return tx.Commit()
}

// UsageEvents is up to limit of the events not sent yet, oldest first.
func (s *Store) UsageEvents(ctx context.Context, limit int) ([]UsageEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, id, day, name, props FROM usage_events ORDER BY seq LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UsageEvent
	for rows.Next() {
		var e UsageEvent
		var props string
		if err := rows.Scan(&e.Seq, &e.ID, &e.Day, &e.Name, &props); err != nil {
			return nil, err
		}
		e.Props = json.RawMessage(props)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ForgetUsageEvents deletes the events up to and including seq, or all of
// them when seq is 0, and those of days before before (YYYY-MM-DD) when it
// isn't "": the server takes none that old.
func (s *Store) ForgetUsageEvents(ctx context.Context, seq int64, before string) error {
	var err error
	switch {
	case before != "":
		_, err = s.db.ExecContext(ctx, `DELETE FROM usage_events WHERE day < ?`, before)
	case seq == 0:
		_, err = s.db.ExecContext(ctx, `DELETE FROM usage_events`)
	default:
		_, err = s.db.ExecContext(ctx, `DELETE FROM usage_events WHERE seq <= ?`, seq)
	}
	return err
}
