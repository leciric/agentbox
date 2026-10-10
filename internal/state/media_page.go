package state

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// MediaFilter narrows the media a Media view reads. Empty fields don't
// narrow anything.
type MediaFilter struct {
	Project  string
	Agent    string   // within Project
	Kinds    []string // any of these
	Favorite bool     // favorites only
	Unseen   bool     // only those whose notification hasn't been seen
}

// unseenMedia is the condition for an item a notification told the user
// about and they haven't seen yet (UnseenMedia).
const unseenMedia = `id IN (SELECT media_id FROM notifications WHERE media_id != '' AND seen_at = 0)`

func (f MediaFilter) where() ([]string, []any) {
	var where []string
	var args []any
	if f.Project != "" {
		where, args = append(where, "project = ?"), append(args, f.Project)
	}
	if f.Agent != "" {
		where, args = append(where, "agent = ?"), append(args, f.Agent)
	}
	if len(f.Kinds) > 0 {
		where = append(where, "kind IN (?"+strings.Repeat(", ?", len(f.Kinds)-1)+")")
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if f.Favorite {
		where = append(where, "favorite = 1")
	}
	if f.Unseen {
		where = append(where, unseenMedia)
	}
	return where, args
}

// MediaCursor is where a page of media ends: the last item's time and ID, the
// order EachMedia reads in. Items added or deleted while someone scrolls
// don't move it, so no page repeats or skips one.
func MediaCursor(m Media) string {
	return strconv.FormatInt(m.CreatedAt.UnixMilli(), 10) + "." + m.ID
}

// ErrBadCursor is a cursor MediaCursor didn't make.
var ErrBadCursor = errors.New("invalid media cursor")

func parseMediaCursor(cursor string) (int64, string, error) {
	at, id, ok := strings.Cut(cursor, ".")
	ms, err := strconv.ParseInt(at, 10, 64)
	if !ok || err != nil || id == "" {
		return 0, "", ErrBadCursor
	}
	return ms, id, nil
}

// EachMedia calls fn with the items the filter keeps, newest first, starting
// after cursor (from the newest when it's empty), until fn returns false or
// they run out. Rows are read as fn asks for them, so a page stops reading
// where it ends: a search the database can't do (the daemon's) still pages
// correctly by skipping what it doesn't match.
func (s *Store) EachMedia(ctx context.Context, f MediaFilter, cursor string, fn func(Media) bool) error {
	where, args := f.where()
	if cursor != "" {
		ms, id, err := parseMediaCursor(cursor)
		if err != nil {
			return err
		}
		where = append(where, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, ms, ms, id)
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+mediaColumns+` FROM media `+clause+` ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m Media
		var created, orphaned int64
		if err := rows.Scan(&m.ID, &m.Project, &m.Agent, &m.Kind, &m.Name, &m.File, &m.Mime, &m.Size,
			&m.SHA256, &m.Source, &m.Text, &m.Meta, &created, &orphaned, &m.Favorite); err != nil {
			return err
		}
		m.CreatedAt = time.UnixMilli(created)
		if orphaned > 0 {
			m.OrphanedAt = time.UnixMilli(orphaned)
		}
		if !fn(m) {
			break
		}
	}
	return rows.Err()
}

// MediaTally is how many items one agent has of one kind, and what they add
// up to, for the counts on a Media view's filters.
type MediaTally struct {
	Project, Agent, Kind string
	Count                int
	Bytes                int64
	Favorites            int
	Unseen               int
	Newest               time.Time
}

// MediaTallies counts the items the filter keeps by project, agent and kind,
// newest group first, without reading any item.
func (s *Store) MediaTallies(ctx context.Context, f MediaFilter) ([]MediaTally, error) {
	where, args := f.where()
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT project, agent, kind, COUNT(*), COALESCE(SUM(size), 0), COALESCE(SUM(favorite), 0),
		COALESCE(SUM(`+unseenMedia+`), 0), MAX(created_at)
		FROM media `+clause+` GROUP BY project, agent, kind ORDER BY MAX(created_at) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MediaTally
	for rows.Next() {
		var t MediaTally
		var newest int64
		if err := rows.Scan(&t.Project, &t.Agent, &t.Kind, &t.Count, &t.Bytes, &t.Favorites, &t.Unseen, &newest); err != nil {
			return nil, err
		}
		t.Newest = time.UnixMilli(newest)
		out = append(out, t)
	}
	return out, rows.Err()
}
