package state

import (
	"context"
	"time"
)

// A Claude account's usage limits, as Anthropic last reported them (D85). The
// chat relays each reading and the daemon keeps the newest per account: a
// limit belongs to the account, which every agent on it shares.

// ClaudeLimitReading is the newest reading for one account. Reading is the
// adapter's JSON (acp.RateLimit), At when it arrived.
type ClaudeLimitReading struct {
	Account string
	Reading []byte
	At      time.Time
}

// SetClaudeLimit replaces an account's reading with a newer one.
func (s *Store) SetClaudeLimit(ctx context.Context, account string, reading []byte, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO claude_limits (account, reading, at) VALUES (?, ?, ?)
		ON CONFLICT (account) DO UPDATE SET reading = excluded.reading, at = excluded.at
		WHERE excluded.at >= claude_limits.at`, account, string(reading), at.UnixMilli())
	return err
}

// ClaudeLimits lists every account's newest reading, by account.
func (s *Store) ClaudeLimits(ctx context.Context) ([]ClaudeLimitReading, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account, reading, at FROM claude_limits ORDER BY account`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ClaudeLimitReading
	for rows.Next() {
		var r ClaudeLimitReading
		var reading string
		var at int64
		if err := rows.Scan(&r.Account, &reading, &at); err != nil {
			return nil, err
		}
		r.Reading, r.At = []byte(reading), time.UnixMilli(at)
		out = append(out, r)
	}
	return out, rows.Err()
}
