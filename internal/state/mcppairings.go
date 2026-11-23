package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// MCPPairing is another app allowed to use this machine's AgentBox over
// remote MCP (internal/daemon/remotemcp.go). Through a hub, the hub holds the
// app's tokens and says which pairing a call came with. Without one (Route
// MCPRouteLocal or MCPRouteTunnel), the app calls the daemon itself with a
// bearer token whose hash is kept here. Either way what the pairing may do is
// here, where no hub can change it. Revoking one is deleting its row.
type MCPPairing struct {
	ID         string // the hub's ID for it, or this machine's
	Hub        string // the hub it was made on; "" for one made without a hub
	Route      string // "" through Hub, or MCPRouteLocal or MCPRouteTunnel
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

// The ways an app reaches this machine without a hub.
const (
	// MCPRouteLocal is an app on the computer AgentBox runs on, at a
	// loopback address of that computer.
	MCPRouteLocal = "local"
	// MCPRouteTunnel is an app anywhere, through the tunnel phones use.
	MCPRouteTunnel = "tunnel"
)

// SettingRemoteMCPTunnel lets apps paired for MCPRouteTunnel reach this
// machine through the tunnel ("1"), which then runs whether chatting from a
// phone is on or not. Off by default.
const SettingRemoteMCPTunnel = "remote_mcp_tunnel"

// SettingRemoteMCPPort is the port apps on this computer reach the MCP
// endpoint at; "" is DefaultRemoteMCPPort.
const SettingRemoteMCPPort = "remote_mcp_port"

// DefaultRemoteMCPPort is the local MCP port when nobody chose one: next to
// phones' (DefaultLANPort) and the named tunnel's (DefaultLANTunnelPort).
const DefaultRemoteMCPPort = 7782

const mcpPairingColumns = `id, hub, route, label, client, access, projects, created_at, last_used_at`

func scanMCPPairing(row interface{ Scan(...any) error }) (MCPPairing, error) {
	var p MCPPairing
	var projects string
	var created, used int64
	if err := row.Scan(&p.ID, &p.Hub, &p.Route, &p.Label, &p.Client, &p.Access, &projects, &created, &used); err != nil {
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO mcp_pairings (`+mcpPairingColumns+`) VALUES (?, ?, '', ?, ?, ?, ?, ?, 0)`,
		p.ID, p.Hub, p.Label, p.Client, p.Access, string(projects), p.CreatedAt.UnixMilli())
	return err
}

// newMCPToken is a bearer token for a pairing made without a hub, marked as
// the hub marks its own (hubapi.MCPTokenPrefix, "abx_m_"), and its hash.
func newMCPToken() (token, hash string, err error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", err
	}
	token = "abx_m_" + base64.RawURLEncoding.EncodeToString(secret)
	return token, HashPhoneToken(token), nil
}

// AddDirectMCPPairing pairs an app that reaches this machine on route, with
// no hub: it gives p an ID and returns it with its bearer token, which isn't
// kept and can't be read again.
func (s *Store) AddDirectMCPPairing(ctx context.Context, p MCPPairing) (MCPPairing, string, error) {
	if p.Route != MCPRouteLocal && p.Route != MCPRouteTunnel {
		return MCPPairing{}, "", errors.New("a pairing without a hub is for the local route or the tunnel")
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return MCPPairing{}, "", err
	}
	token, hash, err := newMCPToken()
	if err != nil {
		return MCPPairing{}, "", err
	}
	p.ID, p.Hub, p.LastUsedAt = "mcp_"+hex.EncodeToString(id), "", time.Time{}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	p.Projects = append([]string{}, p.Projects...)
	projects, _ := json.Marshal(p.Projects)
	_, err = s.db.ExecContext(ctx, `INSERT INTO mcp_pairings (`+mcpPairingColumns+`, token_hash) VALUES (?, '', ?, ?, ?, ?, ?, ?, 0, ?)`,
		p.ID, p.Route, p.Label, p.Client, p.Access, string(projects), p.CreatedAt.UnixMilli(), hash)
	if err != nil {
		return MCPPairing{}, "", err
	}
	return p, token, nil
}

// RenewMCPToken gives a pairing made without a hub a new bearer token, which
// it returns; the old one stops working at once.
func (s *Store) RenewMCPToken(ctx context.Context, id string) (string, error) {
	token, hash, err := newMCPToken()
	if err != nil {
		return "", err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE mcp_pairings SET token_hash = ? WHERE id = ? AND hub = '' AND route != ''`, hash, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return token, nil
}

// MCPPairingByToken is the pairing made without a hub whose bearer token
// this is, or ErrNotFound.
func (s *Store) MCPPairingByToken(ctx context.Context, token string) (MCPPairing, error) {
	if token == "" {
		return MCPPairing{}, ErrNotFound
	}
	return scanMCPPairing(s.db.QueryRowContext(ctx, `SELECT `+mcpPairingColumns+` FROM mcp_pairings WHERE token_hash = ? AND hub = '' AND route != ''`, HashPhoneToken(token)))
}

// MCPPairingByID is a pairing, made on any hub or none, or ErrNotFound.
func (s *Store) MCPPairingByID(ctx context.Context, id string) (MCPPairing, error) {
	return scanMCPPairing(s.db.QueryRowContext(ctx, `SELECT `+mcpPairingColumns+` FROM mcp_pairings WHERE id = ?`, id))
}

// HasMCPRoute reports whether any app is paired to reach this machine on
// route.
func (s *Store) HasMCPRoute(ctx context.Context, route string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM mcp_pairings WHERE hub = '' AND route = ?)`, route).Scan(&n)
	return n == 1, err
}

// MCPPairings are the pairings made on a hub, the oldest first; hub "" is
// those made without one.
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

// MCPPairing is a pairing made on a hub ("": without one), or ErrNotFound.
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
