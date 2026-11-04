package state

import (
	"context"
	"time"
)

// SeeLeadSession records that a session of project's chat was on screen at
// at, and returns when it was last seen: at, or a later time already
// recorded, since a window reporting late mustn't make a message read on
// the phone unread again.
func (s *Store) SeeLeadSession(ctx context.Context, project, session string, at time.Time) (time.Time, error) {
	var ms int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO lead_session_seen (project, session, seen_at) VALUES (?, ?, ?)
		ON CONFLICT (project, session) DO UPDATE SET seen_at = max(seen_at, excluded.seen_at)
		RETURNING seen_at`,
		project, session, at.UnixMilli()).Scan(&ms)
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(ms), nil
}

// LeadSessionsSeen is when each of project's sessions was last seen, by id;
// one never seen isn't in it.
func (s *Store) LeadSessionsSeen(ctx context.Context, project string) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session, seen_at FROM lead_session_seen WHERE project = ?`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var ms int64
		if err := rows.Scan(&id, &ms); err != nil {
			return nil, err
		}
		out[id] = time.UnixMilli(ms)
	}
	return out, rows.Err()
}
