package transfer

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Most of what is exported is rows of state.db, carried as they are: a table's
// name, its columns and its rows, with the primary key that identifies a row
// on any instance. Carrying rows rather than Go structs keeps the format
// independent of the state package's types, and lets an archive from an older
// AgentBox (fewer columns) or a newer one (more) still merge: only the
// columns both sides have are written, and the rest take their defaults.
//
// Merging is by primary key, so importing the same rows twice adds nothing:
// a row whose key is new is added, one whose key is there with the same
// values is skipped, and one whose key is there with other values is a
// conflict, which keeps this instance's row and says so in the report.

// tableRows is one table's rows in an archive.
type tableRows struct {
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
	// Key is the columns a row is matched by; empty when the table's own key
	// is an id local to one instance (token_usage's integer id), so rows are
	// matched by their whole content.
	Key  []string `json:"key,omitempty"`
	Rows [][]any  `json:"rows"`
}

// importedRows is tableRows as read back: values stay JSON until a column is
// known to be written.
type importedRows struct {
	Table   string              `json:"table"`
	Columns []string            `json:"columns"`
	Key     []string            `json:"key,omitempty"`
	Rows    [][]json.RawMessage `json:"rows"`
}

// tableSpec says which of a table's rows belong to a project's section.
type tableSpec struct {
	table string
	// where selects them, with every ? being the project's name.
	where string
	// omit are columns that stay behind: sign-ins, ids local to this
	// instance, what is chosen again on import.
	omit []string
}

type column struct {
	name string
	pk   int // position in the primary key, from 1; 0 when not in it
}

// columns is a table's ordinary columns, generated ones left out (SQLite
// computes those).
func columns(ctx context.Context, q querier, table string) ([]column, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, pk, hidden FROM pragma_table_xinfo(?)`, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cols []column
	for rows.Next() {
		var c column
		var hidden int
		if err := rows.Scan(&c.name, &c.pk, &hidden); err != nil {
			return nil, err
		}
		if hidden == 0 {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 && rows.Err() == nil {
		return nil, fmt.Errorf("this AgentBox has no table %s", table)
	}
	return cols, rows.Err()
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type execer interface {
	querier
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// dump reads a spec's rows for a project.
func dump(ctx context.Context, q querier, spec tableSpec, project string) (tableRows, error) {
	cols, err := columns(ctx, q, spec.table)
	if err != nil {
		return tableRows{}, err
	}
	t := tableRows{Table: spec.table, Rows: [][]any{}}
	var key []column
	keyOmitted := false
	for _, c := range cols {
		if slices.Contains(spec.omit, c.name) {
			keyOmitted = keyOmitted || c.pk > 0
			continue
		}
		t.Columns = append(t.Columns, c.name)
		if c.pk > 0 {
			key = append(key, c)
		}
	}
	if !keyOmitted {
		slices.SortFunc(key, func(a, b column) int { return a.pk - b.pk })
		for _, c := range key {
			t.Key = append(t.Key, c.name)
		}
	}
	args := make([]any, strings.Count(spec.where, "?"))
	for i := range args {
		args[i] = project
	}
	rows, err := q.QueryContext(ctx, `SELECT `+quoteAll(t.Columns)+` FROM `+spec.table+` WHERE `+spec.where+orderBy(t.Key), args...)
	if err != nil {
		return t, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		vals := make([]any, len(t.Columns))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return t, err
		}
		for i, v := range vals {
			vals[i] = encodeValue(v)
		}
		t.Rows = append(t.Rows, vals)
	}
	return t, rows.Err()
}

// orderBy keeps an export of unchanged data byte for byte the same, which a
// sync comparing hashes relies on.
func orderBy(key []string) string {
	if len(key) == 0 {
		return " ORDER BY rowid"
	}
	return " ORDER BY " + quoteAll(key)
}

func quoteAll(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = `"` + c + `"`
	}
	return strings.Join(q, ", ")
}

// encodeValue is a column's value as the archive holds it: JSON's own types,
// and bytes (an icon, a skill's file) as {"b64": "…"}.
func encodeValue(v any) any {
	if b, ok := v.([]byte); ok {
		return map[string]string{"b64": base64.StdEncoding.EncodeToString(b)}
	}
	return v
}

// decodeValue is encodeValue's other half: what to write into a column.
func decodeValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch raw[0] {
	case '"':
		var s string
		err := json.Unmarshal(raw, &s)
		return s, err
	case '{':
		var b struct {
			B64 string `json:"b64"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(b.B64)
	case 't', 'f':
		var v bool
		err := json.Unmarshal(raw, &v)
		return v, err
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, err
	}
	if i, err := n.Int64(); err == nil {
		return i, nil
	}
	return n.Float64()
}

