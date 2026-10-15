package state

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"
)

// ProjectIcon is a project's project_icons row.
type ProjectIcon struct {
	// Chosen is what the user picked: "" for nothing, "emoji" or "image".
	Chosen string
	Emoji  string
	Image  []byte
	MIME   string
	// Detected is the icon found in the repository or at its GitHub owner,
	// nil when the last look found nothing.
	Detected     []byte
	DetectedMIME string
	DetectedFrom string
	// DetectedAt is when it was last looked for; zero when it never was.
	DetectedAt time.Time
	// UpdatedAt changes whenever what the project shows does.
	UpdatedAt time.Time
}

// ProjectIcon returns a project's icon row; ok is false when it has none.
func (s *Store) ProjectIcon(ctx context.Context, project string) (ProjectIcon, bool, error) {
	var ic ProjectIcon
	var detectedAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT chosen, emoji, image, mime, detected, detected_mime, detected_from, detected_at, updated_at
		FROM project_icons WHERE project = ?`, project).Scan(&ic.Chosen, &ic.Emoji, &ic.Image, &ic.MIME,
		&ic.Detected, &ic.DetectedMIME, &ic.DetectedFrom, &detectedAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectIcon{}, false, nil
	}
	if err != nil {
		return ProjectIcon{}, false, err
	}
	if detectedAt != 0 {
		ic.DetectedAt = time.UnixMilli(detectedAt)
	}
	if updatedAt != 0 {
		ic.UpdatedAt = time.UnixMilli(updatedAt)
	}
	return ic, true, nil
}

// ProjectIconVersion is a project's icon's updated_at in unix ms, 0 when it
// has no row: what the app keys its copy of the icon by.
func (s *Store) ProjectIconVersion(ctx context.Context, project string) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT updated_at FROM project_icons WHERE project = ?`, project).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// ChooseProjectIcon keeps the icon the user picked: an emoji, or an image
// with its type. chosen is "emoji" or "image"; "" clears the choice, which
// leaves the detected icon showing.
func (s *Store) ChooseProjectIcon(ctx context.Context, project, chosen, emoji string, image []byte, mime string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO project_icons (project, chosen, emoji, image, mime, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (project) DO UPDATE SET chosen = excluded.chosen, emoji = excluded.emoji, image = excluded.image,
			mime = excluded.mime, updated_at = max(excluded.updated_at, updated_at + 1)`,
		project, chosen, emoji, image, mime, now.UnixMilli())
	return err
}

// SaveDetectedProjectIcon keeps what a look for a project's icon found, data
// nil for nothing. updated_at moves only when the detected icon changed: a
// look that finds the same icon again leaves the app's copy good.
func (s *Store) SaveDetectedProjectIcon(ctx context.Context, project string, data []byte, mime, from string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var old []byte
	var oldMIME string
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT detected, detected_mime FROM project_icons WHERE project = ?`, project).Scan(&old, &oldMIME)
	switch {
	case err == nil:
		exists = true
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	changed := !exists || !bytes.Equal(old, data) || oldMIME != mime
	ms := now.UnixMilli()
	if !exists {
		_, err = tx.ExecContext(ctx, `INSERT INTO project_icons (project, detected, detected_mime, detected_from, detected_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, project, data, mime, from, ms, ms)
	} else {
		// Never backwards, and never the same value twice for two different
		// icons, which the app's cache would mistake for one.
		update := `updated_at`
		args := []any{data, mime, from, ms}
		if changed {
			update = `max(?, updated_at + 1)`
			args = append(args, ms)
		}
		args = append(args, project)
		_, err = tx.ExecContext(ctx, `UPDATE project_icons SET detected = ?, detected_mime = ?, detected_from = ?, detected_at = ?,
			updated_at = `+update+` WHERE project = ?`, args...)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ProjectActivity is when a project last did anything, and when the user
// last wrote to it, read from its chats: its lead's and every agent's.
type ProjectActivity struct {
	Project string
	// LastActivity is the createdAt of its newest chat item of any kind,
	// "" when it has none.
	LastActivity string
	// LastMessage is the createdAt of the newest message the user wrote to
	// it: a user item, not one AgentBox woke the chat with, nor hidden.
	LastMessage string
}

// ProjectsActivity returns every project's activity that has a chat item.
// Each maximum is a correlated lookup on chat_items_created or
// chat_items_user_created (the migration's WHERE, repeated verbatim here),
// so the cost is per project, not per item.
func (s *Store) ProjectsActivity(ctx context.Context) ([]ProjectActivity, error) {
	rows, err := s.db.QueryContext(ctx, projectsActivityQuery)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProjectActivity
	for rows.Next() {
		var a ProjectActivity
		var activity, message sql.NullString
		if err := rows.Scan(&a.Project, &activity, &message); err != nil {
			return nil, err
		}
		if !activity.Valid {
			continue
		}
		a.LastActivity, a.LastMessage = activity.String, message.String
		out = append(out, a)
	}
	return out, rows.Err()
}

const projectsActivityQuery = `SELECT p.name,
	(SELECT json_extract(data, '$.createdAt') FROM chat_items INDEXED BY chat_items_created
		WHERE project = p.name AND julianday(CASE WHEN json_valid(data) THEN json_extract(data, '$.createdAt') END) IS NOT NULL
		ORDER BY julianday(CASE WHEN json_valid(data) THEN json_extract(data, '$.createdAt') END) DESC LIMIT 1),
	(SELECT json_extract(data, '$.createdAt') FROM chat_items INDEXED BY chat_items_user_created
		WHERE project = p.name
			AND json_valid(data) AND json_extract(data, '$.kind') = 'user'
			AND NOT coalesce(json_extract(data, '$.woken'), 0) AND NOT coalesce(json_extract(data, '$.hidden'), 0)
			AND julianday(CASE WHEN json_valid(data) THEN json_extract(data, '$.createdAt') END) IS NOT NULL
		ORDER BY julianday(CASE WHEN json_valid(data) THEN json_extract(data, '$.createdAt') END) DESC LIMIT 1)
	FROM projects p ORDER BY p.name`
