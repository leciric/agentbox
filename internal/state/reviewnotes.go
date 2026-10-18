package state

import (
	"context"
	"database/sql"
	"time"
)

// ReviewNote is a note the user left on lines of an agent's diff in the
// review: the file, the side of the diff its lines are numbered on ("new"
// for added and unchanged lines, "old" for removed ones), the lines, the code
// they quote and what the user wrote. A draft until Request changes sends
// it (SentAt), and resolved when the user says so (ResolvedAt).
type ReviewNote struct {
	ID         int64
	Project    string
	Agent      string
	Path       string
	Side       string
	Start      int
	End        int
	Code       string
	Body       string
	CreatedAt  time.Time
	SentAt     time.Time // zero while a draft
	SentHash   string    // the file's content hash when it was sent
	ResolvedAt time.Time
}

// AddReviewNote keeps a new draft note and returns it with its ID.
func (s *Store) AddReviewNote(ctx context.Context, n ReviewNote) (ReviewNote, error) {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO review_notes (project, agent, path, side, start_line, end_line, code, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Project, n.Agent, n.Path, n.Side, n.Start, n.End, n.Code, n.Body, n.CreatedAt.UnixMilli())
	if err != nil {
		return ReviewNote{}, err
	}
	n.ID, err = res.LastInsertId()
	return n, err
}

// ReviewNotes is an agent's notes, drafts and sent, in the order they were
// written.
func (s *Store) ReviewNotes(ctx context.Context, project, agent string) ([]ReviewNote, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, path, side, start_line, end_line, code, body, created_at, sent_at, sent_hash, resolved_at
		FROM review_notes WHERE project = ? AND agent = ? ORDER BY id`, project, agent)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ReviewNote{}
	for rows.Next() {
		n := ReviewNote{Project: project, Agent: agent}
		var created, sent, resolved int64
		if err := rows.Scan(&n.ID, &n.Path, &n.Side, &n.Start, &n.End, &n.Code, &n.Body, &created, &sent, &n.SentHash, &resolved); err != nil {
			return nil, err
		}
		n.CreatedAt = time.UnixMilli(created)
		n.SentAt = milliTime(sent)
		n.ResolvedAt = milliTime(resolved)
		out = append(out, n)
	}
	return out, rows.Err()
}

// ReviewNote is one of an agent's notes, or ErrNotFound.
func (s *Store) ReviewNote(ctx context.Context, project, agent string, id int64) (ReviewNote, error) {
	notes, err := s.ReviewNotes(ctx, project, agent)
	if err != nil {
		return ReviewNote{}, err
	}
	for _, n := range notes {
		if n.ID == id {
			return n, nil
		}
	}
	return ReviewNote{}, ErrNotFound
}

// SetReviewNoteBody rewrites what a draft says. A sent note can't be: the
// agent has it as it was.
func (s *Store) SetReviewNoteBody(ctx context.Context, project, agent string, id int64, body string) error {
	return s.oneNote(s.db.ExecContext(ctx,
		`UPDATE review_notes SET body = ? WHERE project = ? AND agent = ? AND id = ? AND sent_at = 0`, body, project, agent, id))
}

// SetReviewNoteResolved marks a sent note resolved, or not, at at.
func (s *Store) SetReviewNoteResolved(ctx context.Context, project, agent string, id int64, resolved bool, at time.Time) error {
	var ms int64
	if resolved {
		ms = at.UnixMilli()
	}
	return s.oneNote(s.db.ExecContext(ctx,
		`UPDATE review_notes SET resolved_at = ? WHERE project = ? AND agent = ? AND id = ? AND sent_at > 0`, ms, project, agent, id))
}

// DeleteReviewNote removes a note, draft or sent.
func (s *Store) DeleteReviewNote(ctx context.Context, project, agent string, id int64) error {
	return s.oneNote(s.db.ExecContext(ctx, `DELETE FROM review_notes WHERE project = ? AND agent = ? AND id = ?`, project, agent, id))
}

// MarkReviewNoteSent records a draft as sent at at, with its file's hash
// then.
func (s *Store) MarkReviewNoteSent(ctx context.Context, project, agent string, id int64, hash string, at time.Time) error {
	return s.oneNote(s.db.ExecContext(ctx,
		`UPDATE review_notes SET sent_at = ?, sent_hash = ? WHERE project = ? AND agent = ? AND id = ? AND sent_at = 0`, at.UnixMilli(), hash, project, agent, id))
}

// oneNote is ErrNotFound when an update or delete reached no note.
func (s *Store) oneNote(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// removeReviewNotes drops an agent's notes with it: its name may go to
// another agent.
func (s *Store) removeReviewNotes(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM review_notes WHERE project = ? AND agent = ?`, project, agent)
	return err
}

// milliTime reads back what millis wrote: 0 is the zero time.
func milliTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
