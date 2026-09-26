package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Job is a long-running daemon operation, like creating an agent. Finished
// jobs stay in the table as history.
type Job struct {
	ID         string
	Kind       string
	Target     string
	Status     string
	Error      string
	Result     string // JSON
	Log        string
	CreatedAt  time.Time
	FinishedAt time.Time // zero while running
}

const jobColumns = `id, kind, target, status, error, result, log, created_at, finished_at`

func (s *Store) AddJob(ctx context.Context, j Job) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs (`+jobColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Kind, j.Target, j.Status, j.Error, j.Result, j.Log, j.CreatedAt.UnixMilli(), unixMilli(j.FinishedAt))
	return err
}

func (s *Store) FinishJob(ctx context.Context, j Job) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error = ?, result = ?, log = ?, finished_at = ? WHERE id = ?`,
		j.Status, j.Error, j.Result, j.Log, unixMilli(j.FinishedAt), j.ID)
	return err
}

func (s *Store) Job(ctx context.Context, id string) (Job, error) {
	jobs, err := s.queryJobs(ctx, `WHERE id = ?`, id)
	if err != nil {
		return Job{}, err
	}
	if len(jobs) == 0 {
		return Job{}, fmt.Errorf("job %q: %w", id, ErrNotFound)
	}
	return jobs[0], nil
}

// Jobs lists the most recent jobs, newest first.
func (s *Store) Jobs(ctx context.Context, limit int) ([]Job, error) {
	return s.queryJobs(ctx, `ORDER BY created_at DESC LIMIT ?`, limit)
}

// HasRunningJob says whether a job of this kind, on this target, is still
// running: an agent's create job, watched to tell an unfinished create still
// under way from one that died with the daemon (see AgentCreating).
func (s *Store) HasRunningJob(ctx context.Context, kind, target string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM jobs WHERE kind = ? AND target = ? AND status = ?`,
		kind, target, "running").Scan(&n)
	return n > 0, err
}

// FailRunningJobs marks jobs left running by a previous daemon as failed.
func (s *Store) FailRunningJobs(ctx context.Context, reason string, at time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'failed', error = ?, finished_at = ? WHERE status = 'running'`,
		reason, at.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) queryJobs(ctx context.Context, clause string, args ...any) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var jobs []Job
	for rows.Next() {
		var j Job
		var created, finished int64
		if err := rows.Scan(&j.ID, &j.Kind, &j.Target, &j.Status, &j.Error, &j.Result, &j.Log, &created, &finished); err != nil {
			return nil, err
		}
		j.CreatedAt = time.UnixMilli(created)
		if finished != 0 {
			j.FinishedAt = time.UnixMilli(finished)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return jobs, nil
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
