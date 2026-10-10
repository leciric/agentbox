package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"agentbox/internal/redact"
)

// Scrubbing what was stored before secrets were removed on the way in.
//
// The normalisers in memory.go keep every new write free of secrets, but rows
// written before they did still hold whatever an agent pasted: a token in a
// report's summary, a password in an event's payload. Tidy runs the same
// redaction over a project's existing rows and rewrites the ones it changes,
// and only those: a row with nothing to remove is left byte for byte, so
// running it again finds nothing.

// Scrubbed is how many rows of each kind had secrets removed, or would have.
type Scrubbed struct {
	Events        int `json:"events"`
	Memories      int `json:"memories"`
	Reports       int `json:"reports"`
	Artifacts     int `json:"artifacts"`
	WorkingMemory int `json:"workingMemory"`
}

// Total is how many rows changed, of every kind.
func (s Scrubbed) Total() int {
	return s.Events + s.Memories + s.Reports + s.Artifacts + s.WorkingMemory
}

// scrubbable is a table whose columns are written through the normalisers:
// text columns through text or redact.Secrets, JSON ones through
// jsonDocument or jsonList. fts is its full-text index, and indexed are the
// index's columns, in order, when a rewrite has to keep it in step itself:
// events and reports, being append-only, have no update trigger.
type scrubbable struct {
	table      string
	text, json []string
	fts        string
	indexed    []string
	count      func(*Scrubbed) *int
}

var scrubbables = []scrubbable{
	{table: "events", json: []string{"payload"},
		fts: "events_fts", indexed: []string{"type", "payload"},
		count: func(s *Scrubbed) *int { return &s.Events }},
	// memories_fts_update reindexes a memory on any update.
	{table: "memories", text: []string{"title", "content", "resolved_by"}, fts: "memories_fts",
		count: func(s *Scrubbed) *int { return &s.Memories }},
	{table: "agent_reports", text: []string{"task", "summary"},
		json: []string{"discoveries", "decisions", "remaining_issues", "artifacts"},
		fts:  "reports_fts", indexed: []string{"task", "summary", "discoveries", "decisions", "remaining_issues"},
		count: func(s *Scrubbed) *int { return &s.Reports }},
	{table: "artifacts", text: []string{"path"}, json: []string{"metadata"},
		count: func(s *Scrubbed) *int { return &s.Artifacts }},
	{table: "working_memory", json: []string{"data"},
		count: func(s *Scrubbed) *int { return &s.WorkingMemory }},
}

// scrubbedRow is one row a scrub changes: its columns as they are, and as
// they will be.
type scrubbedRow struct {
	rowid    int64
	old, new map[string]string
}

// scrub counts the project's rows that still hold a secret and, with apply,
// rewrites them. A row that changed between the read and the write is left
// for the next run rather than overwritten: the update matches the values it
// read.
func (s *Store) scrub(ctx context.Context, project string, apply bool) (Scrubbed, error) {
	var out Scrubbed
	for _, t := range scrubbables {
		rows, err := s.scrubbedRows(ctx, project, t)
		if err != nil {
			return out, fmt.Errorf("reading %s: %w", t.table, err)
		}
		if !apply {
			*t.count(&out) = len(rows)
			continue
		}
		n, err := s.rewrite(ctx, project, t, rows)
		if err != nil {
			return out, fmt.Errorf("scrubbing %s: %w", t.table, err)
		}
		*t.count(&out) = n
	}
	return out, nil
}

// scrubbedRows are the rows of t that redaction changes.
func (s *Store) scrubbedRows(ctx context.Context, project string, t scrubbable) ([]scrubbedRow, error) {
	cols := t.columns()
	rows, err := s.db.QueryContext(ctx,
		`SELECT rowid, `+strings.Join(cols, ", ")+` FROM `+t.table+` WHERE project = ?`, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []scrubbedRow
	for rows.Next() {
		var rowid int64
		values := make([]string, len(cols))
		dest := []any{&rowid}
		for i := range values {
			dest = append(dest, &values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		r := scrubbedRow{rowid: rowid, old: map[string]string{}, new: map[string]string{}}
		changed := false
		for i, c := range cols {
			clean := scrubColumn(t, c, values[i])
			r.old[c], r.new[c] = values[i], clean
			changed = changed || clean != values[i]
		}
		if changed {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// rewrite writes rows back, in one transaction, and answers how many it
// changed. FTS5 deletes a row's terms by recording that they are gone, and
// keeps the terms themselves in its segments until they are merged, so a
// rewritten table's index is optimized after: otherwise the secret would
// stay in the file, unsearchable but there.
func (s *Store) rewrite(ctx context.Context, project string, t scrubbable, rows []scrubbedRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	scrubbed := slices.Concat(t.text, t.json)
	set, match := make([]string, len(scrubbed)), make([]string, len(scrubbed))
	for i, c := range scrubbed {
		set[i], match[i] = c+" = ?", c+" = ?"
	}
	update := `UPDATE ` + t.table + ` SET ` + strings.Join(set, ", ") +
		` WHERE rowid = ? AND project = ? AND ` + strings.Join(match, " AND ")
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(t.indexed)), ", ")
	unindex := `INSERT INTO ` + t.fts + ` (` + t.fts + `, rowid, ` + strings.Join(t.indexed, ", ") + `) VALUES ('delete', ?, ` + marks + `)`
	index := `INSERT INTO ` + t.fts + ` (rowid, ` + strings.Join(t.indexed, ", ") + `) VALUES (?, ` + marks + `)`
	n := 0
	for _, r := range rows {
		args := make([]any, 0, 2*len(scrubbed)+2)
		for _, c := range scrubbed {
			args = append(args, r.new[c])
		}
		args = append(args, r.rowid, project)
		for _, c := range scrubbed {
			args = append(args, r.old[c])
		}
		res, err := tx.ExecContext(ctx, update, args...)
		if err != nil {
			return 0, err
		}
		if changed, err := res.RowsAffected(); err != nil {
			return 0, err
		} else if changed == 0 {
			continue // written since it was read: the next run looks again
		}
		n++
		if len(t.indexed) == 0 {
			continue
		}
		// Kept in step by hand: the table's triggers don't cover updates.
		if _, err := tx.ExecContext(ctx, unindex, r.values(r.old, t.indexed)...); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, index, r.values(r.new, t.indexed)...); err != nil {
			return 0, err
		}
	}
	if n > 0 && t.fts != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+t.fts+` (`+t.fts+`) VALUES ('optimize')`); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit()
}

// columns are what a scrub of t reads: the columns it rewrites and the
// ones its index needs.
func (t scrubbable) columns() []string {
	cols := slices.Concat(t.text, t.json)
	for _, c := range t.indexed {
		if !slices.Contains(cols, c) {
			cols = append(cols, c)
		}
	}
	return cols
}

func (r scrubbedRow) values(from map[string]string, cols []string) []any {
	out := []any{r.rowid}
	for _, c := range cols {
		out = append(out, from[c])
	}
	return out
}

// scrubColumn is one stored value with its secrets removed, the way it was
// normalised on the way in. A JSON column that somehow doesn't hold JSON is
// scrubbed as text: there is no document to break.
func scrubColumn(t scrubbable, col, value string) string {
	switch {
	case slices.Contains(t.text, col):
		return redact.Secrets(value)
	case slices.Contains(t.json, col):
		if !json.Valid([]byte(value)) {
			return redact.Secrets(value)
		}
		clean, err := scrubJSON(json.RawMessage(value))
		if err != nil {
			return redact.Secrets(value)
		}
		return clean
	}
	return value // only read for the index
}
