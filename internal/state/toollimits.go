package state

import (
	"context"
	"time"
)

// An account's usage limits for a tool whose adapter doesn't relay them the
// way claude-agent-acp does (Codex). The daemon reads them off the tool and
// keeps the newest per tool and account, as claude_limits does for Claude.

// ToolLimitReading is the newest reading for one tool's account. Reading is
// JSON the daemon wrote, At when it was read.
type ToolLimitReading struct {
	Tool    string
	Account string
	Reading []byte
	At      time.Time
}

// SetToolLimit replaces an account's reading with a newer one.
func (s *Store) SetToolLimit(ctx context.Context, tool, account string, reading []byte, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tool_limits (tool, account, reading, at) VALUES (?, ?, ?, ?)
		ON CONFLICT (tool, account) DO UPDATE SET reading = excluded.reading, at = excluded.at
		WHERE excluded.at >= tool_limits.at`, tool, account, string(reading), at.UnixMilli())
	return err
}

// ToolLimits lists a tool's newest reading per account, by account.
func (s *Store) ToolLimits(ctx context.Context, tool string) ([]ToolLimitReading, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account, reading, at FROM tool_limits WHERE tool = ? ORDER BY account`, tool)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ToolLimitReading
	for rows.Next() {
		r := ToolLimitReading{Tool: tool}
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
