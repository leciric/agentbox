package state

import (
	"context"
	"time"
)

// What the weekly recap (GET /v1/recap) reads from the database: when the
// agents worked, from the token ledger, and when the user was there to see
// it, from what they typed to their leads and the verdicts they gave.

// TurnSpan is one turn an agent worked: Start is End less the time it spent
// generating, the ledger's generation_ms.
type TurnSpan struct {
	Project, Agent string
	Start, End     time.Time
}

// Duration is how long the turn worked.
func (t TurnSpan) Duration() time.Duration { return t.End.Sub(t.Start) }

// TurnSpans is every turn a worker (not a lead) ended in [since, until),
// oldest first. A turn's rows, one per model, share its end and its time; a
// row from before turns had ids is a turn of its own.
func (s *Store) TurnSpans(ctx context.Context, since, until time.Time) ([]TurnSpan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, agent, MAX(at), MAX(generation_ms) FROM token_usage
		WHERE kind = ? AND agent != ? AND at >= ? AND at < ?
		GROUP BY project, agent, CASE WHEN turn = '' THEN CAST(id AS TEXT) ELSE turn END
		ORDER BY MAX(at)`, TokensTurn, LeadName, since.UnixMilli(), until.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TurnSpan
	for rows.Next() {
		var t TurnSpan
		var at, ms int64
		if err := rows.Scan(&t.Project, &t.Agent, &at, &ms); err != nil {
			return nil, err
		}
		t.End = time.UnixMilli(at)
		t.Start = t.End.Add(-time.Duration(ms) * time.Millisecond)
		out = append(out, t)
	}
	return out, rows.Err()
}

// TurnDays is every day, as offset from UTC moves it, on which a worker
// ended a turn since then: days since the Unix epoch, ascending.
func (s *Store) TurnDays(ctx context.Context, since time.Time, offset time.Duration) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT (at + ?) / 86400000 AS day FROM token_usage
		WHERE kind = ? AND agent != ? AND at >= ? ORDER BY day`,
		offset.Milliseconds(), TokensTurn, LeadName, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var d int64
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UserPresence is when the user was at AgentBox in [since, until), as the
// database can tell: each message they sent a lead (a project's chat or
// Home's), and each verdict they gave an agent's work. A message to a worker
// isn't counted, since the lead's tell_agent writes the same item.
func (s *Store) UserPresence(ctx context.Context, since, until time.Time) ([]time.Time, error) {
	var out []time.Time
	rows, err := s.db.QueryContext(ctx, `SELECT json_extract(data, '$.createdAt') FROM chat_items
		WHERE agent = ? AND json_extract(data, '$.kind') = 'user'
			AND json_extract(data, '$.woken') IS NOT 1 AND json_extract(data, '$.hidden') IS NOT 1
			AND json_type(data, '$.projectMessage') IS NULL AND json_type(data, '$.scheduledRun') IS NULL`, LeadName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var at *string
		if err := rows.Scan(&at); err != nil {
			return nil, err
		}
		if at == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, *at)
		if err != nil || t.Before(since) || !t.Before(until) {
			continue
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	verdicts, err := s.db.QueryContext(ctx, `SELECT at FROM agent_reviews WHERE at >= ? AND at < ?`, since.UnixMilli(), until.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer func() { _ = verdicts.Close() }()
	for verdicts.Next() {
		var at int64
		if err := verdicts.Scan(&at); err != nil {
			return nil, err
		}
		out = append(out, time.UnixMilli(at))
	}
	return out, verdicts.Err()
}
