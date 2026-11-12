package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// MCPPairing is another app allowed to use this machine's AgentBox over
// remote MCP, through a hub (internal/daemon/remotemcp.go). The hub holds the
// app's tokens and says which pairing a call came with; what the pairing may
// do is here, where the hub can't change it. Revoking one is deleting its row.
type MCPPairing struct {
	ID         string // the hub's ID for it
	Hub        string // the hub it was made on
	Label      string
	Client     string
	Access     string
	Projects   []string // none: every project
	CreatedAt  time.Time
	LastUsedAt time.Time
}

// Covers reports whether the pairing reaches a project.
func (p MCPPairing) Covers(project string) bool {
	if project == HomeProject || project == "" {
		return false
	}
	if len(p.Projects) == 0 {
		return true
	}
	for _, name := range p.Projects {
		if name == project {
			return true
		}
	}
	return false
}

const mcpPairingColumns = `id, hub, label, client, access, projects, created_at, last_used_at`

func scanMCPPairing(row interface{ Scan(...any) error }) (MCPPairing, error) {
	var p MCPPairing
	var projects string
	var created, used int64
	if err := row.Scan(&p.ID, &p.Hub, &p.Label, &p.Client, &p.Access, &projects, &created, &used); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MCPPairing{}, ErrNotFound
		}
		return MCPPairing{}, err
	}
	_ = json.Unmarshal([]byte(projects), &p.Projects)
	p.CreatedAt = time.UnixMilli(created)
	if used != 0 {
		p.LastUsedAt = time.UnixMilli(used)
	}
	return p, nil
}

func (s *Store) AddMCPPairing(ctx context.Context, p MCPPairing) error {
	projects, _ := json.Marshal(append([]string{}, p.Projects...))
	_, err := s.db.ExecContext(ctx, `INSERT INTO mcp_pairings (`+mcpPairingColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
		p.ID, p.Hub, p.Label, p.Client, p.Access, string(projects), p.CreatedAt.UnixMilli())
	return err
}

// MCPPairings are the pairings made on a hub, the oldest first.
func (s *Store) MCPPairings(ctx context.Context, hub string) ([]MCPPairing, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mcpPairingColumns+` FROM mcp_pairings WHERE hub = ? ORDER BY created_at, id`, hub)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []MCPPairing{}
	for rows.Next() {
		p, err := scanMCPPairing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MCPPairing is a pairing made on a hub, or ErrNotFound.
func (s *Store) MCPPairing(ctx context.Context, hub, id string) (MCPPairing, error) {
	return scanMCPPairing(s.db.QueryRowContext(ctx, `SELECT `+mcpPairingColumns+` FROM mcp_pairings WHERE hub = ? AND id = ?`, hub, id))
}

// RemoveMCPPairing revokes a pairing; ErrNotFound when there is none.
func (s *Store) RemoveMCPPairing(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM mcp_pairings WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveMCPPairings revokes every pairing made on a hub.
func (s *Store) RemoveMCPPairings(ctx context.Context, hub string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mcp_pairings WHERE hub = ?`, hub)
	return err
}

// TouchMCPPairing records that a pairing's app called.
func (s *Store) TouchMCPPairing(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE mcp_pairings SET last_used_at = ? WHERE id = ?`, at.UnixMilli(), id)
	return err
}
