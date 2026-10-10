package state

import (
	"context"
	"strings"
	"time"
)

// Notification is one thing the app told the user about: an agent of some
// project finishing, asking, or keeping a screenshot or recording. Like an
// agent event, the row holds what it is about and when, and the rest as JSON
// package daemon decides the shape of (api.Notification). Unlike one, it
// outlives its agent: the history is for what the user missed, and an agent
// destroyed since is part of that.
type Notification struct {
	ID        string
	Project   string
	Agent     string
	Kind      string
	MediaID   string // the media item it's about, if any
	CreatedAt time.Time
	SeenAt    time.Time // zero while unseen
	Data      []byte    // JSON
}

// KeepNotifications is how long a notification stays in the history, and
// keepNotificationRows how many of them at most.
const (
	KeepNotifications    = 30 * 24 * time.Hour
	keepNotificationRows = 500
)

// AddNotification stores one and drops those past KeepNotifications.
func (s *Store) AddNotification(ctx context.Context, n Notification) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO notifications (id, project, agent, kind, media_id, created_at, seen_at, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET data = excluded.data`,
		n.ID, n.Project, n.Agent, n.Kind, n.MediaID, n.CreatedAt.UnixMilli(), millis(n.SeenAt), string(n.Data)); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM notifications WHERE created_at < ?`, n.CreatedAt.Add(-KeepNotifications).UnixMilli())
	return err
}

// Notifications lists the history, newest first: every project's still
// there, at most keepNotificationRows, none older than KeepNotifications.
func (s *Store) Notifications(ctx context.Context, now time.Time) ([]Notification, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT n.id, n.project, n.agent, n.kind, n.media_id, n.created_at, n.seen_at, n.data FROM notifications n
		WHERE n.created_at >= ? AND EXISTS (SELECT 1 FROM projects p WHERE p.name = n.project)
		ORDER BY n.created_at DESC, n.rowid DESC LIMIT ?`, now.Add(-KeepNotifications).UnixMilli(), keepNotificationRows)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Notification
	for rows.Next() {
		var n Notification
		var created, seen int64
		var data string
		if err := rows.Scan(&n.ID, &n.Project, &n.Agent, &n.Kind, &n.MediaID, &created, &seen, &data); err != nil {
			return nil, err
		}
		n.CreatedAt, n.Data = time.UnixMilli(created), []byte(data)
		if seen > 0 {
			n.SeenAt = time.UnixMilli(seen)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UnseenMedia is which of the media items with a notification haven't been
// seen yet, by item ID.
func (s *Store) UnseenMedia(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT media_id FROM notifications WHERE media_id != '' AND seen_at = 0`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SeeNotifications marks notifications seen at "at": those with the given
// IDs, those about the given media items, with allMedia every one about a
// media item, or, with all, every one. It returns how many it changed.
func (s *Store) SeeNotifications(ctx context.Context, ids, media []string, all, allMedia bool, at time.Time) (int, error) {
	var where []string
	var args []any
	args = append(args, at.UnixMilli())
	if all {
		where = append(where, "1")
	}
	if allMedia {
		where = append(where, "media_id != ''")
	}
	if len(ids) > 0 {
		where = append(where, "id IN (?"+strings.Repeat(", ?", len(ids)-1)+")")
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if len(media) > 0 {
		where = append(where, "media_id IN (?"+strings.Repeat(", ?", len(media)-1)+")")
		for _, id := range media {
			args = append(args, id)
		}
	}
	if len(where) == 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `UPDATE notifications SET seen_at = ? WHERE seen_at = 0 AND (`+strings.Join(where, " OR ")+`)`, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// AllMedia lists every project's items, newest first, for the app's Media
// view across projects; only those of the given kinds when kinds isn't empty.
func (s *Store) AllMedia(ctx context.Context, kinds []string) ([]Media, error) {
	if len(kinds) == 0 {
		return s.queryMedia(ctx, `ORDER BY created_at DESC, rowid DESC`)
	}
	args := make([]any, len(kinds))
	for i, k := range kinds {
		args[i] = k
	}
	return s.queryMedia(ctx, `WHERE kind IN (?`+strings.Repeat(", ?", len(kinds)-1)+`) ORDER BY created_at DESC, rowid DESC`, args...)
}
