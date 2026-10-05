package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// A delegation is a task one agent handed to a sub-agent of its own
// (internal/daemon/delegation.go). The sub-agent is an ordinary agent whose
// Parent names the agent that started it; the delegation is the parent's
// view of it — running, or how it ended and what it came back with — and
// outlives the sub-agent, which is retired once it is done.

// Delegation statuses.
const (
	DelegationRunning   = "running"
	DelegationDone      = "done"
	DelegationFailed    = "failed"
	DelegationCancelled = "cancelled"
)

// Delegation is one sub-agent's task.
type Delegation struct {
	ID      string
	Project string
	Parent  string // the agent that delegated, by name
	Child   string // the sub-agent, by name
	Title   string
	Task    string
	AI      string
	Job     string // the job making the sub-agent
	// Keep leaves the sub-agent as it is once done, rather than retiring it.
	Keep       bool
	Status     string
	Result     string // what it came back with, once it ended
	CreatedAt  time.Time
	FinishedAt time.Time // zero while it runs
}

// NewDelegationID is a delegation's id.
func NewDelegationID() string { return "dlg_" + randomHex(6) }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("reading random bytes: %v", err))
	}
	return hex.EncodeToString(b)
}

const delegationColumns = `id, project, parent, child, title, task, ai, job, keep, status, result, created_at, finished_at`

// AddDelegation records a sub-agent's task as it is handed over.
func (s *Store) AddDelegation(ctx context.Context, d Delegation) error {
	if d.ID == "" {
		d.ID = NewDelegationID()
	}
	if d.Status == "" {
		d.Status = DelegationRunning
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO delegations (`+delegationColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Project, d.Parent, d.Child, d.Title, d.Task, d.AI, d.Job, d.Keep, d.Status, d.Result, d.CreatedAt.UnixMilli(), unixMilli(d.FinishedAt))
	return err
}

// SetDelegationJob records the job making a delegation's sub-agent.
func (s *Store) SetDelegationJob(ctx context.Context, id, job string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE delegations SET job = ? WHERE id = ?`, job, id)
	return err
}

// Delegation is one, by id, in a project.
func (s *Store) Delegation(ctx context.Context, project, id string) (Delegation, error) {
	found, err := s.queryDelegations(ctx, `WHERE project = ? AND id = ?`, project, id)
	if err != nil {
		return Delegation{}, err
	}
	if len(found) == 0 {
		return Delegation{}, fmt.Errorf("delegation %s: %w", id, ErrNotFound)
	}
	return found[0], nil
}

// Delegations lists what an agent delegated, newest first; running limits
// it to the sub-agents still at it. An empty parent lists the project's.
func (s *Store) Delegations(ctx context.Context, project, parent string, running bool) ([]Delegation, error) {
	where := `WHERE project = ?`
	args := []any{project}
	if parent != "" {
		where += ` AND parent = ?`
		args = append(args, parent)
	}
	if running {
		where += ` AND status = ?`
		args = append(args, DelegationRunning)
	}
	return s.queryDelegations(ctx, where+` ORDER BY created_at DESC, rowid DESC`, args...)
}

// RunningDelegations is every delegation still running, in every project.
func (s *Store) RunningDelegations(ctx context.Context) ([]Delegation, error) {
	return s.queryDelegations(ctx, `WHERE status = ? ORDER BY created_at`, DelegationRunning)
}

// DelegationOf is the running delegation a sub-agent is working on, if any.
func (s *Store) DelegationOf(ctx context.Context, project, child string) (Delegation, bool, error) {
	found, err := s.queryDelegations(ctx, `WHERE project = ? AND child = ? AND status = ? ORDER BY created_at DESC LIMIT 1`,
		project, child, DelegationRunning)
	if err != nil || len(found) == 0 {
		return Delegation{}, false, err
	}
	return found[0], true, nil
}

// FinishDelegation ends a running delegation as status, with what it came
// back with. It reports whether it was still running: a delegation ends once,
// whichever of a finish, a cancel or a failure gets there first.
func (s *Store) FinishDelegation(ctx context.Context, id, status, result string, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE delegations SET status = ?, result = ?, finished_at = ? WHERE id = ? AND status = ?`,
		status, result, unixMilli(at), id, DelegationRunning)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) queryDelegations(ctx context.Context, clause string, args ...any) ([]Delegation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+delegationColumns+` FROM delegations `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Delegation
	for rows.Next() {
		var d Delegation
		var created, finished int64
		if err := rows.Scan(&d.ID, &d.Project, &d.Parent, &d.Child, &d.Title, &d.Task, &d.AI, &d.Job, &d.Keep, &d.Status, &d.Result, &created, &finished); err != nil {
			return nil, err
		}
		d.CreatedAt = time.UnixMilli(created)
		d.FinishedAt = fromMilli(finished)
		out = append(out, d)
	}
	return out, rows.Err()
}

func fromMilli(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
