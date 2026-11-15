package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MergeWhenGreen is a pull request the user marked "Resolved — merge when CI
// is green": the daemon merges it with Method on the first green, mergeable
// head, however many red checks, conflicts and fixes come before. Error is
// set when it was given up instead (closed, GitHub refused the merge for good,
// its agent is gone, or it stayed unmergeable too long): the row then waits
// for the user to dismiss it or try again, and is no longer watched.
type MergeWhenGreen struct {
	Project string
	Number  int
	Agent   string // the agent whose work it is, whose verdict it recorded
	Method  string // merge, squash or rebase
	// HeadSHA is the commit the user resolved.
	HeadSHA string
	// Fingerprint is the review verdict the mark recorded, taken back when it
	// is given up or cancelled.
	Fingerprint string
	Error       string
	At          time.Time
	FailedAt    time.Time
	MergeWait
}

// MergeWait is what a pending mark waits on as the watch last saw it, and
// what the give-up limits count.
type MergeWait struct {
	// Waiting is "checks" (running), "failing", "conflict", "draft" or
	// "retry" (GitHub refused the merge for now), "" before the first look.
	Waiting string
	Detail  string // the failing checks, or GitHub's message
	// TroubleSince is when it was first seen failing, conflicting or a
	// draft; zero while it never was.
	TroubleSince time.Time
	// BadHeads is how many heads were seen failing or conflicting, BadHead
	// the last of them: the fix rounds are BadHeads after the first.
	BadHeads int
	BadHead  string
}

// Pending is whether the daemon is still waiting to merge it.
func (m MergeWhenGreen) Pending() bool { return m.Error == "" }

const mergeWhenGreenColumns = `project, number, agent, method, head_sha, fingerprint, error, at, failed_at, waiting, detail, trouble_since, bad_heads, bad_head`

func scanMergeWhenGreen(row interface{ Scan(...any) error }) (MergeWhenGreen, error) {
	var m MergeWhenGreen
	var at, failed, trouble int64
	if err := row.Scan(&m.Project, &m.Number, &m.Agent, &m.Method, &m.HeadSHA, &m.Fingerprint, &m.Error, &at, &failed,
		&m.Waiting, &m.Detail, &trouble, &m.BadHeads, &m.BadHead); err != nil {
		return MergeWhenGreen{}, err
	}
	if trouble != 0 {
		m.TroubleSince = time.UnixMilli(trouble)
	}
	m.At = time.UnixMilli(at)
	if failed != 0 {
		m.FailedAt = time.UnixMilli(failed)
	}
	return m, nil
}

// SetMergeWhenGreen marks a pull request to be merged when it is green,
// replacing any earlier mark on it, a failed one included, and starting its
// wait afresh.
func (s *Store) SetMergeWhenGreen(ctx context.Context, m MergeWhenGreen) error {
	failed := millisOrZero(m.FailedAt)
	_, err := s.db.ExecContext(ctx, `INSERT INTO pr_merge_when_green (`+mergeWhenGreenColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, number) DO UPDATE SET agent = excluded.agent, method = excluded.method,
			head_sha = excluded.head_sha, fingerprint = excluded.fingerprint, error = excluded.error,
			at = excluded.at, failed_at = excluded.failed_at, waiting = excluded.waiting, detail = excluded.detail,
			trouble_since = excluded.trouble_since, bad_heads = excluded.bad_heads, bad_head = excluded.bad_head`,
		m.Project, m.Number, m.Agent, m.Method, m.HeadSHA, m.Fingerprint, m.Error, m.At.UnixMilli(), failed,
		m.Waiting, m.Detail, millisOrZero(m.TroubleSince), m.BadHeads, m.BadHead)
	return err
}

// SetMergeWait saves what a pending mark waits on now. A mark that is gone,
// or given up meanwhile, is left as it is.
func (s *Store) SetMergeWait(ctx context.Context, project string, number int, w MergeWait) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pr_merge_when_green SET waiting = ?, detail = ?, trouble_since = ?, bad_heads = ?, bad_head = ?
		WHERE project = ? AND number = ? AND error = ''`,
		w.Waiting, w.Detail, millisOrZero(w.TroubleSince), w.BadHeads, w.BadHead, project, number)
	return err
}

func millisOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
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
