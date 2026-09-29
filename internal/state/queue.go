package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The agent queue: agents a project has asked for but that wait for one of
// its slots before they get a machine. A queued agent is an agents row with
// status AgentQueued, so its name and branch are taken from the moment it is
// queued, and a row here beside it with the create request it starts from and
// its place in line. The daemon decides when each starts (package daemon,
// queue.go); this is only what it remembers.

// QueuedAgent is one agent waiting in its project's queue.
type QueuedAgent struct {
	Project  string
	Name     string
	Position int // 1 is next, within its project
	// Request is the create request it starts from, as the daemon wrote it:
	// JSON this package never reads.
	Request  []byte
	QueuedAt time.Time
}

// Ref is the agent's project/name.
func (q QueuedAgent) Ref() string { return q.Project + "/" + q.Name }

// Enqueue adds a, whose Status must be AgentQueued, to the end of its
// project's queue, with the request it will be created from.
func (s *Store) Enqueue(ctx context.Context, a Agent, request []byte) error {
	if a.Status != AgentQueued {
		return fmt.Errorf("agent %s: only a queued agent joins the queue", a.Ref())
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if a.Interface == "" {
		a.Interface = InterfaceCLI
	}
	if a.Role == "" {
		a.Role = RoleWorker
	}
	if a.ID == "" {
		a.ID = NewAgentID()
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO agents (`+agentColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Project, a.Name, a.Instance, a.AI, a.Autonomous, a.Branch, a.BaseRef, a.BaseCommit, a.Worktree, a.Status, a.CreatedAt.Unix(), a.Source, a.Title, a.ClaudeAccount, a.Interface, a.Role, a.GitHubAccount, a.FinishNotice, a.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("agent %s: %w", a.Ref(), ErrExists)
		}
		return err
	}
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) FROM agent_queue WHERE project = ?`, a.Project).Scan(&last); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_queue (project, name, position, request, queued_at) VALUES (?, ?, ?, ?, ?)`,
		a.Project, a.Name, last+1, string(request), a.CreatedAt.Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Whatever an earlier agent of the same name left behind isn't this one's.
	return removeChat(ctx, s.db, a.Project, a.Name)
}

// Queue lists the queued agents of one project, or of every project for "",
// in order: by project, then by place in line.
func (s *Store) Queue(ctx context.Context, project string) ([]QueuedAgent, error) {
	query := `SELECT project, name, position, request, queued_at FROM agent_queue`
	var args []any
	if project != "" {
		query += ` WHERE project = ?`
		args = append(args, project)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY project, position, queued_at, name`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []QueuedAgent
	for rows.Next() {
		var q QueuedAgent
		var request string
		var at int64
		if err := rows.Scan(&q.Project, &q.Name, &q.Position, &request, &at); err != nil {
			return nil, err
		}
		q.Request, q.QueuedAt = []byte(request), time.Unix(at, 0)
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Positions are kept 1..n by every write, but numbered here again from
	// the order alone, so a gap never shows as "Queued #3" with two ahead.
	for i := range out {
		if i == 0 || out[i-1].Project != out[i].Project {
			out[i].Position = 1
		} else {
			out[i].Position = out[i-1].Position + 1
		}
	}
	return out, nil
}

// QueuedAgentRequest is the request a queued agent starts from.
func (s *Store) QueuedAgentRequest(ctx context.Context, project, name string) ([]byte, error) {
	var request string
	err := s.db.QueryRowContext(ctx, `SELECT request FROM agent_queue WHERE project = ? AND name = ?`, project, name).Scan(&request)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("queued agent %s/%s: %w", project, name, ErrNotFound)
	}
	return []byte(request), err
}

// MoveQueued puts a queued agent at position (1 is next) in its project's
// queue, moving the others along. A position past the end is the end.
func (s *Store) MoveQueued(ctx context.Context, project, name string, position int) error {
	if position < 1 {
		return errors.New("a place in the queue starts at 1")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT name FROM agent_queue WHERE project = ? ORDER BY position, queued_at, name`, project)
	if err != nil {
		return err
	}
	var names []string
	found := false
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return err
		}
		if n == name {
			found = true
			continue
		}
		names = append(names, n)
	}
	_ = rows.Close()
	if !found {
		return fmt.Errorf("%s/%s isn't queued: %w", project, name, ErrNotFound)
	}
	at := min(position-1, len(names))
	names = append(names[:at], append([]string{name}, names[at:]...)...)
	for i, n := range names {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_queue SET position = ? WHERE project = ? AND name = ?`, i+1, project, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// StartQueued takes a queued agent out of the queue as it starts being made:
// its row becomes a with status AgentCreating, the machine's columns filled
// in. It fails with ErrNotFound when the agent isn't queued any more, so two
// starts of the same agent can't both go ahead.
func (s *Store) StartQueued(ctx context.Context, a Agent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM agent_queue WHERE project = ? AND name = ?`, a.Project, a.Name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s isn't queued: %w", a.Ref(), ErrNotFound)
	}
	res, err = tx.ExecContext(ctx, `UPDATE agents SET instance = ?, ai = ?, autonomous = ?, branch = ?, base_ref = ?, base_commit = ?,
		worktree = ?, status = ?, source = ?, title = ?, claude_account = ?, interface = ?, github_account = ?, finish_notice = ?
		WHERE project = ? AND name = ? AND status = ?`,
		a.Instance, a.AI, a.Autonomous, a.Branch, a.BaseRef, a.BaseCommit, a.Worktree, AgentCreating, a.Source, a.Title,
		a.ClaudeAccount, a.Interface, a.GitHubAccount, a.FinishNotice, a.Project, a.Name, AgentQueued)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s isn't queued: %w", a.Ref(), ErrNotFound)
	}
	return tx.Commit()
}

// RecordMemoryPeak remembers the most memory an agent's machine was seen to
// hold, keeping the larger of what was recorded and peak.
func (s *Store) RecordMemoryPeak(ctx context.Context, project, agent string, peak int64, at time.Time) error {
	if peak <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_memory_peaks (project, agent, peak, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (project, agent) DO UPDATE SET peak = MAX(peak, excluded.peak),
			updated_at = CASE WHEN excluded.peak > peak THEN excluded.updated_at ELSE updated_at END`,
		project, agent, peak, at.Unix())
	return err
}

// memoryPeakSample is how many of a project's agents its typical peak is
// learned from: the latest ones, so it follows the project as it changes.
const memoryPeakSample = 10

// TypicalMemoryPeak is the median of the memory peaks of a project's latest
// agents, or 0 when none was ever seen.
func (s *Store) TypicalMemoryPeak(ctx context.Context, project string) (int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT peak FROM agent_memory_peaks WHERE project = ? ORDER BY updated_at DESC LIMIT ?`, project, memoryPeakSample)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var peaks []int64
	for rows.Next() {
		var p int64
		if err := rows.Scan(&p); err != nil {
			return 0, err
		}
		peaks = append(peaks, p)
	}
	if err := rows.Err(); err != nil || len(peaks) == 0 {
		return 0, err
	}
	sort.Slice(peaks, func(i, j int) bool { return peaks[i] < peaks[j] })
	return peaks[len(peaks)/2], nil
}

// SetProjectSlots pins how many of a project's agents run at once; 0 goes
// back to auto.
func (s *Store) SetProjectSlots(ctx context.Context, name string, slots int) error {
	if slots < 0 {
		return errors.New("slots can't be negative: 0 is auto")
	}
	return s.updateProject(ctx, name, `slots = ?`, slots)
}

// SetProjectAlwaysQueue sets whether a project's new agents queue unless a
// create says otherwise.
func (s *Store) SetProjectAlwaysQueue(ctx context.Context, name string, on bool) error {
	return s.updateProject(ctx, name, `always_queue = ?`, on)
}

func (s *Store) updateProject(ctx context.Context, name, set string, value any) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET `+set+` WHERE name = ?`, value, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}
