package state

import (
	"context"
	"fmt"
	"time"
)

// Scheduled tasks: a project's recurring agent runs, like a nightly build or
// a weekly dependency bump (internal/daemon/schedules.go). Each run is an
// ordinary agent made for the task, recorded in schedule_runs so the next
// one can tell whether the last is still going, and so the app can show how
// each went.

// What a scheduled run's result goes to.
const (
	// ScheduleOutcomePR has the run push its branch and open a pull request.
	ScheduleOutcomePR = "pr"
	// ScheduleOutcomeReport has it open none, and its result goes to the
	// project's chat.
	ScheduleOutcomeReport = "report"
)

// How a scheduled run was started.
const (
	ScheduleTriggerSchedule = "schedule" // it was due
	ScheduleTriggerCatchUp  = "catch-up" // it fell due while AgentBox wasn't running
	ScheduleTriggerManual   = "manual"   // run now
)

// Scheduled run statuses.
const (
	ScheduleRunRunning = "running"
	ScheduleRunDone    = "done"
	ScheduleRunFailed  = "failed"
	ScheduleRunSkipped = "skipped" // it fell due while the last run was still going
)

// Schedule is one scheduled task.
type Schedule struct {
	ID      string
	Project string
	Name    string
	Cron    string // a five-field cron expression, or a descriptor like @daily
	Task    string
	AI      string
	Model   string
	Effort  string
	Size    string
	Outcome string
	Paused  bool

	CreatedAt time.Time
	NextRun   time.Time // when it is next due; zero while paused
	LastRun   time.Time // when a run last started; zero if none has
}

// ScheduleRun is one run of a scheduled task.
type ScheduleRun struct {
	ID         string
	Schedule   string
	Project    string
	Agent      string // the agent made for it, once it has a name
	Job        string // the job making that agent
	Trigger    string
	Status     string
	Result     string // what it came back with, or why it failed
	StartedAt  time.Time
	FinishedAt time.Time
}

// NewScheduleID is a schedule's id; NewScheduleRunID a run's.
func NewScheduleID() string    { return "sch_" + randomHex(6) }
func NewScheduleRunID() string { return "run_" + randomHex(6) }

const scheduleColumns = `id, project, name, cron, task, ai, model, effort, size, outcome, paused, created_at, next_run_at, last_run_at`

