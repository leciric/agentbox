package state

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The Codex, OpenCode and Cursor accounts projects, agents and leads' chats
// name (credentials/toolaccounts.go). Codex's and Cursor's are one name each,
// like a Claude Code account; OpenCode's are one per provider, an AccountMap.

// AccountMap names an account per key: OpenCode's provider → account, or a
// lead chat's tool → account. A key with an empty account isn't kept.
type AccountMap map[string]string

// ParseAccountMap reads the "key=account,…" a column holds; nil for none.
func ParseAccountMap(s string) AccountMap {
	var m AccountMap
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && k != "" && v != "" {
			if m == nil {
				m = AccountMap{}
			}
			m[k] = v
		}
	}
	return m
}

// String is the map as a column holds it, by key.
func (m AccountMap) String() string {
	var pairs []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if m[k] != "" {
			pairs = append(pairs, k+"="+m[k])
		}
	}
	return strings.Join(pairs, ",")
}

// Clean is the map without its empty accounts, never nil.
func (m AccountMap) Clean() AccountMap {
	out := AccountMap{}
	for k, v := range m {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// toolColumn is the column of projects and agents that names a tool's account.
func toolColumn(tool string) (string, error) {
	switch tool {
	case "codex":
		return "codex_account", nil
	case "opencode":
		return "opencode_accounts", nil
	case "cursor":
		return "cursor_account", nil
	}
	return "", fmt.Errorf("%q has no accounts: use codex, opencode or cursor", tool)
}

// SetProjectToolAccount picks the Codex or Cursor account a project's new
// agents and its lead use; "" for the machine's default.
func (s *Store) SetProjectToolAccount(ctx context.Context, name, tool, account string) error {
	if tool == "opencode" {
		return fmt.Errorf("OpenCode's accounts are per provider: use SetProjectOpenCodeAccounts")
	}
	col, err := toolColumn(tool)
	if err != nil {
		return err
	}
	return s.updateProject(ctx, name, `UPDATE projects SET `+col+` = ? WHERE name = ?`, account, name)
}

// SetProjectOpenCodeAccounts picks a project's OpenCode account for each
// provider; a provider left out gets its default.
func (s *Store) SetProjectOpenCodeAccounts(ctx context.Context, name string, accounts AccountMap) error {
	return s.updateProject(ctx, name, `UPDATE projects SET opencode_accounts = ? WHERE name = ?`, accounts.String(), name)
}

func (s *Store) updateProject(ctx context.Context, name, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// SetAgentToolAccounts records the Codex, OpenCode and Cursor accounts whose
// logins an agent, or a lead, holds.
func (s *Store) SetAgentToolAccounts(ctx context.Context, project, name, codex string, opencode AccountMap, cursor string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET codex_account = ?, opencode_accounts = ?, cursor_account = ? WHERE project = ? AND name = ?`,
		codex, opencode.String(), cursor, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("agent %s/%s: %w", project, name, ErrNotFound)
	}
	return nil
}

// ToolAccountRename is what renaming a Codex, OpenCode or Cursor account
// carried over: the projects that named it, and the agents (leads included)
// that hold it, by project/name.
type ToolAccountRename struct {
	Projects []string
	Agents   []string
}

// RenameToolAccount carries every reference to a Codex, OpenCode (one
// provider's) or Cursor account over to its new name, in one transaction:
// projects', agents' and leads' chats' choices. move, when not nil, runs
// inside the transaction, where the credentials store renames the login, and
// an error from it undoes the whole rename.
func (s *Store) RenameToolAccount(ctx context.Context, tool, provider, old, name string, move func() error) (ToolAccountRename, error) {
	var done ToolAccountRename
	col, err := toolColumn(tool)
	if err != nil {
		return done, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return done, err
	}
	defer func() { _ = tx.Rollback() }()

	// renamed is a column's value with the account renamed, and whether it
	// named it at all.
	renamed := func(value string) (string, bool) {
		if tool != "opencode" {
			return name, value == old
		}
		m := ParseAccountMap(value)
		if m[provider] != old {
			return value, false
		}
		m[provider] = name
		return m.String(), true
	}
	for _, table := range []struct{ name, query string }{
		{"projects", `SELECT name, '', ` + col + ` FROM projects ORDER BY name`},
		{"agents", `SELECT project, name, ` + col + ` FROM agents ORDER BY project, name`},
	} {
		rows, err := tx.QueryContext(ctx, table.query)
		if err != nil {
			return done, err
		}
		type change struct{ a, b, value string }
		var changes []change
		for rows.Next() {
			var a, b, value string
			if err := rows.Scan(&a, &b, &value); err != nil {
				_ = rows.Close()
				return done, err
			}
			if v, ok := renamed(value); ok {
				changes = append(changes, change{a, b, v})
			}
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return done, err
		}
		for _, c := range changes {
			if table.name == "projects" {
				_, err = tx.ExecContext(ctx, `UPDATE projects SET `+col+` = ? WHERE name = ?`, c.value, c.a)
				done.Projects = append(done.Projects, c.a)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE agents SET `+col+` = ? WHERE project = ? AND name = ?`, c.value, c.a, c.b)
				done.Agents = append(done.Agents, c.a+"/"+c.b)
			}
			if err != nil {
				return done, err
			}
		}
	}
	// A lead's composer may have chosen it (Chat.ToolAccounts); OpenCode's
	// isn't chosen there, so it has nothing to carry.
	if tool != "opencode" {
		rows, err := tx.QueryContext(ctx, `SELECT project, agent, tool_accounts FROM chats WHERE tool_accounts != ''`)
		if err != nil {
			return done, err
		}
		type change struct{ project, agent, value string }
		var changes []change
		for rows.Next() {
			var c change
			if err := rows.Scan(&c.project, &c.agent, &c.value); err != nil {
				_ = rows.Close()
				return done, err
			}
			m := ParseAccountMap(c.value)
			if m[tool] == old {
				m[tool] = name
				c.value = m.String()
				changes = append(changes, c)
			}
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return done, err
		}
		for _, c := range changes {
			if _, err := tx.ExecContext(ctx, `UPDATE chats SET tool_accounts = ? WHERE project = ? AND agent = ?`, c.value, c.project, c.agent); err != nil {
				return done, err
			}
		}
	}
	if move != nil {
		if err := move(); err != nil {
			return ToolAccountRename{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ToolAccountRename{}, err
	}
	return done, nil
}
