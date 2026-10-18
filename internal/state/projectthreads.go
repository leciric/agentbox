package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"agentbox/internal/trace"
)

// Two projects' leads talking. A lead messages another project's lead when
// similar work happens in both: a thread is that message and its replies, or
// an alignment on a topic that goes back and forth until one of them settles
// it with a summary they agreed. The daemon decides who may say what
// (internal/daemon/projectmessages.go); this is only what was said.

// Thread kinds.
const (
	ThreadMessage = "message"
	ThreadAlign   = "align"
)

// Thread statuses. Only an alignment is ever settled; a message thread stays
// open, and ends when a message in it doesn't ask for a reply.
const (
	ThreadOpen    = "open"
	ThreadSettled = "settled"
)

// ProjectThread is a conversation between two projects' leads. Project
// started it; Peer is the other.
type ProjectThread struct {
	ID        string
	Project   string
	Peer      string
	Kind      string
	Topic     string
	Status    string
	Summary   string
	SettledBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Other is the project in the thread that isn't project.
func (t ProjectThread) Other(project string) string {
	if t.Project == project {
		return t.Peer
	}
	return t.Project
}

// Has reports whether project is one of the thread's two.
func (t ProjectThread) Has(project string) bool { return t.Project == project || t.Peer == project }

// ProjectMessage is one lead's message in a thread: Project sent it to Peer.
// Hop counts the thread's messages before it, so the first is 0.
type ProjectMessage struct {
	ID           string
	Thread       string
	Project      string
	Peer         string
	Text         string
	Hop          int
	ExpectsReply bool
	CreatedAt    time.Time
}

const threadColumns = `id, project, peer, kind, topic, status, summary, settled_by, created_at, updated_at`

// AddProjectThread starts a thread with its first message, in one transaction.
func (s *Store) AddProjectThread(ctx context.Context, t ProjectThread, first ProjectMessage) error {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_threads (`+threadColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Project, t.Peer, t.Kind, t.Topic, t.Status, t.Summary, t.SettledBy, t.CreatedAt.UnixMilli(), t.UpdatedAt.UnixMilli()); err != nil {
		return err
	}
	if err := addProjectMessage(ctx, tx, first); err != nil {
		return err
	}
	return tx.Commit()
}

// AddProjectMessage adds a reply to a thread, and marks the thread as moved.
func (s *Store) AddProjectMessage(ctx context.Context, m ProjectMessage) error {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := addProjectMessage(ctx, tx, m); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_threads SET updated_at = ? WHERE id = ?`, m.CreatedAt.UnixMilli(), m.Thread); err != nil {
		return err
	}
	return tx.Commit()
}

func addProjectMessage(ctx context.Context, tx *trace.Tx, m ProjectMessage) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO project_messages (id, thread, project, peer, text, hop, expects_reply, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, m.ID, m.Thread, m.Project, m.Peer, m.Text, m.Hop, m.ExpectsReply, m.CreatedAt.UnixMilli())
	return err
}

// SettleProjectThread closes an alignment with the summary both leads agreed.
// It is ErrNotFound for a thread that isn't open.
func (s *Store) SettleProjectThread(ctx context.Context, id, by, summary string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE project_threads SET status = ?, summary = ?, settled_by = ?, updated_at = ?
		WHERE id = ? AND status = ?`, ThreadSettled, summary, by, at.UnixMilli(), id, ThreadOpen)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("open thread %s: %w", id, ErrNotFound)
	}
	return nil
}

func (s *Store) ProjectThread(ctx context.Context, id string) (ProjectThread, error) {
	threads, err := s.queryThreads(ctx, `WHERE id = ?`, id)
	if err != nil {
		return ProjectThread{}, err
	}
	if len(threads) == 0 {
		return ProjectThread{}, fmt.Errorf("thread %s: %w", id, ErrNotFound)
	}
	return threads[0], nil
}

// ProjectThreads lists the threads a project is in, most recently moved first.
func (s *Store) ProjectThreads(ctx context.Context, project string, limit int) ([]ProjectThread, error) {
	return s.queryThreads(ctx, `WHERE project = ? OR peer = ? ORDER BY updated_at DESC, rowid DESC LIMIT ?`, project, project, limit)
}

func (s *Store) queryThreads(ctx context.Context, where string, args ...any) ([]ProjectThread, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+threadColumns+` FROM project_threads `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var threads []ProjectThread
	for rows.Next() {
		var t ProjectThread
		var created, updated int64
		if err := rows.Scan(&t.ID, &t.Project, &t.Peer, &t.Kind, &t.Topic, &t.Status, &t.Summary, &t.SettledBy, &created, &updated); err != nil {
			return nil, err
		}
		t.CreatedAt, t.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

// ProjectMessages is a thread's messages, oldest first.
func (s *Store) ProjectMessages(ctx context.Context, thread string) ([]ProjectMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, thread, project, peer, text, hop, expects_reply, created_at
		FROM project_messages WHERE thread = ? ORDER BY hop, created_at`, thread)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var messages []ProjectMessage
	for rows.Next() {
		var m ProjectMessage
		var created int64
		if err := rows.Scan(&m.ID, &m.Thread, &m.Project, &m.Peer, &m.Text, &m.Hop, &m.ExpectsReply, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = time.UnixMilli(created)
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// ProjectMessagesSince counts the messages in a thread sent from since on.
func (s *Store) ProjectMessagesSince(ctx context.Context, thread string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM project_messages WHERE thread = ? AND created_at >= ?`,
		thread, since.UnixMilli()).Scan(&n)
	return n, err
}

// PairActivitySince counts what two projects' leads said to each other, either
// way round, from since on: their messages, and the threads they started.
func (s *Store) PairActivitySince(ctx context.Context, a, b string, since time.Time) (messages, threads int, err error) {
	pair := `((project = ? AND peer = ?) OR (project = ? AND peer = ?)) AND created_at >= ?`
	args := []any{a, b, b, a, since.UnixMilli()}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM project_messages WHERE `+pair, args...).Scan(&messages); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM project_threads WHERE `+pair, args...).Scan(&threads); err != nil {
		return 0, 0, err
	}
	return messages, threads, nil
}

// LinkProjects records that the user let two projects' leads talk.
func (s *Store) LinkProjects(ctx context.Context, a, b string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO project_links (project, peer, approved_at) VALUES (?, ?, ?), (?, ?, ?)`,
		a, b, at.UnixMilli(), b, a, at.UnixMilli())
	return err
}

// ProjectsLinked reports whether the user has let two projects' leads talk.
func (s *Store) ProjectsLinked(ctx context.Context, a, b string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM project_links WHERE project = ? AND peer = ?`, a, b).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
