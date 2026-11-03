package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MergeWhenGreen is a pull request the user marked "Resolved — merge when CI
// is green": the daemon merges it with Method once its checks pass. Error is
// set when it was given up instead (its checks failed, it conflicts, new
// commits came, GitHub refused the merge): the row then waits for the user to
// dismiss it or try again, and is no longer watched.
type MergeWhenGreen struct {
	Project string
	Number  int
	Agent   string // the agent whose work it is, whose verdict it recorded
	Method  string // merge, squash or rebase
	// HeadSHA is the commit the user resolved: a pull request that moves on
	// from it is new work, not merged unseen.
	HeadSHA string
	// Fingerprint is the review verdict the mark recorded, taken back when it
	// is given up or cancelled.
	Fingerprint string
	Error       string
	At          time.Time
	FailedAt    time.Time
}

// Pending is whether the daemon is still waiting to merge it.
func (m MergeWhenGreen) Pending() bool { return m.Error == "" }

const mergeWhenGreenColumns = `project, number, agent, method, head_sha, fingerprint, error, at, failed_at`

func scanMergeWhenGreen(row interface{ Scan(...any) error }) (MergeWhenGreen, error) {
	var m MergeWhenGreen
	var at, failed int64
	if err := row.Scan(&m.Project, &m.Number, &m.Agent, &m.Method, &m.HeadSHA, &m.Fingerprint, &m.Error, &at, &failed); err != nil {
		return MergeWhenGreen{}, err
	}
	m.At = time.UnixMilli(at)
	if failed != 0 {
		m.FailedAt = time.UnixMilli(failed)
	}
	return m, nil
}

// SetMergeWhenGreen marks a pull request to be merged when it is green,
// replacing any earlier mark on it, a failed one included.
func (s *Store) SetMergeWhenGreen(ctx context.Context, m MergeWhenGreen) error {
	var failed int64
	if !m.FailedAt.IsZero() {
		failed = m.FailedAt.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO pr_merge_when_green (`+mergeWhenGreenColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, number) DO UPDATE SET agent = excluded.agent, method = excluded.method,
			head_sha = excluded.head_sha, fingerprint = excluded.fingerprint, error = excluded.error,
			at = excluded.at, failed_at = excluded.failed_at`,
		m.Project, m.Number, m.Agent, m.Method, m.HeadSHA, m.Fingerprint, m.Error, m.At.UnixMilli(), failed)
	return err
}

// MergesWhenGreen is every pull request of a project marked to merge when
// green, the given-up ones included, by number.
func (s *Store) MergesWhenGreen(ctx context.Context, project string) ([]MergeWhenGreen, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mergeWhenGreenColumns+` FROM pr_merge_when_green WHERE project = ? ORDER BY number`, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MergeWhenGreen
	for rows.Next() {
		m, err := scanMergeWhenGreen(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MergeWhenGreenOf is a pull request's mark, and false when it has none.
func (s *Store) MergeWhenGreenOf(ctx context.Context, project string, number int) (MergeWhenGreen, bool, error) {
	m, err := scanMergeWhenGreen(s.db.QueryRowContext(ctx, `SELECT `+mergeWhenGreenColumns+` FROM pr_merge_when_green WHERE project = ? AND number = ?`, project, number))
	if errors.Is(err, sql.ErrNoRows) {
		return MergeWhenGreen{}, false, nil
	}
	return m, err == nil, err
}

// FailMergeWhenGreen gives up a pending mark, saying why. A mark that is
// gone, or already given up, is left as it is: false says so.
func (s *Store) FailMergeWhenGreen(ctx context.Context, project string, number int, why string, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE pr_merge_when_green SET error = ?, failed_at = ? WHERE project = ? AND number = ? AND error = ''`,
		why, at.UnixMilli(), project, number)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ForgetMergeWhenGreen drops a pull request's mark: merged, closed, or
// cancelled by the user.
func (s *Store) ForgetMergeWhenGreen(ctx context.Context, project string, number int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pr_merge_when_green WHERE project = ? AND number = ?`, project, number)
	return err
}

// TakeBackReview drops an agent's verdict if it is still the one recorded
// with fingerprint: a mark given up or cancelled returns the agent to the
// review, unless the user has since given another verdict.
func (s *Store) TakeBackReview(ctx context.Context, project, agent, fingerprint string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_reviews WHERE project = ? AND agent = ? AND fingerprint = ?`, project, agent, fingerprint)
	return err
}
