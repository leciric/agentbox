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
// An AgentBox-wide connector has neither project nor agent: every project
// gets it, unless the project overrides it or has its own of the same name.
//
// ClientSecret, AccessToken and RefreshToken are *ciphertext*, sealed by
// package secrets before they get here, like a Secret's Value. Package
// connectors is the only reader that opens them.
type Connector struct {
	Project string // empty for an AgentBox-wide connector
	Agent   string // empty for a project connector, which every agent of it gets
	Name    string
	URL     string
	Auth    string // "oauth", "secret" or "none"
	// Secret, Header and Scheme are a "secret" connector's: the secret it
	// sends and how.
	Secret string
	Header string
	Scheme string
	// Enabled is whether agents get it. An AgentBox-wide connector's is the
	// AgentBox-wide switch, except as ProjectConnectors and AgentConnectors
	// return it, where it is what the project says (EnabledFor).
	Enabled bool
	// Overrides are, for an AgentBox-wide connector, the projects that say
	// otherwise than Enabled, by name.
	Overrides map[string]bool

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

// ScopeAgentBox is an AgentBox-wide connector's scope.
const ScopeAgentBox = "agentbox"

// Scope is ScopeAgentBox, ScopeProject or ScopeAgent.
func (c Connector) Scope() string {
	switch {
	case c.Project == "":
		return ScopeAgentBox
	case c.Agent == "":
		return ScopeProject
	}
	return ScopeAgent
}

// Wide reports whether it is an AgentBox-wide connector.
func (c Connector) Wide() bool { return c.Project == "" }

// EnabledFor is whether a project's agents get an AgentBox-wide connector:
// the project's override, or the AgentBox-wide switch.
func (c Connector) EnabledFor(project string) bool {
	if on, ok := c.Overrides[project]; ok {
		return on
	}
	return c.Enabled
}

const connectorColumns = `project, agent, name, url, auth, secret, header, scheme, enabled,
	issuer, token_endpoint, resource, client_id, client_secret, token_auth, redirect_uri,
	access_token, refresh_token, expires_at, scope, error, connected_at, updated_at`

// SetConnector stores a connector whole, replacing whatever was there under
// (project, agent, name). Callers read, change and write it back. Only adding
// or changing a connector by hand uses it: everything else updates one that
// is already there (UpdateConnector), so it can't bring back a connector
// removed meanwhile.
func (s *Store) SetConnector(ctx context.Context, c Connector) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO connectors (`+connectorColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Project, c.Agent, c.Name, c.URL, c.Auth, c.Secret, c.Header, c.Scheme, c.Enabled,
		c.Issuer, c.TokenEndpoint, c.Resource, c.ClientID, c.ClientSecret, c.TokenAuth, c.RedirectURI,
		c.AccessToken, c.RefreshToken, unixOrZero(c.ExpiresAt), c.Granted, c.Error, unixOrZero(c.ConnectedAt), c.UpdatedAt.Unix())
	return err
}

// UpdateConnector stores a connector whole, like SetConnector, but only if it
// is still there: a sign-in or a refresh that ends after the connector, its
// agent or its project was removed finds it gone (ErrNotFound) rather than
// writing it, tokens and all, back.
func (s *Store) UpdateConnector(ctx context.Context, c Connector) error {
	res, err := s.db.ExecContext(ctx, `UPDATE connectors SET url = ?, auth = ?, secret = ?, header = ?, scheme = ?, enabled = ?,
		issuer = ?, token_endpoint = ?, resource = ?, client_id = ?, client_secret = ?, token_auth = ?, redirect_uri = ?,
		access_token = ?, refresh_token = ?, expires_at = ?, scope = ?, error = ?, connected_at = ?, updated_at = ?
		WHERE project = ? AND agent = ? AND name = ?`,
		c.URL, c.Auth, c.Secret, c.Header, c.Scheme, c.Enabled,
		c.Issuer, c.TokenEndpoint, c.Resource, c.ClientID, c.ClientSecret, c.TokenAuth, c.RedirectURI,
		c.AccessToken, c.RefreshToken, unixOrZero(c.ExpiresAt), c.Granted, c.Error, unixOrZero(c.ConnectedAt), c.UpdatedAt.Unix(),
		c.Project, c.Agent, c.Name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("connector %s: %w", c.Name, ErrNotFound)
	}
	return nil
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
// is "", otherwise that agent's own, and the AgentBox-wide ones for project "".
func (s *Store) Connectors(ctx context.Context, project, agent string) ([]Connector, error) {
	return s.queryConnectors(ctx, `WHERE project = ? AND agent = ? ORDER BY name`, project, agent)
}

// AllConnectors is every connector of every project, and the AgentBox-wide
// ones, for the refresh sweep.
func (s *Store) AllConnectors(ctx context.Context) ([]Connector, error) {
	return s.queryConnectors(ctx, `ORDER BY project, agent, name`)
}

// ProjectConnectors is what a project's agents get, enabled or not, before
// any agent's limit or own: its own connectors and the AgentBox-wide ones, by
// name, its own replacing an AgentBox-wide one of the same name. An
// AgentBox-wide one comes with Enabled as the project has it (EnabledFor).
func (s *Store) ProjectConnectors(ctx context.Context, project string) ([]Connector, error) {
	found, err := s.queryConnectors(ctx, `WHERE project IN (?, '') AND agent = '' ORDER BY name, project = ''`, project)
	if err != nil {
		return nil, err
	}
	return firstOfEach(found, project, Agent{}), nil
}

// ProjectConnector is the connector of that name a project's agents get: its
// own, or else the AgentBox-wide one, with Enabled as the project has it.
func (s *Store) ProjectConnector(ctx context.Context, project, name string) (Connector, error) {
	found, err := s.queryConnectors(ctx, `WHERE project IN (?, '') AND agent = '' AND name = ? ORDER BY project = ''`, project, name)
	if err != nil {
		return Connector{}, err
	}
	if found = firstOfEach(found, project, Agent{}); len(found) == 0 {
		return Connector{}, fmt.Errorf("connector %s: %w", name, ErrNotFound)
	}
	return found[0], nil
}

// AgentConnectors is what one agent gets, enabled or not: its project's
// connectors (ProjectConnectors) that its limit lets through
// (Agent.Connectors) and its own, by name, an agent's own replacing its
// project's of the same name, and those an AgentBox-wide one.
func (s *Store) AgentConnectors(ctx context.Context, project, agent string) ([]Connector, error) {
	a, err := s.Agent(ctx, project, agent)
	switch {
	case errors.Is(err, ErrNotFound):
		a = Agent{} // not made yet, or gone: nothing limits it
	case err != nil:
		return nil, err
	}
	found, err := s.queryConnectors(ctx, `WHERE (project = ? AND agent IN ('', ?)) OR (project = '' AND agent = '')
		ORDER BY name, agent = '', project = ''`, project, agent)
	if err != nil {
		return nil, err
	}
	return firstOfEach(found, project, a), nil
}

// firstOfEach keeps the first connector of each name of found, which is
// ordered by name and then by which one counts. One a's limit leaves out is
// skipped, and an AgentBox-wide one gets the project's Enabled.
func firstOfEach(found []Connector, project string, a Agent) []Connector {
	var out []Connector
	for i, c := range found {
		if i > 0 && found[i-1].Name == c.Name {
			continue
		}
		if c.Agent == "" && !a.GetsConnector(c.Name) {
			continue
		}
		if c.Wide() {
			c.Enabled = c.EnabledFor(project)
		}
		out = append(out, c)
	}
	return out
}

// SetConnectorOverride sets a project's say on an AgentBox-wide connector: on,
// off, or (nil) none, when it follows the AgentBox-wide switch again.
func (s *Store) SetConnectorOverride(ctx context.Context, name, project string, on *bool) error {
	if _, err := s.Connector(ctx, "", "", name); err != nil {
		return err
	}
	if on == nil {
		_, err := s.db.ExecContext(ctx, `DELETE FROM connector_projects WHERE connector = ? AND project = ?`, name, project)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO connector_projects (connector, project, enabled) VALUES (?, ?, ?)`, name, project, *on)
	return err
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

// RemoveConnector forgets one connector, sign-in and all, and an
// AgentBox-wide one's overrides.
func (s *Store) RemoveConnector(ctx context.Context, project, agent, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM connectors WHERE project = ? AND agent = ? AND name = ?`, project, agent, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("connector %s: %w", name, ErrNotFound)
	}
	if project == "" {
		_, err = s.db.ExecContext(ctx, `DELETE FROM connector_projects WHERE connector = ?`, name)
	}
	return err
}

// RemoveAgentConnectors forgets the connectors that belonged to one agent.
func (s *Store) RemoveAgentConnectors(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM connectors WHERE project = ? AND agent = ?`, project, agent)
	return err
}

// RemoveProjectConnectors forgets every connector of a project, in both scopes,
// and its overrides of the AgentBox-wide ones.
func (s *Store) RemoveProjectConnectors(ctx context.Context, project string) error {
	if project == "" {
		return errors.New("no project")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM connectors WHERE project = ?`, project); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM connector_projects WHERE project = ?`, project)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close() // before the next query, on a store that may have one connection
	return out, s.connectorOverrides(ctx, out)
}

// connectorOverrides fills in the AgentBox-wide connectors' Overrides.
func (s *Store) connectorOverrides(ctx context.Context, found []Connector) error {
	wide := map[string]int{}
	for i, c := range found {
		if c.Wide() {
			found[i].Overrides = map[string]bool{}
			wide[c.Name] = i
		}
	}
	if len(wide) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT connector, project, enabled FROM connector_projects`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name, project string
		var on bool
		if err := rows.Scan(&name, &project, &on); err != nil {
			return err
		}
		if i, ok := wide[name]; ok {
			found[i].Overrides[project] = on
		}
	}
	return rows.Err()
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
