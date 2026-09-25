package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var errNoType = errors.New("an event needs a type, like \"test_failed\" or \"pr_opened\"")

func notFound(what, id string) error { return fmt.Errorf("%s %s: %w", what, id, ErrNotFound) }

// AddMemory writes down something worth keeping. Title and kind are required:
// a memory nobody can recognise in a list is one nobody will read.
func (s *Store) AddMemory(ctx context.Context, m Memory) (Memory, error) {
	if err := requireProject(m.Project); err != nil {
		return Memory{}, err
	}
	if m.Kind == "" {
		m.Kind = KindProject
	}
	if err := oneOf("a memory's kind", m.Kind, Kinds); err != nil {
		return Memory{}, err
	}
	title, err := text("a memory's title", m.Title, MaxTitleLen)
	if err != nil {
		return Memory{}, err
	}
	if title == "" {
		return Memory{}, errors.New("a memory needs a title: one line somebody can recognise it by in a list")
	}
	content, err := text("a memory's content", m.Content, MaxContentLen)
	if err != nil {
		return Memory{}, err
	}
	m.Title, m.Content = title, content
	if m.Importance == 0 {
		m.Importance = DefaultImportance
	}
	if m.Importance < MinImportance || m.Importance > MaxImportance {
		return Memory{}, fmt.Errorf("importance is %d: it is %d (worth knowing) to %d (nobody should work on this project without it)",
			m.Importance, MinImportance, MaxImportance)
	}
	if m.ID == "" {
		m.ID = newID("mem")
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = m.CreatedAt
	}
	m.CreatedAt, m.UpdatedAt = stamp(m.CreatedAt), stamp(m.UpdatedAt)
	// A memory may replace one as it is written, which is the common case:
	// the thing being corrected is what prompted the new memory.
	if m.SupersedesID != "" {
		old, err := s.Memory(ctx, m.Project, m.SupersedesID)
		if err != nil {
			return Memory{}, err
		}
		if old.ID == m.ID {
			return Memory{}, errors.New("a memory can't supersede itself")
		}
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO memories (id, project, kind, title, content, importance, created_at, updated_at, supersedes_id, source_event_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Project, m.Kind, m.Title, m.Content, m.Importance,
		m.CreatedAt.UnixMilli(), m.UpdatedAt.UnixMilli(), nullable(m.SupersedesID), nullable(m.SourceEventID))
	if err != nil {
		return Memory{}, err
	}
	return m, nil
}

// SupersedeMemory says that one memory replaces another: the new one keeps the
// pointer, and the old one stops coming back from Memories and Search. Nothing
// is deleted — the old memory is still there by id, and still reachable
// through the new one's SupersedesID, so how the project's understanding
// changed is readable afterwards.
func (s *Store) SupersedeMemory(ctx context.Context, project, old, replacement string) error {
	if old == replacement {
		return errors.New("a memory can't supersede itself")
	}
	if _, err := s.Memory(ctx, project, old); err != nil {
		return err
	}
	current, err := s.Memory(ctx, project, replacement)
	if err != nil {
		return err
	}
	if current.SupersedesID != "" && current.SupersedesID != old {
		return fmt.Errorf("%s already supersedes %s: write a new memory rather than moving this one", replacement, current.SupersedesID)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE memories SET supersedes_id = ?, updated_at = ? WHERE project = ? AND id = ?`,
		old, time.Now().UnixMilli(), project, replacement)
	return err
}

// Memories are a project's live memories, newest first, of the kinds asked
// for; no kinds is every kind. Superseded and resolved memories are left out:
// something later says they are no longer true, and a list that returns both
// is a list nobody can act on. Memory reads one by id, live or not.
//
// It returns at most MaxLimit of them: everything here is read into a model's
// context, and a project with a thousand memories needs Search, not a list.
func (s *Store) Memories(ctx context.Context, project string, kinds []string) ([]Memory, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	where := []string{"m.project = ?", live}
	args := []any{project}
	if len(kinds) > 0 {
		for _, k := range kinds {
			if err := oneOf("a memory's kind", k, Kinds); err != nil {
				return nil, err
			}
		}
		where = append(where, "m.kind IN ("+placeholders(len(kinds))+")")
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	// Importance first, then recency: what a project can't be worked on
	// without comes before what merely happened most recently.
	return s.queryMemories(ctx, `WHERE `+strings.Join(where, " AND ")+
		` ORDER BY m.importance DESC, m.created_at DESC, m.rowid DESC LIMIT ?`, append(args, MaxLimit)...)
}

// Memory is one memory by id, whether or not something supersedes it and
// whether or not it has been resolved.
func (s *Store) Memory(ctx context.Context, project, id string) (Memory, error) {
	if err := requireProject(project); err != nil {
		return Memory{}, err
	}
	rows, err := s.queryMemories(ctx, `WHERE m.project = ? AND m.id = ?`, project, id)
	if err != nil {
		return Memory{}, err
	}
	if len(rows) == 0 {
		return Memory{}, notFound("memory", id)
	}
	return rows[0], nil
}

// LatestFrom is the newest live memory of a kind that was learned from an
// event of a type: the memory a writer of both can find again without keeping
// an id of its own. It answers ErrNotFound when the project has none.
//
// It exists for the conversation the daemon compacts (D73), which writes one
// event and, from it, the narrative of everything up to that point. The recap
// the next session starts with has to find that narrative, and "the newest
// memory this writer wrote" is the only thing that identifies it: a title can
// be written by anyone, and a kind is shared with every other episodic memory.
func (s *Store) LatestFrom(ctx context.Context, project, kind, eventType string) (Memory, error) {
	if err := requireProject(project); err != nil {
		return Memory{}, err
	}
	if err := oneOf("a memory's kind", kind, Kinds); err != nil {
		return Memory{}, err
	}
	if eventType == "" {
		return Memory{}, errNoType
	}
	rows, err := s.queryMemories(ctx, `JOIN events e ON e.id = m.source_event_id
		WHERE m.project = ? AND m.kind = ? AND e.type = ? AND `+live+`
		ORDER BY m.created_at DESC, m.rowid DESC LIMIT 1`, project, kind, eventType)
	if err != nil {
		return Memory{}, err
	}
	if len(rows) == 0 {
		return Memory{}, fmt.Errorf("no %s memory from a %s event: %w", kind, eventType, ErrNotFound)
	}
	return rows[0], nil
}

// notSuperseded is the condition that leaves out a memory another one
// replaces. supersedes_id on the newer row is the only record of that, so
// this is a reverse lookup rather than a flag two writes could disagree on;
// memories_superseded is the index it uses.
const notSuperseded = `NOT EXISTS (SELECT 1 FROM memories r WHERE r.supersedes_id = m.id)`

// live is what a listing and a search return: nothing has replaced it, and
// nobody has closed it. The two halves are different facts — a replacement
// says what is true instead, a resolution says nothing is — and an issue
// needs both, because inventing a replacement for a problem that simply went
// away would be a memory of something that never happened (D76).
const live = notSuperseded + ` AND m.resolved_at = 0`

const memoryColumns = `m.id, m.project, m.kind, m.title, m.content, m.importance, m.created_at, m.updated_at,
	m.supersedes_id, m.source_event_id, EXISTS (SELECT 1 FROM memories r WHERE r.supersedes_id = m.id),
	m.resolved_at, m.resolved_by, m.referenced_at, m.decayed_at`

func (s *Store) queryMemories(ctx context.Context, clause string, args ...any) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+memoryColumns+` FROM memories m `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanMemory(rows scanner) (Memory, error) {
	var m Memory
	var created, updated int64
	var supersedes, source *string
	var resolved, referenced, decayed int64
	if err := rows.Scan(&m.ID, &m.Project, &m.Kind, &m.Title, &m.Content, &m.Importance,
		&created, &updated, &supersedes, &source, &m.Superseded,
		&resolved, &m.ResolvedBy, &referenced, &decayed); err != nil {
		return Memory{}, err
	}
	m.CreatedAt, m.UpdatedAt = attime(created), attime(updated)
	m.ResolvedAt, m.ReferencedAt, m.DecayedAt = attime(resolved), attime(referenced), attime(decayed)
	if supersedes != nil {
		m.SupersedesID = *supersedes
	}
	if source != nil {
		m.SourceEventID = *source
	}
	return m, nil
}

// nullable stores an empty string as NULL, so "no memory replaced" is absent
// rather than a row whose supersedes_id is "".
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
