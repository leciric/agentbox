package state

import (
	"context"
	"fmt"
	"time"
)

// Media is something kept as proof of an agent's work: a screenshot, recording,
// report, log, note or file. Items are added and deleted, never changed.
type Media struct {
	ID        string
	Project   string
	Agent     string
	Kind      string
	Name      string
	File      string // relative to the agent's media directory; empty for notes
	Mime      string
	Size      int64
	SHA256    string
	Source    string // "agent" or "user"
	Text      string // a note's text
	Meta      string // JSON details that depend on the kind
	CreatedAt time.Time
	// OrphanedAt is when this item's agent was destroyed and the item was
	// kept, which is when its retention clock starts. Zero means its agent
	// still exists, so it never expires.
	OrphanedAt time.Time
}

func (m Media) Ref() string { return m.Project + "/" + m.Agent }

const mediaColumns = `id, project, agent, kind, name, file, mime, size, sha256, source, text, meta, created_at, orphaned_at`

func (s *Store) AddMedia(ctx context.Context, m Media) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO media (`+mediaColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Project, m.Agent, m.Kind, m.Name, m.File, m.Mime, m.Size, m.SHA256, m.Source, m.Text, m.Meta, m.CreatedAt.UnixMilli(), millis(m.OrphanedAt))
	return err
}

// millis is a nullable Unix millisecond timestamp: 0 for a zero time, so it
// round-trips through OrphanedAt's "still has an agent" meaning.
func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// Media lists an agent's items, newest first.
func (s *Store) Media(ctx context.Context, project, agent string) ([]Media, error) {
	// rowid breaks ties between items added in the same millisecond.
	return s.queryMedia(ctx, `WHERE project = ? AND agent = ? ORDER BY created_at DESC, rowid DESC`, project, agent)
}

// ProjectMedia lists every agent's items for a project, newest first, so the
// app can show one stream and filter it by agent.
func (s *Store) ProjectMedia(ctx context.Context, project string) ([]Media, error) {
	return s.queryMedia(ctx, `WHERE project = ? ORDER BY created_at DESC, rowid DESC`, project)
}

func (s *Store) MediaItem(ctx context.Context, id string) (Media, error) {
	items, err := s.queryMedia(ctx, `WHERE id = ?`, id)
	if err != nil {
		return Media{}, err
	}
	if len(items) == 0 {
		return Media{}, fmt.Errorf("media %s: %w", id, ErrNotFound)
	}
	return items[0], nil
}

func (s *Store) DeleteMedia(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM media WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("media %s: %w", id, ErrNotFound)
	}
	return nil
}

func (s *Store) DeleteAgentMedia(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM media WHERE project = ? AND agent = ?`, project, agent)
	return err
}

// OrphanAgentMedia marks an agent's media as kept past its agent, starting
// its retention clock at "at" instead of each item's own created_at. Items
// already orphaned (by a previous agent of the same name, since names are
// reused) keep their own clock.
func (s *Store) OrphanAgentMedia(ctx context.Context, project, agent string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE media SET orphaned_at = ? WHERE project = ? AND agent = ? AND orphaned_at = 0`, at.UnixMilli(), project, agent)
	return err
}

// ExpiredMedia lists kept media whose agent is gone and whose project's
// retention period, counted from when it was orphaned, has passed as of now.
// Media whose agent still exists is never included.
func (s *Store) ExpiredMedia(ctx context.Context, now time.Time) ([]Media, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.project, m.agent, m.kind, m.name, m.file, m.mime, m.size, m.sha256, m.source, m.text, m.meta,
		        m.created_at, m.orphaned_at, COALESCE(p.media_retention_days, ?)
		 FROM media m
		 LEFT JOIN projects p ON p.name = m.project
		 WHERE m.orphaned_at > 0`, DefaultMediaRetentionDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Media
	for rows.Next() {
		var m Media
		var created, orphaned int64
		var retentionDays int
		if err := rows.Scan(&m.ID, &m.Project, &m.Agent, &m.Kind, &m.Name, &m.File, &m.Mime, &m.Size,
			&m.SHA256, &m.Source, &m.Text, &m.Meta, &created, &orphaned, &retentionDays); err != nil {
			return nil, err
		}
		m.CreatedAt = time.UnixMilli(created)
		m.OrphanedAt = time.UnixMilli(orphaned)
		if now.Sub(m.OrphanedAt) >= time.Duration(retentionDays)*24*time.Hour {
			items = append(items, m)
		}
	}
	return items, rows.Err()
}

func (s *Store) queryMedia(ctx context.Context, clause string, args ...any) ([]Media, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mediaColumns+` FROM media `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Media
	for rows.Next() {
		var m Media
		var created, orphaned int64
		if err := rows.Scan(&m.ID, &m.Project, &m.Agent, &m.Kind, &m.Name, &m.File, &m.Mime, &m.Size,
			&m.SHA256, &m.Source, &m.Text, &m.Meta, &created, &orphaned); err != nil {
			return nil, err
		}
		m.CreatedAt = time.UnixMilli(created)
		if orphaned > 0 {
			m.OrphanedAt = time.UnixMilli(orphaned)
		}
		items = append(items, m)
	}
	return items, rows.Err()
}
