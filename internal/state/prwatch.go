package state

import (
	"context"
	"time"
)

// PRWatch is what the daemon last saw of one of an agent's pull requests,
// kept so that it speaks up only when something turns bad, not every time it
// looks and finds it still bad.
type PRWatch struct {
	Project string
	Number  int
	Agent   string // the agent whose pull request it is
	AgentID string // which agent of that name: they are never reused, but an ID says so outright
	HeadSHA string // the commit the checks below are about
	// Conflict is whether it conflicted with its base.
	Conflict bool
	// Checks is passing, failing, pending, or "" for none.
	Checks string
	// Review is GitHub's review decision: approved, changes_requested,
	// review_required, or "".
	Review    string
	UpdatedAt time.Time
}

// PRWatches is every pull request the daemon is watching in a project.
func (s *Store) PRWatches(ctx context.Context, project string) ([]PRWatch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, number, agent, agent_id, head_sha, conflict, checks, review, updated_at
		FROM pr_watches WHERE project = ? ORDER BY number`, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PRWatch
	for rows.Next() {
		var w PRWatch
		var updated int64
		if err := rows.Scan(&w.Project, &w.Number, &w.Agent, &w.AgentID, &w.HeadSHA, &w.Conflict, &w.Checks, &w.Review, &updated); err != nil {
			return nil, err
		}
		w.UpdatedAt = time.Unix(updated, 0)
		out = append(out, w)
	}
	return out, rows.Err()
}

// SavePRWatch stores what the daemon now sees of a pull request.
func (s *Store) SavePRWatch(ctx context.Context, w PRWatch) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pr_watches (project, number, agent, agent_id, head_sha, conflict, checks, review, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, number) DO UPDATE SET agent = excluded.agent, agent_id = excluded.agent_id,
			head_sha = excluded.head_sha, conflict = excluded.conflict, checks = excluded.checks,
			review = excluded.review, updated_at = excluded.updated_at`,
		w.Project, w.Number, w.Agent, w.AgentID, w.HeadSHA, w.Conflict, w.Checks, w.Review, w.UpdatedAt.Unix())
	return err
}

// ForgetPRWatch stops watching a pull request: it was merged or closed, or
// watching was turned off.
func (s *Store) ForgetPRWatch(ctx context.Context, project string, number int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pr_watches WHERE project = ? AND number = ?`, project, number)
	return err
}

// ForgetPRWatches stops watching every pull request in a project.
func (s *Store) ForgetPRWatches(ctx context.Context, project string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pr_watches WHERE project = ?`, project)
	return err
}
