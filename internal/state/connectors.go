package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Connector is one remote MCP server a project, or one agent of it, uses:
// where it is, how it signs in, and — for an OAuth one that has — the sign-in.
//
// ClientSecret, AccessToken and RefreshToken are *ciphertext*, sealed by
// package secrets before they get here, like a Secret's Value. Package
// connectors is the only reader that opens them.
type Connector struct {
	Project string
	Agent   string // empty for a project connector, which every agent of it gets
	Name    string
	URL     string
	Auth    string // "oauth", "secret" or "none"
	// Secret, Header and Scheme are a "secret" connector's: the secret it
	// sends and how.
	Secret  string
	Header  string
	Scheme  string
	Enabled bool

	// What discovery and registration found, kept so a refresh needs neither.
	Issuer        string
	TokenEndpoint string
	Resource      string
	ClientID      string
	ClientSecret  []byte
	TokenAuth     string // how the client authenticates to the token endpoint
	RedirectURI   string // what the client was registered with

	AccessToken  []byte
	RefreshToken []byte
	ExpiresAt    time.Time // zero when the token doesn't say
	Granted      string    // the scopes the server granted, space-separated
	// Error is why the connector can't be used until somebody acts.
	Error       string
	ConnectedAt time.Time
	UpdatedAt   time.Time
}

// Scope is ScopeProject or ScopeAgent.
func (c Connector) Scope() string {
	if c.Agent == "" {
		return ScopeProject
	}
	return ScopeAgent
}

const connectorColumns = `project, agent, name, url, auth, secret, header, scheme, enabled,
	issuer, token_endpoint, resource, client_id, client_secret, token_auth, redirect_uri,
	access_token, refresh_token, expires_at, scope, error, connected_at, updated_at`

// SetConnector stores a connector whole, replacing whatever was there under
// (project, agent, name). Callers read, change and write it back.
func (s *Store) SetConnector(ctx context.Context, c Connector) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO connectors (`+connectorColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Project, c.Agent, c.Name, c.URL, c.Auth, c.Secret, c.Header, c.Scheme, c.Enabled,
		c.Issuer, c.TokenEndpoint, c.Resource, c.ClientID, c.ClientSecret, c.TokenAuth, c.RedirectURI,
		c.AccessToken, c.RefreshToken, unixOrZero(c.ExpiresAt), c.Granted, c.Error, unixOrZero(c.ConnectedAt), c.UpdatedAt.Unix())
	return err
}

// Connector reads one connector. An agent is "" for a project connector.
func (s *Store) Connector(ctx context.Context, project, agent, name string) (Connector, error) {
	found, err := s.queryConnectors(ctx, `WHERE project = ? AND agent = ? AND name = ?`, project, agent, name)
	if err != nil {
		return Connector{}, err
	}
	if len(found) == 0 {
		return Connector{}, fmt.Errorf("connector %s: %w", name, ErrNotFound)
	}
	return found[0], nil
}

// Connectors lists one scope's connectors by name: a project's own when agent
// is "", otherwise that agent's own.
func (s *Store) Connectors(ctx context.Context, project, agent string) ([]Connector, error) {
	return s.queryConnectors(ctx, `WHERE project = ? AND agent = ? ORDER BY name`, project, agent)
}

// AllConnectors is every connector of every project, for the refresh sweep.
func (s *Store) AllConnectors(ctx context.Context) ([]Connector, error) {
	return s.queryConnectors(ctx, `ORDER BY project, agent, name`)
}

// AgentConnectors is what one agent gets, enabled or not: its project's
// connectors that its limit lets through (Agent.Connectors) and its own, by
// name, an agent's own replacing its project's of the same name.
func (s *Store) AgentConnectors(ctx context.Context, project, agent string) ([]Connector, error) {
	a, err := s.Agent(ctx, project, agent)
	switch {
	case errors.Is(err, ErrNotFound):
		a = Agent{} // not made yet, or gone: nothing limits it
	case err != nil:
		return nil, err
	}
	found, err := s.queryConnectors(ctx, `WHERE project = ? AND agent IN ('', ?) ORDER BY name, agent = ''`, project, agent)
	if err != nil {
		return nil, err
	}
	// Ordered by name with an agent's own first, so the first of each name
	// is the one that counts.
	var out []Connector
	for _, c := range found {
		if len(out) > 0 && out[len(out)-1].Name == c.Name {
			continue
		}
		if c.Agent == "" && !a.GetsConnector(c.Name) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// SetAgentConnectors changes which of its project's connectors an agent is
// given: nil for every one.
func (s *Store) SetAgentConnectors(ctx context.Context, project, name string, connectors []string) error {
	limit, err := connectorLimit(connectors)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET connectors = ? WHERE project = ? AND name = ?`, limit, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("agent %s/%s: %w", project, name, ErrNotFound)
	}
	return nil
}

// connectorLimit is Agent.Connectors as its column holds it: NULL for no
// limit, a JSON array otherwise.
func connectorLimit(names []string) (sql.NullString, error) {
	if names == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(names)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

// RemoveConnector forgets one connector, sign-in and all.
func (s *Store) RemoveConnector(ctx context.Context, project, agent, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM connectors WHERE project = ? AND agent = ? AND name = ?`, project, agent, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("connector %s: %w", name, ErrNotFound)
	}
	return nil
}

// RemoveAgentConnectors forgets the connectors that belonged to one agent.
func (s *Store) RemoveAgentConnectors(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM connectors WHERE project = ? AND agent = ?`, project, agent)
	return err
}

// RemoveProjectConnectors forgets every connector of a project, in both scopes.
func (s *Store) RemoveProjectConnectors(ctx context.Context, project string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM connectors WHERE project = ?`, project)
	return err
}

func (s *Store) queryConnectors(ctx context.Context, clause string, args ...any) ([]Connector, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+connectorColumns+` FROM connectors `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Connector
	for rows.Next() {
		var c Connector
		var expires, connected, updated int64
		if err := rows.Scan(&c.Project, &c.Agent, &c.Name, &c.URL, &c.Auth, &c.Secret, &c.Header, &c.Scheme, &c.Enabled,
			&c.Issuer, &c.TokenEndpoint, &c.Resource, &c.ClientID, &c.ClientSecret, &c.TokenAuth, &c.RedirectURI,
			&c.AccessToken, &c.RefreshToken, &expires, &c.Granted, &c.Error, &connected, &updated); err != nil {
			return nil, err
		}
		c.ExpiresAt, c.ConnectedAt = timeOrZero(expires), timeOrZero(connected)
		c.UpdatedAt = time.Unix(updated, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrZero(unix int64) time.Time {
	if unix == 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}
