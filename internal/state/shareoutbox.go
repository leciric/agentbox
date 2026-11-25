package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ShareQueued is something written in a shared project while its owner
// couldn't be reached, kept to be sent once it is back
// (internal/daemon/shareoutbox.go). It goes with its remote.
type ShareQueued struct {
	ID     int64
	Remote string
	// Key goes with it each time it is sent, so the owner does it once
	// however many times it arrives.
	Key string
	// Kind is api.ShareDidMessage, ShareDidPrompt or ShareDidAnswer.
	Kind string
	// Project is the project as it is known here ("<remote>~<project>"),
	// Chat the chat ref it shows in, Question the question an answer
	// answers, and To the agent a message in the project's chat goes to.
	Project, Chat, Question, To string
	// Path is the owner's, under its share API, query included; Body is
	// what the owner is sent, with its projects named as it knows them.
	Path string
	Body []byte
	// Text and Attachments are what the app shows of it.
	Text        string
	Attachments int
	Created     time.Time
	// Error is why the owner refused it once back: it isn't sent again.
	Error string
}

const shareQueuedColumns = `id, remote_id, send_key, kind, local_name, chat, question, to_agent, path, body, text, attachments, created_at, error`

func scanShareQueued(row interface{ Scan(...any) error }) (ShareQueued, error) {
	var q ShareQueued
	var created int64
	err := row.Scan(&q.ID, &q.Remote, &q.Key, &q.Kind, &q.Project, &q.Chat, &q.Question, &q.To, &q.Path, &q.Body, &q.Text, &q.Attachments, &created, &q.Error)
	q.Created = time.UnixMilli(created)
	return q, err
}

// AddShareQueued keeps q, after everything kept for its remote before, and
// gives it its id.
func (s *Store) AddShareQueued(ctx context.Context, q ShareQueued) (ShareQueued, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO share_outbox (remote_id, send_key, kind, local_name, chat, question, to_agent, path, body, text, attachments, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		q.Remote, q.Key, q.Kind, q.Project, q.Chat, q.Question, q.To, q.Path, q.Body, q.Text, q.Attachments, q.Created.UnixMilli())
	if err != nil {
		return ShareQueued{}, err
	}
	q.ID, err = res.LastInsertId()
	return q, err
}

// ShareOutbox lists what is kept for remote, or for every remote when it is
// "", oldest first; refused ones too unless waiting says only those still to
// be sent. Bodies are left out: ShareQueuedItem reads one.
func (s *Store) ShareOutbox(ctx context.Context, remote string, waiting bool) ([]ShareQueued, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+shareQueuedColumnsNoBody+` FROM share_outbox
		WHERE (? = '' OR remote_id = ?) AND (? = 0 OR error = '') ORDER BY id`, remote, remote, boolInt(waiting))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ShareQueued
	for rows.Next() {
		q, err := scanShareQueued(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// The columns with an empty body in its place, for lists.
const shareQueuedColumnsNoBody = `id, remote_id, send_key, kind, local_name, chat, question, to_agent, path, x'', text, attachments, created_at, error`

// ShareQueuedItem reads one, body and all.
func (s *Store) ShareQueuedItem(ctx context.Context, id int64) (ShareQueued, error) {
	q, err := scanShareQueued(s.db.QueryRowContext(ctx, `SELECT `+shareQueuedColumns+` FROM share_outbox WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ShareQueued{}, ErrNotFound
	}
	return q, err
}

// RemoveShareQueued forgets one: sent, or taken back.
func (s *Store) RemoveShareQueued(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM share_outbox WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RefuseShareQueued marks one the owner refused, with why: it stays, not to
// be sent again.
func (s *Store) RefuseShareQueued(ctx context.Context, id int64, why string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE share_outbox SET error = ?, body = x'' WHERE id = ?`, why, id)
	return err
}