// AddSchedule records a new scheduled task.
func (s *Store) AddSchedule(ctx context.Context, sc Schedule) (Schedule, error) {
	if sc.ID == "" {
		sc.ID = NewScheduleID()
	}
	if sc.CreatedAt.IsZero() {
		sc.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO schedules (`+scheduleColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sc.ID, sc.Project, sc.Name, sc.Cron, sc.Task, sc.AI, sc.Model, sc.Effort, sc.Size, sc.Outcome, sc.Paused,
		sc.CreatedAt.UnixMilli(), unixMilli(sc.NextRun), unixMilli(sc.LastRun))
	return sc, err
}

// UpdateSchedule writes every field of a schedule back.
func (s *Store) UpdateSchedule(ctx context.Context, sc Schedule) error {
	res, err := s.db.ExecContext(ctx, `UPDATE schedules SET name = ?, cron = ?, task = ?, ai = ?, model = ?, effort = ?, size = ?,
		outcome = ?, paused = ?, next_run_at = ?, last_run_at = ? WHERE project = ? AND id = ?`,
		sc.Name, sc.Cron, sc.Task, sc.AI, sc.Model, sc.Effort, sc.Size, sc.Outcome, sc.Paused,
		unixMilli(sc.NextRun), unixMilli(sc.LastRun), sc.Project, sc.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("schedule %s: %w", sc.ID, ErrNotFound)
	}
	return nil
}

// Schedule is one of a project's scheduled tasks, by id.
func (s *Store) Schedule(ctx context.Context, project, id string) (Schedule, error) {
	found, err := s.querySchedules(ctx, `WHERE project = ? AND id = ?`, project, id)
	if err != nil {
		return Schedule{}, err
	}
	if len(found) == 0 {
		return Schedule{}, fmt.Errorf("schedule %s: %w", id, ErrNotFound)
	}
	return found[0], nil
}

// Schedules lists a project's scheduled tasks, or every project's when
// project is "", oldest first.
func (s *Store) Schedules(ctx context.Context, project string) ([]Schedule, error) {
	if project == "" {
		return s.querySchedules(ctx, `ORDER BY created_at, rowid`)
	}
	return s.querySchedules(ctx, `WHERE project = ? ORDER BY created_at, rowid`, project)
}

// RemoveSchedule deletes a scheduled task and the record of its runs. The
// agents its runs made are left as they are.
func (s *Store) RemoveSchedule(ctx context.Context, project, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE project = ? AND id = ?`, project, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("schedule %s: %w", id, ErrNotFound)
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM schedule_runs WHERE schedule_id = ?`, id)
	return err
}

func (s *Store) querySchedules(ctx context.Context, clause string, args ...any) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM schedules `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Schedule
	for rows.Next() {
		var sc Schedule
		var created, next, last int64
		if err := rows.Scan(&sc.ID, &sc.Project, &sc.Name, &sc.Cron, &sc.Task, &sc.AI, &sc.Model, &sc.Effort, &sc.Size, &sc.Outcome,
			&sc.Paused, &created, &next, &last); err != nil {
			return nil, err
		}
		sc.CreatedAt, sc.NextRun, sc.LastRun = time.UnixMilli(created), fromMilli(next), fromMilli(last)
		out = append(out, sc)
	}
	return out, rows.Err()
}

const scheduleRunColumns = `id, schedule_id, project, agent, job, trigger, status, result, started_at, finished_at`

// AddScheduleRun records a run as it starts, and forgets all but the newest
// keep runs of its schedule.
func (s *Store) AddScheduleRun(ctx context.Context, r ScheduleRun, keep int) (ScheduleRun, error) {
	if r.ID == "" {
		r.ID = NewScheduleRunID()
	}
	if r.Status == "" {
		r.Status = ScheduleRunRunning
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO schedule_runs (`+scheduleRunColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Schedule, r.Project, r.Agent, r.Job, r.Trigger, r.Status, r.Result, r.StartedAt.UnixMilli(), unixMilli(r.FinishedAt)); err != nil {
		return ScheduleRun{}, err
	}
	if keep > 0 {
		// A running run is never forgotten: it is what stops the next one
		// overlapping it.
		if _, err := s.db.ExecContext(ctx, `DELETE FROM schedule_runs WHERE schedule_id = ? AND status != ? AND id NOT IN (
			SELECT id FROM schedule_runs WHERE schedule_id = ? ORDER BY started_at DESC, rowid DESC LIMIT ?)`,
			r.Schedule, ScheduleRunRunning, r.Schedule, keep); err != nil {
			return ScheduleRun{}, err
		}
	}
	return r, nil
}

// SetScheduleRunAgent records the agent and job a run is made of.
func (s *Store) SetScheduleRunAgent(ctx context.Context, id, agent, job string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE schedule_runs SET agent = ?, job = ? WHERE id = ?`, agent, job, id)
	return err
}

// FinishScheduleRun ends a running run. It reports whether it was still
// running, so a run ends once.
func (s *Store) FinishScheduleRun(ctx context.Context, id, status, result string, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE schedule_runs SET status = ?, result = ?, finished_at = ? WHERE id = ? AND status = ?`,
		status, result, unixMilli(at), id, ScheduleRunRunning)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ScheduleRuns lists a schedule's runs, newest first, at most limit of them.
func (s *Store) ScheduleRuns(ctx context.Context, schedule string, limit int) ([]ScheduleRun, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.queryScheduleRuns(ctx, `WHERE schedule_id = ? ORDER BY started_at DESC, rowid DESC LIMIT ?`, schedule, limit)
}

// RunningScheduleRuns is every run still going, in every project.
func (s *Store) RunningScheduleRuns(ctx context.Context) ([]ScheduleRun, error) {
	return s.queryScheduleRuns(ctx, `WHERE status = ? ORDER BY started_at`, ScheduleRunRunning)
}

// ScheduleRunOf is the running run an agent was made for, if any.
func (s *Store) ScheduleRunOf(ctx context.Context, project, agent string) (ScheduleRun, bool, error) {
	found, err := s.queryScheduleRuns(ctx, `WHERE project = ? AND agent = ? AND status = ? ORDER BY started_at DESC LIMIT 1`,
		project, agent, ScheduleRunRunning)
	if err != nil || len(found) == 0 {
		return ScheduleRun{}, false, err
	}
	return found[0], true, nil
}

func (s *Store) queryScheduleRuns(ctx context.Context, clause string, args ...any) ([]ScheduleRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduleRunColumns+` FROM schedule_runs `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ScheduleRun
	for rows.Next() {
		var r ScheduleRun
		var started, finished int64
		if err := rows.Scan(&r.ID, &r.Schedule, &r.Project, &r.Agent, &r.Job, &r.Trigger, &r.Status, &r.Result, &started, &finished); err != nil {
			return nil, err
		}
		r.StartedAt, r.FinishedAt = time.UnixMilli(started), fromMilli(finished)
		out = append(out, r)
	}
	return out, rows.Err()
}
