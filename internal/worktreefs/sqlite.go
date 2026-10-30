package worktreefs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"agentbox/internal/api"

	_ "modernc.org/sqlite"
)

// The viewer's SQLite browser: a database in a worktree, opened read-only
// (mode=ro, and query_only so no statement can write), its tables listed and
// one table's rows read a page at a time. Nothing but those two queries runs.

const (
	// RowsLimit caps one page of a table's rows.
	RowsLimit = 500
	// SQLiteLimit is the largest database a commit's copy is taken out of git
	// for; on disk, any size opens.
	SQLiteLimit = 256 << 20
	// sqliteTimeout bounds each query, and countTimeout a table's row count.
	sqliteTimeout = 10 * time.Second
	countTimeout  = 2 * time.Second
)

var sqliteHeader = []byte("SQLite format 3\x00")

// ErrNoTable is a table the database doesn't have.
var ErrNoTable = errors.New("no such table")

// SQLite reads a database in the worktree at root: its tables, and when table
// is set, up to limit of its rows from offset.
func SQLite(ctx context.Context, root, p, table string, offset, limit int) (api.SQLiteQuery, error) {
	rel, abs, err := Resolve(root, p)
	if err != nil {
		return api.SQLiteQuery{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return api.SQLiteQuery{}, err
	}
	if !st.Mode().IsRegular() {
		return api.SQLiteQuery{}, fmt.Errorf("%s is not a regular file", rel)
	}
	// Not immutable: an agent may be writing to it, and SQLite's locks keep
	// the read consistent.
	return querySQLite(ctx, rel, abs, false, table, offset, limit)
}

// SQLiteAt is SQLite on a database in commit, copied out of git to a
// temporary file, which a commit's never changes: opened immutable.
func SQLiteAt(ctx context.Context, root, commit, p, table string, offset, limit int) (api.SQLiteQuery, error) {
	rel, err := Rel(root, p)
	if err != nil {
		return api.SQLiteQuery{}, err
	}
	o, err := lookupOne(root, commit, rel)
	if err != nil {
		return api.SQLiteQuery{}, err
	}
	if o.kind != "blob" {
		return api.SQLiteQuery{}, fmt.Errorf("%s is a folder", rel)
	}
	if o.size > SQLiteLimit {
		return api.SQLiteQuery{}, fmt.Errorf("%s is too large to open from git", rel)
	}
	data, err := blob(root, o.oid, SQLiteLimit)
	if err != nil {
		return api.SQLiteQuery{}, err
	}
	f, err := os.CreateTemp("", "agentbox-sqlite-*.db")
	if err != nil {
		return api.SQLiteQuery{}, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return api.SQLiteQuery{}, err
	}
	return querySQLite(ctx, rel, f.Name(), true, table, offset, limit)
}

// sqliteURI is file's URI for SQLite, which decodes %HH in it: a '?' or '#'
// in the path would otherwise start the query or fragment.
func sqliteURI(file string, immutable bool) string {
	path := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(file)
	uri := "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)"
	if immutable {
		uri += "&immutable=1"
	}
	return uri
}

func querySQLite(ctx context.Context, rel, file string, immutable bool, table string, offset, limit int) (api.SQLiteQuery, error) {
	out := api.SQLiteQuery{Path: rel, Tables: []api.SQLiteTable{}, Columns: []string{}, Rows: [][]any{}}
	// sql.Open would make an empty database of a file that isn't one.
	f, err := os.Open(file)
	if err != nil {
		return out, err
	}
	head := make([]byte, len(sqliteHeader))
	n, _ := io.ReadFull(f, head)
	_ = f.Close()
	if !bytes.Equal(head[:n], sqliteHeader) {
		return out, fmt.Errorf("%s is not an SQLite database", rel)
	}
	db, err := sql.Open("sqlite", sqliteURI(file, immutable))
	if err != nil {
		return out, err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, sqliteTimeout)
	defer cancel()

	rows, err := db.QueryContext(ctx, `SELECT name, type FROM sqlite_schema WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			_ = rows.Close()
			return out, err
		}
		out.Tables = append(out.Tables, api.SQLiteTable{Name: name, View: kind == "view"})
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if table == "" {
		return out, nil
	}
	found := false
	for _, t := range out.Tables {
		found = found || t.Name == table
	}
	if !found {
		return out, fmt.Errorf("%w: %s", ErrNoTable, table)
	}
	out.Table = table
	limit = min(max(limit, 1), RowsLimit)
	offset = max(offset, 0)
	out.Offset = offset
	quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`

	out.Total = -1
	cctx, ccancel := context.WithTimeout(ctx, countTimeout)
	_ = db.QueryRowContext(cctx, "SELECT count(*) FROM "+quoted).Scan(&out.Total)
	ccancel()

	rows, err = db.QueryContext(ctx, "SELECT * FROM "+quoted+" LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	if out.Columns, err = rows.Columns(); err != nil {
		return out, err
	}
	for rows.Next() {
		cells := make([]any, len(out.Columns))
		ptrs := make([]any, len(cells))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return out, err
		}
		for i, c := range cells {
			cells[i] = cell(c)
		}
		out.Rows = append(out.Rows, cells)
	}
	return out, rows.Err()
}

// maxSafe is JavaScript's Number.MAX_SAFE_INTEGER.
const maxSafe = 1<<53 - 1

// cell is a value as SQLiteQuery sends it.
func cell(v any) any {
	switch v := v.(type) {
	case int64:
		if v > maxSafe || v < -maxSafe {
			return strconv.FormatInt(v, 10)
		}
	case []byte:
		// A TEXT column holding text comes as a string; this is a BLOB, or
		// text the driver handed over as bytes.
		if utf8.Valid(v) && !IsBinary(v) {
			return string(v)
		}
		return map[string]int{"blob": len(v)}
	case time.Time:
		return v.Format(time.RFC3339Nano)
	}
	return v
}
