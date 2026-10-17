package state

import (
	"context"
	"time"
)

// SettingClaudeCodeAutoUpdate says whether the daemon keeps Claude Code and
// its ACP adapter at the newest release on npm rather than at tools.txt's
// pins (internal/daemon/toolupdates.go). Off until somebody turns it on, so it
// is read with Flag.
const SettingClaudeCodeAutoUpdate = "claude_code_auto_update"

// ToolVersion is what the daemon knows of one agent tool it keeps up to date
// past its pin (tool_versions).
type ToolVersion struct {
	// Name is mise's name for the tool: "claude".
	Name string
	// Version is the one the base image runs instead of the pin, once an
	// update to it passed; "" for the pin.
	Version string
	// Latest is the newest release the last check found, CheckedAt when, and
	// Error why that check or the update after it failed.
	Latest    string
	CheckedAt time.Time
	Error     string
}

// ToolVersions is every tool's row, by name.
func (s *Store) ToolVersions(ctx context.Context) (map[string]ToolVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, version, latest, checked_at, error FROM tool_versions`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]ToolVersion{}
	for rows.Next() {
		var v ToolVersion
		var at int64
		if err := rows.Scan(&v.Name, &v.Version, &v.Latest, &at, &v.Error); err != nil {
			return nil, err
		}
		if at > 0 {
			v.CheckedAt = time.UnixMilli(at)
		}
		out[v.Name] = v
	}
	return out, rows.Err()
}

// ToolOverrides are the versions the base image runs instead of the pins:
// name → version, for the tools that have one.
func (s *Store) ToolOverrides(ctx context.Context) (map[string]string, error) {
	all, err := s.ToolVersions(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, v := range all {
		if v.Version != "" {
			out[name] = v.Version
		}
	}
	return out, nil
}

// SetToolVersion replaces a tool's row.
func (s *Store) SetToolVersion(ctx context.Context, v ToolVersion) error {
	var at int64
	if !v.CheckedAt.IsZero() {
		at = v.CheckedAt.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO tool_versions (name, version, latest, checked_at, error) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET version = excluded.version, latest = excluded.latest,
			checked_at = excluded.checked_at, error = excluded.error`,
		v.Name, v.Version, v.Latest, at, v.Error)
	return err
}

// ForgetToolVersions drops what the daemon knew of these tools, overrides
// included, which puts them back on their pins.
func (s *Store) ForgetToolVersions(ctx context.Context, names ...string) error {
	for _, name := range names {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM tool_versions WHERE name = ?`, name); err != nil {
			return err
		}
	}
	return nil
}
