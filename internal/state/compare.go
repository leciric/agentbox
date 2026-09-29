package state

import (
	"context"
	"database/sql"
	"fmt"
)

// carriedTables hold what moving an installation has to carry: what a user
// would miss if it didn't arrive. Tables that only record the daemon's own
// work (jobs, consolidation passes) are left out, and so are FTS indexes,
// which follow their tables.
var carriedTables = []string{
	"projects", "agents", "chats", "chat_items", "questions", "settings", "secrets", "media",
	"events", "memories", "working_memory", "artifacts", "agent_reports", "tasks",
	"project_sections", "token_usage", "claude_limits", "pr_watches",
}

// Carried is how many rows of a table one state.db had, and how many another
// made from it has.
type Carried struct {
	Table    string
	From, To int
}

// OpenReadOnly opens a state.db without migrating it or writing to it: a
// copy of another installation's, from any release, which Compare reads.
func OpenReadOnly(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
}

// Compare counts the rows of carriedTables in from, a state.db from any
// release, and in to, one made from it and migrated since. A table from
// didn't have yet is left out; to has every table, being migrated.
func Compare(ctx context.Context, from, to *sql.DB) ([]Carried, error) {
	have, err := tables(ctx, from)
	if err != nil {
		return nil, err
	}
	var out []Carried
	for _, table := range carriedTables {
		if !have[table] {
			continue
		}
		c := Carried{Table: table}
		if err := from.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&c.From); err != nil {
			return nil, fmt.Errorf("counting %s: %w", table, err)
		}
		if err := to.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&c.To); err != nil {
			return nil, fmt.Errorf("counting %s: %w", table, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// HasTable reports whether db has a table of that name.
func HasTable(ctx context.Context, db *sql.DB, name string) (bool, error) {
	have, err := tables(ctx, db)
	return have[name], err
}

func tables(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		have[name] = true
	}
	return have, rows.Err()
}
