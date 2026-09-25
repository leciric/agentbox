package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// WorkingMemory is what a project is doing right now: one small document, kept
// current rather than appended to. It is the thing a context builder puts at
// the top of a brief, so it has to stay short enough to always fit.
type WorkingMemory struct {
	// Goal is what the project is trying to achieve at the moment, over and
	// above any one task.
	Goal string `json:"goal,omitempty"`
	// CurrentTask is what is being worked on now.
	CurrentTask string `json:"currentTask,omitempty"`
	// ActiveAgents are the agents on it, by name.
	ActiveAgents []string `json:"activeAgents,omitempty"`
	// Blockers are what is in the way.
	Blockers []string `json:"blockers,omitempty"`
	// Notes is anything else that matters today and won't next month.
	Notes string `json:"notes,omitempty"`
	// UpdatedAt is when it was last written; zero for a project that has
	// never had any.
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// WorkingMemoryPatch is a merge patch: a field that is nil is left as it is,
// and a field that is set replaces what was there. An empty string or an empty
// list clears its field — the two are the same operation, so there is no
// separate "clear" and nothing has to guess what "" meant.
type WorkingMemoryPatch struct {
	Goal         *string   `json:"goal,omitempty"`
	CurrentTask  *string   `json:"currentTask,omitempty"`
	ActiveAgents *[]string `json:"activeAgents,omitempty"`
	Blockers     *[]string `json:"blockers,omitempty"`
	Notes        *string   `json:"notes,omitempty"`
}

// Empty reports whether the patch asks for nothing.
func (p WorkingMemoryPatch) Empty() bool {
	return p.Goal == nil && p.CurrentTask == nil && p.ActiveAgents == nil && p.Blockers == nil && p.Notes == nil
}

// maxWorkingField keeps working memory the size of a paragraph. It is meant to
// be read in full, every time, by everything.
const maxWorkingField = 2000

// WorkingMemory is a project's, empty for a project that has never written
// any. A project with nothing to say about itself is the ordinary case, not an
// error.
func (s *Store) WorkingMemory(ctx context.Context, project string) (WorkingMemory, error) {
	if err := requireProject(project); err != nil {
		return WorkingMemory{}, err
	}
	var data string
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT data, updated_at FROM working_memory WHERE project = ?`, project).Scan(&data, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkingMemory{}, nil
	}
	if err != nil {
		return WorkingMemory{}, err
	}
	var w WorkingMemory
	if err := json.Unmarshal([]byte(data), &w); err != nil {
		// A document this row can't parse is worth nothing and shouldn't
		// break every read of the project; the next write replaces it.
		return WorkingMemory{UpdatedAt: attime(updated)}, nil
	}
	w.UpdatedAt = attime(updated)
	return w, nil
}

// SetWorkingMemory applies a merge patch and returns the document as it now
// stands. Several agents write this — a patch that only names the field it
// knows about is why it is a patch and not a replace.
func (s *Store) SetWorkingMemory(ctx context.Context, project string, patch WorkingMemoryPatch) (WorkingMemory, error) {
	if err := requireProject(project); err != nil {
		return WorkingMemory{}, err
	}
	if patch.Empty() {
		return s.WorkingMemory(ctx, project)
	}
	current, err := s.WorkingMemory(ctx, project)
	if err != nil {
		return WorkingMemory{}, err
	}
	if patch.Goal != nil {
		if current.Goal, err = text("the goal", *patch.Goal, maxWorkingField); err != nil {
			return WorkingMemory{}, err
		}
	}
	if patch.CurrentTask != nil {
		if current.CurrentTask, err = text("the current task", *patch.CurrentTask, maxWorkingField); err != nil {
			return WorkingMemory{}, err
		}
	}
	if patch.Notes != nil {
		if current.Notes, err = text("the notes", *patch.Notes, maxWorkingField); err != nil {
			return WorkingMemory{}, err
		}
	}
	if patch.ActiveAgents != nil {
		current.ActiveAgents = cleanList(*patch.ActiveAgents)
	}
	if patch.Blockers != nil {
		current.Blockers = cleanList(*patch.Blockers)
	}
	now := time.Now()
	current.UpdatedAt = stamp(now)
	data, err := json.Marshal(current)
	if err != nil {
		return WorkingMemory{}, err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO working_memory (project, data, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (project) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		project, string(data), now.UnixMilli())
	if err != nil {
		return WorkingMemory{}, err
	}
	return current, nil
}

func cleanList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
