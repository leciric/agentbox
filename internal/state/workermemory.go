package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// WorkerMemory is what one worker of a tool takes in a project, learned from
// its heavy commands that finished (agent.LearnPerWorker): PerWorker bytes,
// from Runs runs.
type WorkerMemory struct {
	Project   string
	Tool      string
	PerWorker int64
	Runs      int
	UpdatedAt time.Time
}

// WorkerMemory is what is known of tool's workers in project; ok is false
// for nothing yet.
func (s *Store) WorkerMemory(ctx context.Context, project, tool string) (WorkerMemory, bool, error) {
	w := WorkerMemory{Project: project, Tool: tool}
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT per_worker, runs, updated_at FROM worker_memory WHERE project = ? AND tool = ?`, project, tool).
		Scan(&w.PerWorker, &w.Runs, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkerMemory{}, false, nil
	}
	if err != nil {
		return WorkerMemory{}, false, err
	}
	w.UpdatedAt = time.Unix(at, 0)
	return w, true, nil
}

// SetWorkerMemory keeps what is known of a tool's workers in a project.
func (s *Store) SetWorkerMemory(ctx context.Context, w WorkerMemory) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO worker_memory (project, tool, per_worker, runs, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (project, tool) DO UPDATE SET per_worker = excluded.per_worker, runs = excluded.runs, updated_at = excluded.updated_at`,
		w.Project, w.Tool, w.PerWorker, w.Runs, w.UpdatedAt.Unix())
	return err
}
