package state

import (
	"context"
	"time"
)

// What a paired AgentBox did on this machine (internal/daemon/shareactivity.go)
// and what it spent here: the tokens of its agents and of the chat turns its
// messages started, and the minutes its agents ran.

// ShareActivity is one thing a peer did.
type ShareActivity struct {
	ID      int64
	Peer    string
	At      time.Time
	Kind    string
	Project string
	Agent   string
	Detail  string
}

// MaxShareActivity is how many of a peer's activities are kept: older ones
// go as new ones come.
const MaxShareActivity = 500

// AddShareActivity records what a peer did, and forgets its oldest past
// MaxShareActivity.
func (s *Store) AddShareActivity(ctx context.Context, a ShareActivity) (ShareActivity, error) {
	if a.At.IsZero() {
		a.At = time.Now()
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO share_activity (peer_id, at, kind, project, agent, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		a.Peer, a.At.UnixMilli(), a.Kind, a.Project, a.Agent, a.Detail)
	if err != nil {
		return ShareActivity{}, err
	}
	a.ID, _ = res.LastInsertId()
	_, err = s.db.ExecContext(ctx, `DELETE FROM share_activity WHERE peer_id = ? AND id <= ?`, a.Peer, a.ID-MaxShareActivity)
	return a, err
}

// ShareActivities lists a peer's activity, the newest first, at most limit.
func (s *Store) ShareActivities(ctx context.Context, peer string, limit int) ([]ShareActivity, error) {
	if limit <= 0 || limit > MaxShareActivity {
		limit = MaxShareActivity
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, peer_id, at, kind, project, agent, detail FROM share_activity
		WHERE peer_id = ? ORDER BY id DESC LIMIT ?`, peer, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ShareActivity
	for rows.Next() {
		var a ShareActivity
		var at int64
		if err := rows.Scan(&a.ID, &a.Peer, &at, &a.Kind, &a.Project, &a.Agent, &a.Detail); err != nil {
			return nil, err
		}
		a.At = time.UnixMilli(at)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SharePeerUsage is what a peer spent on this machine.
type SharePeerUsage struct {
	// Tokens are every token (input, output and cache) of its agents' turns
	// and of the chat turns its messages started; CostUSD what the AI tools
	// said those cost.
	Tokens  int64
	CostUSD float64
	// RunSeconds is how long its agents' machines ran.
	RunSeconds int64
}

// SharePeerUsage totals what a peer spent: its agents' tokens and run time,
// and the tokens of the turns its messages started in a chat (a turn is the
// id of the user message that heads it, whose author is the peer).
func (s *Store) SharePeerUsage(ctx context.Context, peer string) (SharePeerUsage, error) {
	var u SharePeerUsage
	err := s.db.QueryRowContext(ctx, `SELECT
			coalesce(sum(t.input_tokens + t.output_tokens + t.cache_read_tokens + t.cache_write_tokens), 0),
			coalesce(sum(t.cost_usd), 0)
		FROM token_usage t
		WHERE EXISTS (SELECT 1 FROM share_peer_agents pa WHERE pa.peer_id = ? AND pa.project = t.project AND pa.agent = t.agent)
			OR (t.turn != '' AND EXISTS (SELECT 1 FROM chat_items c WHERE c.project = t.project AND c.agent = t.agent AND c.id = t.turn AND c.author = ?))`,
		peer, peer).Scan(&u.Tokens, &u.CostUSD)
	if err != nil {
		return u, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT coalesce(sum(run_seconds), 0) FROM share_peer_agents WHERE peer_id = ?`, peer).Scan(&u.RunSeconds)
	return u, err
}

// AddShareRunSeconds adds to how long one of a peer's agents has run.
func (s *Store) AddShareRunSeconds(ctx context.Context, project, agent string, seconds int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE share_peer_agents SET run_seconds = run_seconds + ? WHERE project = ? AND agent = ?`, seconds, project, agent)
	return err
}
