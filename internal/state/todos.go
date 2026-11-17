package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"agentbox/internal/trace"
)

// A project's todo list: what the user, or the lead on their behalf, means to
// get done, in the order they put it. It is a list a person keeps, not the
// task graph #250 removed: a todo is open or done, has a place in the list,
// and may have been handed to an agent. Nothing adds or checks one off on its
// own.

// ErrTodoOrder is a reorder that doesn't name every todo of the project
// exactly once: the list changed under whoever sent it.
var ErrTodoOrder = errors.New("the order must name each of the project's todos once: the list changed, so read it again")

// Todo is one entry of a project's todo list.
type Todo struct {
	ID      string
	Project string
	Text    string
	Done    bool
	// Position is its place in the list, from 1.
	Position int
	// Job is the job creating the agent it was handed to, and Agent that
	// agent's name once the job made it; both empty until then.
	Job   string
	Agent string
	// CreatedBy is "user" or "lead".
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
	// DoneAt is when it was checked off; zero while it is open.
	DoneAt time.Time
}

const todoColumns = `id, project, text, done, position, job, agent, created_by, created_at, updated_at, done_at`

// AddTodo puts a new todo at the end of its project's list, and returns it
// with its position.
func (s *Store) AddTodo(ctx context.Context, t Todo) (Todo, error) {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return Todo{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM todos WHERE project = ?`, t.Project).Scan(&t.Position); err != nil {
		return Todo{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO todos (`+todoColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Project, t.Text, t.Done, t.Position, t.Job, t.Agent, t.CreatedBy, t.CreatedAt.UnixMilli(), t.UpdatedAt.UnixMilli(), msOrZero(t.DoneAt)); err != nil {
		return Todo{}, err
	}
	return t, tx.Commit()
}

// TodoPatch is what a change to a todo sets; nil fields are left as they
// are, so a change never writes back a column someone else changed meanwhile
// (an edit racing a start would otherwise clear the agent it was given).
type TodoPatch struct {
	Text *string
	Done *bool
}

// PatchTodo changes only what patch sets, and returns the todo as it now is,
// with whether its done state changed. Checking off sets done_at; reopening
// clears it; checking off one that is done already leaves done_at alone.
func (s *Store) PatchTodo(ctx context.Context, project, id string, patch TodoPatch, now time.Time) (Todo, bool, error) {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return Todo{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var was bool
	if err := tx.QueryRowContext(ctx, `SELECT done FROM todos WHERE id = ? AND project = ?`, id, project).Scan(&was); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Todo{}, false, fmt.Errorf("todo %s: %w", id, ErrNotFound)
		}
		return Todo{}, false, err
	}
	if patch.Text != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE todos SET text = ?, updated_at = ? WHERE id = ? AND project = ?`, *patch.Text, now.UnixMilli(), id, project); err != nil {
			return Todo{}, false, err
		}
	}
	flipped := patch.Done != nil && *patch.Done != was
	if flipped {
		doneAt := int64(0)
		if *patch.Done {
			doneAt = now.UnixMilli()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE todos SET done = ?, done_at = ?, updated_at = ? WHERE id = ? AND project = ?`, *patch.Done, doneAt, now.UnixMilli(), id, project); err != nil {
			return Todo{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Todo{}, false, err
	}
	t, err := s.TodoByID(ctx, project, id)
	return t, flipped, err
}

// ClaimTodo hands a todo to whoever is about to start an agent for it, if it
// is still with the job and agent the caller saw (job, agent): a compare and
// swap, so of two starts at once only one claims it. claim takes the job's
// place until the job exists (SetTodoJob) or the start gives up
// (ReleaseTodo).
func (s *Store) ClaimTodo(ctx context.Context, project, id, job, agent, claim string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE todos SET job = ?, agent = '', updated_at = ? WHERE id = ? AND project = ? AND job = ? AND agent = ?`,
		claim, now.UnixMilli(), id, project, job, agent)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetTodoJob puts the job a claim started in the claim's place.
func (s *Store) SetTodoJob(ctx context.Context, project, id, claim, job string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE todos SET job = ? WHERE id = ? AND project = ? AND job = ?`, job, id, project, claim)
	return err
}

// ReleaseTodo gives a claimed todo back its job and agent from before, when
// the start it was claimed for failed.
func (s *Store) ReleaseTodo(ctx context.Context, project, id, claim, job, agent string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE todos SET job = ?, agent = ? WHERE id = ? AND project = ? AND job = ?`, job, agent, id, project, claim)
	return err
}

// SetTodoAgent records the agent a todo's job made, if the todo is still
// waiting on that job.
func (s *Store) SetTodoAgent(ctx context.Context, project, job, agent string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE todos SET agent = ? WHERE project = ? AND job = ? AND agent = ''`, agent, project, job)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteTodo removes a todo and closes the gap it leaves, so positions stay
// 1..n.
func (s *Store) DeleteTodo(ctx context.Context, project, id string) error {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var pos int
	if err := tx.QueryRowContext(ctx, `SELECT position FROM todos WHERE id = ? AND project = ?`, id, project).Scan(&pos); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("todo %s: %w", id, ErrNotFound)
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM todos WHERE id = ? AND project = ?`, id, project); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE todos SET position = position - 1 WHERE project = ? AND position > ?`, project, pos); err != nil {
		return err
	}
	return tx.Commit()
}

// ReorderTodos puts a project's todos in the order ids gives, which must
// name each of them once (ErrTodoOrder otherwise).
func (s *Store) ReorderTodos(ctx context.Context, project string, ids []string) error {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM todos WHERE project = ?`, project)
	if err != nil {
		return err
	}
	var have []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		have = append(have, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	want := slices.Clone(ids)
	slices.Sort(have)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		return ErrTodoOrder
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE todos SET position = ? WHERE id = ? AND project = ?`, i+1, id, project); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TodoByID is one of a project's todos.
func (s *Store) TodoByID(ctx context.Context, project, id string) (Todo, error) {
	list, err := s.queryTodos(ctx, `WHERE id = ? AND project = ?`, id, project)
	if err != nil {
		return Todo{}, err
	}
	if len(list) == 0 {
		return Todo{}, fmt.Errorf("todo %s: %w", id, ErrNotFound)
	}
	return list[0], nil
}

// Todos is a project's todo list, in its order.
func (s *Store) Todos(ctx context.Context, project string) ([]Todo, error) {
	return s.queryTodos(ctx, `WHERE project = ? ORDER BY position, created_at`, project)
}

// OpenTodoCounts is how many todos each project has still open; a project
// with none isn't in it.
func (s *Store) OpenTodoCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, count(*) FROM todos WHERE done = 0 GROUP BY project`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, rows.Err()
}

func (s *Store) queryTodos(ctx context.Context, where string, args ...any) ([]Todo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+todoColumns+` FROM todos `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Todo
	for rows.Next() {
		var t Todo
		var created, updated, done int64
		if err := rows.Scan(&t.ID, &t.Project, &t.Text, &t.Done, &t.Position, &t.Job, &t.Agent, &t.CreatedBy, &created, &updated, &done); err != nil {
			return nil, err
		}
		t.CreatedAt, t.UpdatedAt, t.DoneAt = time.UnixMilli(created), time.UnixMilli(updated), msTimeOrZero(done)
		out = append(out, t)
	}
	return out, rows.Err()
}
