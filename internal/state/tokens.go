package state

import (
	"context"
	"strings"
	"time"

	"agentbox/internal/api"
)

// The token ledger (D83): what every chat spent, turn by turn, kept after the
// agent is gone. The chat writes it as turns end (internal/chat); nothing
// else writes to it and nothing ever changes a row.

// Token kinds: what a ledger row's tokens were spent on.
const (
	// TokensTurn is a turn somebody started: a message, or a notice waking
	// the chat.
	TokensTurn = api.TokensTurn
	// TokensBackground is work a session did between turns by itself, like
	// answering a background task that finished. The adapter reports its cost
	// but not its tokens, so these rows carry a cost and no tokens.
	TokensBackground = api.TokensBackground
	// TokensCompaction is the hidden prompt that summarises a project's chat
	// before its session rolls over (D73).
	TokensCompaction = api.TokensCompaction
	// TokensConsolidation is distilling a project's events into memories
	// (D76, D78).
	TokensConsolidation = api.TokensConsolidation
)

// TokenRow is one line of the ledger: what one model spent in one turn.
type TokenRow struct {
	Project string
	Agent   string
	AI      string // claude, codex or opencode
	Session string // the AI tool's session id
	Turn    string // groups the rows of one turn
	Kind    string
	Model   string // as the adapter named it; "" when it named none
	At      time.Time

	Input      int64 // not read from the cache
	Output     int64
	CacheRead  int64
	CacheWrite int64
	// CostUSD is the adapter's estimate at API prices. A turn's whole cost is
	// on one of its rows, so it adds up by agent but not by model.
	CostUSD float64
	// Context is how many tokens the session's context held when the turn
	// ended, as the tool last reported it; 0 when it hadn't.
	Context int64
}

// Total is every token the row counts.
func (r TokenRow) Total() int64 { return r.Input + r.Output + r.CacheRead + r.CacheWrite }

// AddTokenRows writes a turn's rows together.
func (s *Store) AddTokenRows(ctx context.Context, rows []TokenRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO token_usage
			(project, agent, ai, session_id, turn, kind, model, at,
			 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, context_tokens)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Project, r.Agent, r.AI, r.Session, r.Turn, r.Kind, r.Model, r.At.UnixMilli(),
			r.Input, r.Output, r.CacheRead, r.CacheWrite, r.CostUSD, r.Context); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TokenFilter narrows the ledger. Every field is optional.
type TokenFilter struct {
	Project string // "" for every project
	Agent   string // "" for every agent of Project; ignored without one
	Since   time.Time
	Until   time.Time
}

func (f TokenFilter) where() (string, []any) {
	var clauses []string
	var args []any
	if f.Project != "" {
		clauses = append(clauses, "project = ?")
		args = append(args, f.Project)
		if f.Agent != "" {
			clauses = append(clauses, "agent = ?")
			args = append(args, f.Agent)
		}
	}
	if !f.Since.IsZero() {
		clauses = append(clauses, "at >= ?")
		args = append(args, f.Since.UnixMilli())
	}
	if !f.Until.IsZero() {
		clauses = append(clauses, "at < ?")
		args = append(args, f.Until.UnixMilli())
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// TokenTotal is the ledger summed for one agent on one model.
type TokenTotal struct {
	Project, Agent, AI, Model            string
	Turns                                int64 // turns, background results and hidden prompts
	Input, Output, CacheRead, CacheWrite int64
	CostUSD                              float64
	MaxContext                           int64 // the fullest the context was seen at a turn's end
	LastAt                               time.Time
}

// TokenTotals sums the ledger by agent and model, in agent order.
func (s *Store) TokenTotals(ctx context.Context, f TokenFilter) ([]TokenTotal, error) {
	where, args := f.where()
	rows, err := s.db.QueryContext(ctx, `SELECT project, agent, MAX(ai), model, COUNT(DISTINCT turn),
		SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens),
		SUM(cost_usd), MAX(context_tokens), MAX(at)
		FROM token_usage`+where+` GROUP BY project, agent, model ORDER BY project, agent, model`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TokenTotal
	for rows.Next() {
		var t TokenTotal
		var last int64
		if err := rows.Scan(&t.Project, &t.Agent, &t.AI, &t.Model, &t.Turns, &t.Input, &t.Output,
			&t.CacheRead, &t.CacheWrite, &t.CostUSD, &t.MaxContext, &last); err != nil {
			return nil, err
		}
		t.LastAt = time.UnixMilli(last)
		out = append(out, t)
	}
	return out, rows.Err()
}

// TokenRows lists the ledger itself, newest first, at most limit rows.
func (s *Store) TokenRows(ctx context.Context, f TokenFilter, limit int) ([]TokenRow, error) {
	where, args := f.where()
	rows, err := s.db.QueryContext(ctx, `SELECT project, agent, ai, session_id, turn, kind, model, at,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, context_tokens
		FROM token_usage`+where+` ORDER BY at DESC, id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TokenRow
	for rows.Next() {
		var r TokenRow
		var at int64
		if err := rows.Scan(&r.Project, &r.Agent, &r.AI, &r.Session, &r.Turn, &r.Kind, &r.Model, &at,
			&r.Input, &r.Output, &r.CacheRead, &r.CacheWrite, &r.CostUSD, &r.Context); err != nil {
			return nil, err
		}
		r.At = time.UnixMilli(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// TokenBucket is the ledger summed over one stretch of time.
type TokenBucket struct {
	Start                                time.Time
	Input, Output, CacheRead, CacheWrite int64
	CostUSD                              float64
}

// TokenBuckets sums the ledger into buckets width long, oldest first. A
// stretch nothing was spent in has no bucket.
func (s *Store) TokenBuckets(ctx context.Context, f TokenFilter, width time.Duration) ([]TokenBucket, error) {
	w := width.Milliseconds()
	if w <= 0 {
		w = time.Hour.Milliseconds()
	}
	where, args := f.where()
	rows, err := s.db.QueryContext(ctx, `SELECT (at / ?) * ? AS start,
		SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens), SUM(cost_usd)
		FROM token_usage`+where+` GROUP BY start ORDER BY start`, append([]any{w, w}, args...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TokenBucket
	for rows.Next() {
		var b TokenBucket
		var start int64
		if err := rows.Scan(&start, &b.Input, &b.Output, &b.CacheRead, &b.CacheWrite, &b.CostUSD); err != nil {
			return nil, err
		}
		b.Start = time.UnixMilli(start)
		out = append(out, b)
	}
	return out, rows.Err()
}
