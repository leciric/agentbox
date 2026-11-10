package hub

import (
	"context"
	"encoding/json"
	"time"
)

const mcpPairingColumns = `id, environment_id, label, access, projects, created_at, last_used_at`

func scanMCPPairing(row scanner) (MCPPairing, error) {
	var p MCPPairing
	var projects string
	var created, used int64
	if err := row.Scan(&p.ID, &p.EnvironmentID, &p.Label, &p.Access, &projects, &created, &used); err != nil {
		return MCPPairing{}, notFound(err)
	}
	_ = json.Unmarshal([]byte(projects), &p.Projects)
	p.CreatedAt, p.LastUsedAt = fromMillis(created), fromMillis(used)
	return p, nil
}

func (s *SQLStore) CreateMCPPairing(ctx context.Context, p MCPPairing) error {
	projects, _ := json.Marshal(p.Projects)
	_, err := s.exec(ctx, `INSERT INTO mcp_pairings (`+mcpPairingColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.EnvironmentID, p.Label, p.Access, string(projects), millis(p.CreatedAt), millis(p.LastUsedAt))
	return err
}

func (s *SQLStore) MCPPairings(ctx context.Context, environmentID string) ([]MCPPairing, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`SELECT `+mcpPairingColumns+` FROM mcp_pairings WHERE environment_id = ? ORDER BY created_at, id`), environmentID)
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

func (s *SQLStore) MCPPairing(ctx context.Context, id string) (MCPPairing, error) {
	return scanMCPPairing(s.queryRow(ctx, `SELECT `+mcpPairingColumns+` FROM mcp_pairings WHERE id = ?`, id))
}

func (s *SQLStore) DeleteMCPPairing(ctx context.Context, environmentID, id string) error {
	res, err := s.exec(ctx, `DELETE FROM mcp_pairings WHERE environment_id = ? AND id = ?`, environmentID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.exec(ctx, `DELETE FROM mcp_tokens WHERE pairing_id = ?`, id)
	return err
}

func (s *SQLStore) TouchMCPPairing(ctx context.Context, id string, used time.Time) error {
	_, err := s.exec(ctx, `UPDATE mcp_pairings SET last_used_at = ? WHERE id = ?`, millis(used), id)
	return err
}

func (s *SQLStore) MCPPairingClients(ctx context.Context, pairingID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`SELECT DISTINCT c.name FROM mcp_tokens t JOIN oauth_clients c ON c.id = t.client_id WHERE t.pairing_id = ? ORDER BY c.name`), pairingID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

const mcpTokenColumns = `token_hash, pairing_id, kind, client_id, created_at, expires_at`

func scanMCPToken(row scanner) (MCPToken, error) {
	var t MCPToken
	var created, expires int64
	if err := row.Scan(&t.TokenHash, &t.PairingID, &t.Kind, &t.ClientID, &created, &expires); err != nil {
		return MCPToken{}, notFound(err)
	}
	t.CreatedAt, t.ExpiresAt = fromMillis(created), fromMillis(expires)
	return t, nil
}

func (s *SQLStore) CreateMCPToken(ctx context.Context, t MCPToken) error {
	_, err := s.exec(ctx, `INSERT INTO mcp_tokens (`+mcpTokenColumns+`) VALUES (?, ?, ?, ?, ?, ?)`,
		t.TokenHash, t.PairingID, t.Kind, t.ClientID, millis(t.CreatedAt), millis(t.ExpiresAt))
	return err
}

// A token whose pairing is gone is no token: the join is what revokes it on a
// database that doesn't cascade.
const mcpTokenLive = ` FROM mcp_tokens WHERE token_hash = ? AND (expires_at = 0 OR expires_at > ?) AND pairing_id IN (SELECT id FROM mcp_pairings)`

func (s *SQLStore) MCPToken(ctx context.Context, tokenHash string, now time.Time) (MCPToken, error) {
	return scanMCPToken(s.queryRow(ctx, `SELECT `+mcpTokenColumns+mcpTokenLive, tokenHash, millis(now)))
}

func (s *SQLStore) TakeMCPToken(ctx context.Context, tokenHash string, now time.Time) (MCPToken, error) {
	// DELETE … RETURNING is one statement on both databases, so two callers
	// racing for the same token can't both get it.
	return scanMCPToken(s.queryRow(ctx, `DELETE`+mcpTokenLive+` RETURNING `+mcpTokenColumns, tokenHash, millis(now)))
}

func (s *SQLStore) DeleteMCPClientTokens(ctx context.Context, pairingID, clientID string) error {
	_, err := s.exec(ctx, `DELETE FROM mcp_tokens WHERE pairing_id = ? AND client_id = ?`, pairingID, clientID)
	return err
}

func (s *SQLStore) DeleteExpiredMCPTokens(ctx context.Context, now time.Time) error {
	_, err := s.exec(ctx, `DELETE FROM mcp_tokens WHERE expires_at != 0 AND expires_at <= ?`, millis(now))
	return err
}

func (s *SQLStore) CreateOAuthClient(ctx context.Context, c OAuthClient) error {
	uris, _ := json.Marshal(c.RedirectURIs)
	_, err := s.exec(ctx, `INSERT INTO oauth_clients (id, name, redirect_uris, created_at) VALUES (?, ?, ?, ?)`, c.ID, c.Name, string(uris), millis(c.CreatedAt))
	return err
}

func (s *SQLStore) OAuthClient(ctx context.Context, id string) (OAuthClient, error) {
	var c OAuthClient
	var uris string
	var created int64
	if err := s.queryRow(ctx, `SELECT id, name, redirect_uris, created_at FROM oauth_clients WHERE id = ?`, id).Scan(&c.ID, &c.Name, &uris, &created); err != nil {
		return OAuthClient{}, notFound(err)
	}
	_ = json.Unmarshal([]byte(uris), &c.RedirectURIs)
	c.CreatedAt = fromMillis(created)
	return c, nil
}

func (s *SQLStore) CountOAuthClients(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, `SELECT COUNT(*) FROM oauth_clients`).Scan(&n)
	return n, err
}

func (s *SQLStore) PruneOAuthClients(ctx context.Context, before time.Time) error {
	_, err := s.exec(ctx, `DELETE FROM oauth_clients WHERE created_at < ? AND id NOT IN (SELECT client_id FROM mcp_tokens)`, millis(before))
	return err
}
