package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"agentbox/internal/trace"
)

// Scheduled tasks: something a project's lead is to do at a time the user
// chose, once or again and again. The daemon runs them
// (internal/daemon/schedules.go), and package schedule says when; this is
// only what was asked for and what came of each run.

// What a schedule's run does.
const (
	// ScheduleTargetLead wakes the project's lead with the task as a message.
	ScheduleTargetLead = "lead"
	// ScheduleTargetAgent creates an agent with the task as its first message.
	ScheduleTargetAgent = "agent"
)

// ScheduleSessionReuse is Schedule.Session for an agent's run that goes to
// the last run's agent; anything else makes a fresh one.
const ScheduleSessionReuse = "reuse"

// What started a run.
const (
	ScheduleTriggerTime   = "schedule"
	ScheduleTriggerManual = "manual"
)

// How a run went.
const (
	ScheduleRunSent   = "sent"
	ScheduleRunAgent  = "agent"
	ScheduleRunFailed = "failed"
	// ScheduleRunSkipped is a run that didn't happen: its precheck failed, or
	// it was missed by more than its grace.
	ScheduleRunSkipped = "skipped"
)

// Schedule is one scheduled task of a project.
type Schedule struct {
	ID      string
	Project string
	Title   string
	// Task is what the lead, or the new agent, is told to do.
	Task string
	// Kind, At, Every, Cron and Timezone are when it runs (schedule.Spec).
	Kind     string
	At       time.Time
	Every    time.Duration
	Cron     string
	Timezone string
	Target   string
	// Session is ScheduleSessionReuse for an agent's run that tells the agent
	// the last run made, while it is still there, rather than making another.
	Session string
	// Model and Effort are what an agent's run creates it on; empty for the
	// project's defaults.
	Model  string
	Effort string
	// Precheck is a shell command run in the lead's worktree before each
	// run: when it fails, the run is skipped.
	Precheck string
	// Grace is how late a missed run may still run; zero runs it however
	// late. A run later than that is skipped, and recorded as skipped.
	Grace  time.Duration
	Paused bool
	// NextRun is when it runs next; zero when it never will again: a one-off
	// that ran, or a cron expression that no longer matches anything.
	NextRun time.Time
	// CreatedBy is "lead" or "user".
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ScheduleRun is one time a schedule ran.
type ScheduleRun struct {
	ID       string
	Schedule string
	Project  string
	Trigger  string
	// DueAt is when it was meant to run, RanAt when it did. Late is set when
	// the daemon wasn't there to run it on time; Missed counts the other
	// times it was due meanwhile, which this one run stands for.
	DueAt  time.Time
	RanAt  time.Time
	Late   bool
	Missed int
	// Outcome is ScheduleRunSent, ScheduleRunAgent, ScheduleRunFailed or
	// ScheduleRunSkipped;
	// empty while it is being delivered.
	Outcome string
	// Agent is the agent it created, Job the job creating it, ChatItem the
	// chat item it arrived as in the lead's chat.
	Agent    string
	Job      string
	ChatItem string
	Error    string
}

const scheduleColumns = `id, project, title, task, kind, at, every_ms, cron, timezone, target, session, model, effort, precheck, grace_minutes, paused, next_run, created_by, created_at, updated_at`

func msOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func msTimeOrZero(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// AddSchedule stores a new schedule.
func (s *Store) AddSchedule(ctx context.Context, sc Schedule) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO schedules (`+scheduleColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sc.ID, sc.Project, sc.Title, sc.Task, sc.Kind, msOrZero(sc.At), sc.Every.Milliseconds(), sc.Cron, sc.Timezone, sc.Target,
		sc.Session, sc.Model, sc.Effort, sc.Precheck, int64(sc.Grace/time.Minute), sc.Paused, msOrZero(sc.NextRun), sc.CreatedBy, sc.CreatedAt.UnixMilli(), sc.UpdatedAt.UnixMilli())
	return err
}

// UpdateSchedule rewrites what a schedule is: everything but its project and
// when it was made.
func (s *Store) UpdateSchedule(ctx context.Context, sc Schedule) error {
	res, err := s.db.ExecContext(ctx, `UPDATE schedules SET title = ?, task = ?, kind = ?, at = ?, every_ms = ?, cron = ?, timezone = ?,
		target = ?, session = ?, model = ?, effort = ?, precheck = ?, grace_minutes = ?, paused = ?, next_run = ?, updated_at = ? WHERE id = ? AND project = ?`,
		sc.Title, sc.Task, sc.Kind, msOrZero(sc.At), sc.Every.Milliseconds(), sc.Cron, sc.Timezone,
		sc.Target, sc.Session, sc.Model, sc.Effort, sc.Precheck, int64(sc.Grace/time.Minute), sc.Paused, msOrZero(sc.NextRun), sc.UpdatedAt.UnixMilli(), sc.ID, sc.Project)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("schedule %s: %w", sc.ID, ErrNotFound)
	}
	return nil
}

// DeleteSchedule removes a schedule and its runs.
func (s *Store) DeleteSchedule(ctx context.Context, project, id string) error {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM schedules WHERE id = ? AND project = ?`, id, project)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("schedule %s: %w", id, ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM schedule_runs WHERE schedule = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Schedule is one of a project's schedules.
func (s *Store) Schedule(ctx context.Context, project, id string) (Schedule, error) {
	list, err := s.querySchedules(ctx, `WHERE id = ? AND project = ?`, id, project)
	if err != nil {
		return Schedule{}, err
	}
	if len(list) == 0 {
		return Schedule{}, fmt.Errorf("schedule %s: %w", id, ErrNotFound)
	}
	return list[0], nil
}

// Schedules lists a project's schedules, oldest first.
func (s *Store) Schedules(ctx context.Context, project string) ([]Schedule, error) {
	return s.querySchedules(ctx, `WHERE project = ? ORDER BY created_at, rowid`, project)
}

// DueSchedules are the schedules, of every project, that should have run by
// now and haven't.
func (s *Store) DueSchedules(ctx context.Context, now time.Time) ([]Schedule, error) {
	return s.querySchedules(ctx, `WHERE paused = 0 AND next_run > 0 AND next_run <= ? ORDER BY next_run`, now.UnixMilli())
}

// NextScheduleTime is when the next schedule of any project runs, and false
// when none will.
func (s *Store) NextScheduleTime(ctx context.Context) (time.Time, bool, error) {
	var next sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT min(next_run) FROM schedules WHERE paused = 0 AND next_run > 0`).Scan(&next); err != nil {
		return time.Time{}, false, err
	}
	if !next.Valid {
		return time.Time{}, false, nil
	}
	return time.UnixMilli(next.Int64), true, nil
}

func (s *Store) querySchedules(ctx context.Context, where string, args ...any) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM schedules `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Schedule
	for rows.Next() {
		var sc Schedule
		var at, every, grace, next, created, updated int64
		if err := rows.Scan(&sc.ID, &sc.Project, &sc.Title, &sc.Task, &sc.Kind, &at, &every, &sc.Cron, &sc.Timezone, &sc.Target,
			&sc.Session, &sc.Model, &sc.Effort, &sc.Precheck, &grace, &sc.Paused, &next, &sc.CreatedBy, &created, &updated); err != nil {
			return nil, err
		}
		sc.Grace = time.Duration(grace) * time.Minute
		sc.At, sc.Every, sc.NextRun = msTimeOrZero(at), time.Duration(every)*time.Millisecond, msTimeOrZero(next)
		sc.CreatedAt, sc.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ErrScheduleMoved is ClaimScheduleRun's answer when the schedule isn't due
// at the time it was read as due any more: paused, edited, deleted or run by
// somebody else since.
var ErrScheduleMoved = errors.New("the schedule changed before it could run")

// ClaimScheduleRun records that a schedule is running, and moves it on to
// next, in one transaction: a daemon that stops halfway through delivering
// it doesn't run it again. A scheduled run claims only a schedule still due
// at wasDue and not paused; a manual one (wasDue zero) claims it whatever it
// is, and leaves its next run alone.
func (s *Store) ClaimScheduleRun(ctx context.Context, run ScheduleRun, wasDue, next time.Time) error {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if !wasDue.IsZero() {
		res, err := tx.ExecContext(ctx, `UPDATE schedules SET next_run = ? WHERE id = ? AND project = ? AND next_run = ? AND paused = 0`,
			msOrZero(next), run.Schedule, run.Project, wasDue.UnixMilli())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrScheduleMoved
		}
	} else {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schedules WHERE id = ? AND project = ?`, run.Schedule, run.Project).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("schedule %s: %w", run.Schedule, ErrNotFound)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_runs (id, schedule, project, trigger, due_at, ran_at, late, missed, outcome, agent, job, chat_item, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.Schedule, run.Project, run.Trigger, msOrZero(run.DueAt), run.RanAt.UnixMilli(), run.Late, run.Missed,
		run.Outcome, run.Agent, run.Job, run.ChatItem, run.Error); err != nil {
		return err
	}
	return tx.Commit()
}

// FinishScheduleRun records how a run went.
func (s *Store) FinishScheduleRun(ctx context.Context, run ScheduleRun) error {
	_, err := s.db.ExecContext(ctx, `UPDATE schedule_runs SET outcome = ?, agent = ?, job = ?, chat_item = ?, error = ? WHERE id = ?`,
		run.Outcome, run.Agent, run.Job, run.ChatItem, run.Error, run.ID)
	return err
}

// SetScheduleRunAgent names the agent a run's job created, once it has.
func (s *Store) SetScheduleRunAgent(ctx context.Context, job, agent string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE schedule_runs SET agent = ? WHERE job = ? AND job != ''`, agent, job)
	return err
}

// ScheduleRuns lists a project's runs, newest first: one schedule's when
// schedule isn't empty.
func (s *Store) ScheduleRuns(ctx context.Context, project, schedule string, limit int) ([]ScheduleRun, error) {
	where, args := `WHERE project = ?`, []any{project}
	if schedule != "" {
		where += ` AND schedule = ?`
		args = append(args, schedule)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, schedule, project, trigger, due_at, ran_at, late, missed, outcome, agent, job, chat_item, error
		FROM schedule_runs `+where+` ORDER BY ran_at DESC, rowid DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ScheduleRun
	for rows.Next() {
		var r ScheduleRun
		var due, ran int64
		if err := rows.Scan(&r.ID, &r.Schedule, &r.Project, &r.Trigger, &due, &ran, &r.Late, &r.Missed, &r.Outcome, &r.Agent, &r.Job, &r.ChatItem, &r.Error); err != nil {
			return nil, err
		}
		r.DueAt, r.RanAt = msTimeOrZero(due), time.UnixMilli(ran)
		out = append(out, r)
	}
	return out, rows.Err()
}