// same compares a value read from this instance with one from the archive.
func same(local, imported any) bool {
	a, _ := json.Marshal(encodeValue(local))
	b, _ := json.Marshal(encodeValue(imported))
	return string(a) == string(b)
}

// rowRewrite changes an imported row before it is matched and written, by
// column; it may drop the row by returning false.
type rowRewrite func(row map[string]any) bool

// merge writes a table's imported rows that this instance doesn't have.
func merge(ctx context.Context, tx execer, t importedRows, rewrite rowRewrite, c *Counts) error {
	cols, err := columns(ctx, tx, t.Table)
	if err != nil {
		return err
	}
	local := map[string]bool{}
	for _, col := range cols {
		local[col.name] = true
	}
	var use []int // the archive's columns this instance has
	for i, name := range t.Columns {
		if local[name] {
			use = append(use, i)
		}
	}
	for _, k := range t.Key {
		if !local[k] {
			return fmt.Errorf("%s: this AgentBox has no column %s", t.Table, k)
		}
	}
	names := make([]string, len(use))
	for i, u := range use {
		names[i] = t.Columns[u]
	}
	insert := `INSERT INTO ` + t.Table + ` (` + quoteAll(names) + `) VALUES (` + strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ") + `)`
	var match string
	if len(t.Key) > 0 {
		match = `SELECT ` + quoteAll(names) + ` FROM ` + t.Table + ` WHERE ` + conditions(t.Key, " = ?")
	} else {
		match = `SELECT 1 FROM ` + t.Table + ` WHERE ` + conditions(names, " IS ?")
	}
	for _, raw := range t.Rows {
		if len(raw) != len(t.Columns) {
			return fmt.Errorf("%s: a row has %d values for %d columns", t.Table, len(raw), len(t.Columns))
		}
		row := make(map[string]any, len(t.Columns))
		for i, name := range t.Columns {
			v, err := decodeValue(raw[i])
			if err != nil {
				return fmt.Errorf("%s.%s: %w", t.Table, name, err)
			}
			row[name] = v
		}
		if rewrite != nil && !rewrite(row) {
			continue
		}
		vals := make([]any, len(names))
		for i, name := range names {
			vals[i] = row[name]
		}
		if len(t.Key) > 0 {
			keyVals := make([]any, len(t.Key))
			for i, k := range t.Key {
				keyVals[i] = row[k]
			}
			have := make([]any, len(names))
			ptrs := make([]any, len(names))
			for i := range have {
				ptrs[i] = &have[i]
			}
			err := tx.QueryRowContext(ctx, match, keyVals...).Scan(ptrs...)
			if err == nil {
				if differs := differing(names, have, vals); differs != "" {
					c.conflict(fmt.Sprintf("%s %s: this instance's differs in %s, kept", t.Table, describeKey(keyVals), differs))
				} else {
					c.Skipped++
				}
				continue
			}
			if err != sql.ErrNoRows {
				return err
			}
		} else {
			var one int
			err := tx.QueryRowContext(ctx, match, vals...).Scan(&one)
			if err == nil {
				c.Skipped++
				continue
			}
			if err != sql.ErrNoRows {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, insert, vals...); err != nil {
			return fmt.Errorf("adding to %s: %w", t.Table, err)
		}
		c.Added++
	}
	return nil
}

func conditions(cols []string, op string) string {
	c := make([]string, len(cols))
	for i, name := range cols {
		c[i] = `"` + name + `"` + op
	}
	return strings.Join(c, " AND ")
}

// differing names the columns whose values differ, "" when none do.
func differing(names []string, have, want []any) string {
	var cols []string
	for i, name := range names {
		if !same(have[i], want[i]) {
			cols = append(cols, name)
		}
	}
	return strings.Join(cols, ", ")
}

func describeKey(vals []any) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, "/")
}
