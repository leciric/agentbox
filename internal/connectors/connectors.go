// Package connectors gives agents remote MCP servers — Notion, Linear,
// Figma… — signed in to once, on the host, and used from inside every agent
// without a token ever reaching one.
//
// The daemon holds the sign-in: it finds out how a server wants to be signed
// in to (oauth.go), runs the browser sign-in, which comes back to the host's
// 127.0.0.1 (flow.go), keeps the tokens sealed in state.db and refreshes them before
// they expire (this file). Inside the agent, `agentbox connector mcp <name>`
// is a stdio MCP server that relays every message to the daemon over the
// agent's own socket (relay.go); the daemon attaches the token and speaks
// streamable HTTP to the server (proxy.go).
//
//	AI tool ─stdin/stdout─ agentbox connector mcp notion ─agent socket, no token─
//	  daemon ─POST + Authorization: Bearer …─ https://mcp.notion.com/mcp
//
// The relay passes Content-Type, Accept, Mcp-Session-Id, Mcp-Protocol-Version
// and Last-Event-ID through, and back Content-Type, Mcp-Session-Id and
// Cache-Control; nothing else crosses either way, cookies included. A
// connector that can't be used answers 409 with why, which the stdio relay
// turns into a JSON-RPC error for the request.
//
// Tokens are refreshed when a request finds them within refreshAhead of
// expiring, and by a sweep every few minutes for those within sweepAhead, so a
// refresh almost never lands in the middle of a tool call. A request answered
// 401 is refreshed and retried once.
//
// What the real servers did, checked on 29 September 2026 (live_test.go):
// Notion answers 401 with resource_metadata, supports S256, allows public
// clients and accepts dynamic registration from a third-party client. Figma's
// metadata advertises a registration endpoint that answers 403 to every
// client it hasn't approved, so Figma is a secret connector, with a personal
// access token in X-Figma-Token.
package connectors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/secrets"
	"agentbox/internal/state"
)

// namePattern is what an AI tool can take as an MCP server's name, in all
// three tools' configuration: Claude Code's JSON keys, Codex's TOML table
// names and OpenCode's.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// Reserved are the names of the MCP servers AgentBox gives every agent
// itself (agentMCPServers in internal/agent), and the one it gives a
// project's chat: a connector of the same name would replace one.
var Reserved = []string{"playwright", "desktop", "memory", "agentbox"}

// ValidateName refuses a name no AI tool could be given.
func ValidateName(name string) error {
	switch {
	case name == "":
		return errors.New("a connector needs a name, like notion")
	case !namePattern.MatchString(name):
		return fmt.Errorf("invalid connector name %q: use lowercase letters, digits, - and _, up to 40 of them (like notion or linear)", name)
	case slices.Contains(Reserved, name):
		return fmt.Errorf("%q is one of the MCP servers AgentBox gives every agent itself: call the connector something else", name)
	}
	return nil
}

// Service is the connectors of every project. The daemon has one.
type Service struct {
	State   *state.Store
	Secrets secrets.Store
	OAuth   OAuth
	// Upstream is what relayed requests go through: no timeout of its own,
	// since a streamed answer lasts as long as the tool call does. The
	// request's context ends it.
	Upstream *http.Client
	// OnChange is told about every connector that changed, or went (with
	// removed set): the daemon publishes it and rewrites agents' MCP
	// configuration when the set they get changed.
	OnChange func(c state.Connector, removed bool)
	// Callback is the address the browser comes back to after a sign-in,
	// on the daemon's preview proxy, which hands it to ServeCallback; ""
	// while there is none. Nil, or "", has each sign-in listen on a port of
	// its own instead (flow.go).
	Callback func() string
	// CallbackHost is where a sign-in's own listener binds: 127.0.0.1 unless
	// a test says otherwise.
	CallbackHost string

	mu      sync.Mutex
	pending map[key]*signIn
	locks   map[key]*sync.Mutex

	upstreamOnce sync.Once
	relayClient  *http.Client
}

type key struct{ project, agent, name string }

func keyOf(c state.Connector) key { return key{c.Project, c.Agent, c.Name} }

// Set adds a connector or changes one. Changing where it is or how it signs
// in forgets the sign-in, which was for the old one.
func (s *Service) Set(ctx context.Context, project, agent, name string, req api.SetConnectorRequest) (state.Connector, error) {
	if err := ValidateName(name); err != nil {
		return state.Connector{}, err
	}
	req.URL = strings.TrimSpace(req.URL)
	if err := s.OAuth.CheckURL(req.URL); err != nil {
		return state.Connector{}, err
	}
	auth := req.Auth
	if auth == "" {
		auth = api.ConnectorOAuth
		if req.Secret != "" {
			auth = api.ConnectorSecret
		}
	}
	l := s.lock(key{project, agent, name})
	l.Lock()
	defer l.Unlock()
	c := state.Connector{Project: project, Agent: agent, Name: name, Enabled: true}
	existing, err := s.State.Connector(ctx, project, agent, name)
	switch {
	case err == nil:
		c = existing
	case !errors.Is(err, state.ErrNotFound):
		return state.Connector{}, err
	}
	switch auth {
	case api.ConnectorOAuth, api.ConnectorNone:
		if req.Secret != "" || req.Header != "" || req.Scheme != "" {
			return state.Connector{}, fmt.Errorf("--secret, --header and --scheme are for a connector that sends a secret, not one with %s auth", auth)
		}
		c.Secret, c.Header, c.Scheme = "", "", ""
	case api.ConnectorSecret:
		if err := secrets.ValidateName(req.Secret); err != nil {
			return state.Connector{}, fmt.Errorf("a connector that sends a secret needs the secret's name: %w", err)
		}
		c.Secret, c.Header, c.Scheme = req.Secret, strings.TrimSpace(req.Header), strings.TrimSpace(req.Scheme)
		if c.Header == "" {
			c.Header = "Authorization"
		}
		if strings.EqualFold(c.Header, "Authorization") && c.Scheme == "" {
			c.Scheme = "Bearer"
		}
		if !validHeader(c.Header) {
			return state.Connector{}, fmt.Errorf("%q isn't a header name", c.Header)
		}
	default:
		return state.Connector{}, fmt.Errorf("unknown auth %q: use oauth, secret or none", auth)
	}
	if c.URL != req.URL || c.Auth != auth {
		s.cancelSignIn(keyOf(c))
		c = forget(c, true)
	}
	c.URL, c.Auth = req.URL, auth
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	c.UpdatedAt = time.Now()
	if err := s.State.SetConnector(ctx, c); err != nil {
		return state.Connector{}, err
	}
	s.changed(c, false)
	return c, nil
}

var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// validHeader refuses what isn't a header name, and the headers the relay
// sets itself.
func validHeader(h string) bool {
	switch strings.ToLower(h) {
	case "content-type", "accept", "mcp-session-id", "mcp-protocol-version", "host", "content-length":
		return false
	}
	return headerName.MatchString(h)
}

// forget empties a connector's sign-in: its tokens, and with registration its
// client too.
func forget(c state.Connector, registration bool) state.Connector {
	c.AccessToken, c.RefreshToken, c.ExpiresAt, c.Granted, c.ConnectedAt, c.Error = nil, nil, time.Time{}, "", time.Time{}, ""
	if registration {
		c.Issuer, c.TokenEndpoint, c.Resource, c.ClientID, c.ClientSecret, c.TokenAuth, c.RedirectURI = "", "", "", "", nil, "", ""
	}
	return c
}

// Remove forgets a connector, sign-in and all. It waits for a refresh under
// way, which then finds the connector gone rather than writing it back.
func (s *Service) Remove(ctx context.Context, project, agent, name string) error {
	l := s.lock(key{project, agent, name})
	l.Lock()
	defer l.Unlock()
	c, err := s.State.Connector(ctx, project, agent, name)
	if err != nil {
		return err
	}
	s.cancelSignIn(keyOf(c))
	if err := s.State.RemoveConnector(ctx, project, agent, name); err != nil {
		return err
	}
	s.changed(c, true)
	return nil
}

// Disconnect forgets a connector's tokens and keeps the connector, and its
// client registration, to connect again.
func (s *Service) Disconnect(ctx context.Context, project, agent, name string) (state.Connector, error) {
	l := s.lock(key{project, agent, name})
	l.Lock()
	defer l.Unlock()
	c, err := s.State.Connector(ctx, project, agent, name)
	if err != nil {
		return state.Connector{}, err
	}
	s.cancelSignIn(keyOf(c))
	c = forget(c, false)
	c.UpdatedAt = time.Now()
	if err := s.State.UpdateConnector(ctx, c); err != nil {
		return state.Connector{}, err
	}
	s.changed(c, false)
	return c, nil
}

func (s *Service) changed(c state.Connector, removed bool) {
	if s.OnChange != nil {
		s.OnChange(c, removed)
	}
}

// Status is where a connector stands, and why when it can't be used. forAgent
// is the agent a project connector is being looked at for, whose own secret
// would be sent; "" looks at the project's.
func (s *Service) Status(ctx context.Context, c state.Connector, forAgent string) (status, why string) {
	if c.Error != "" {
		return api.ConnectorError, c.Error
	}
	switch c.Auth {
	case api.ConnectorNone:
		return api.ConnectorConnected, ""
	case api.ConnectorSecret:
		agent := forAgent
		if c.Agent != "" {
			agent = c.Agent
		}
		if _, err := s.Secrets.Resolve(ctx, c.Project, agent, c.Secret); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return api.ConnectorError, fmt.Sprintf("it sends the secret %s, which isn't set: agentbox secrets set %s %s", c.Secret, c.Project, c.Secret)
			}
			return api.ConnectorError, err.Error()
		}
		return api.ConnectorConnected, ""
	}
	s.mu.Lock()
	_, waiting := s.pending[keyOf(c)]
	s.mu.Unlock()
	switch {
	case waiting:
		return api.ConnectorConnecting, ""
	case len(c.AccessToken) > 0:
		return api.ConnectorConnected, ""
	}
	return api.ConnectorDisconnected, ""
}

// Info is a connector as the API shows it, without a token. agents are the
// refs it reaches.
func (s *Service) Info(ctx context.Context, c state.Connector, agents []string) api.Connector {
	status, why := s.Status(ctx, c, "")
	out := api.Connector{
		Name: c.Name, Scope: c.Scope(), Project: c.Project, Agent: c.Agent, URL: c.URL, Auth: c.Auth,
		Secret: c.Secret, Header: c.Header, Scheme: c.Scheme, Enabled: c.Enabled,
		Status: status, Error: why, Issuer: c.Issuer, Scopes: c.Granted, UpdatedAt: c.UpdatedAt, Agents: agents,
	}
	if out.Agents == nil {
		out.Agents = []string{}
	}
	if !c.ExpiresAt.IsZero() && len(c.AccessToken) > 0 {
		t := c.ExpiresAt
		out.ExpiresAt = &t
	}
	if !c.ConnectedAt.IsZero() {
		t := c.ConnectedAt
		out.ConnectedAt = &t
	}
	return out
}

// refreshAhead is how close to expiring a token is refreshed before a request
// uses it; sweepAhead is how close the sweep refreshes it, so that a request
// almost never has to wait for one.
const (
	refreshAhead = 2 * time.Minute
	sweepAhead   = 10 * time.Minute
)

// ErrNotConnected is a connector that can't be used until the user acts.
var ErrNotConnected = errors.New("not connected")

// Header is the header a request to the connector's server carries, read when
// the request is made: a secret's current value, or an OAuth access token,
// refreshed first when it is about to expire — or, with force, because the
// server just refused it. forAgent is the agent the request is for.
func (s *Service) Header(ctx context.Context, c state.Connector, forAgent string, force bool) (name, value string, err error) {
	switch c.Auth {
	case api.ConnectorNone:
		return "", "", nil
	case api.ConnectorSecret:
		agent := forAgent
		if c.Agent != "" {
			agent = c.Agent
		}
		v, err := s.Secrets.Resolve(ctx, c.Project, agent, c.Secret)
		if errors.Is(err, state.ErrNotFound) {
			return "", "", fmt.Errorf("%w: %s sends the secret %s, which isn't set", ErrNotConnected, c.Name, c.Secret)
		}
		if err != nil {
			return "", "", err
		}
		if c.Scheme != "" {
			v = c.Scheme + " " + v
		}
		return c.Header, v, nil
	}
	c, err = s.fresh(ctx, c, refreshAhead, force)
	if err != nil {
		return "", "", err
	}
	token, err := s.Secrets.Open(c.AccessToken)
	if err != nil {
		return "", "", err
	}
	return "Authorization", "Bearer " + token, nil
}

// lock is the one lock per connector that a refresh holds, so two requests
// that both find the token about to expire refresh it once — which matters
// with a server that rotates refresh tokens, where the second refresh would
// spend a token the first already used. Everything else that reads a
// connector, changes it and writes it back holds it too, so a refresh can't
// undo a removal, a sign-out or a new URL made while it was out.
func (s *Service) lock(k key) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[key]*sync.Mutex{}
	}
	l, ok := s.locks[k]
	if !ok {
		l = &sync.Mutex{}
		s.locks[k] = l
	}
	return l
}

// fresh is an OAuth connector with an access token good for at least ahead,
// refreshing it if need be. It rereads the connector under the lock: another
// request may have refreshed it meanwhile.
func (s *Service) fresh(ctx context.Context, c state.Connector, ahead time.Duration, force bool) (state.Connector, error) {
	l := s.lock(keyOf(c))
	l.Lock()
	defer l.Unlock()
	stale := c
	c, err := s.State.Connector(ctx, c.Project, c.Agent, c.Name)
	if err != nil {
		return state.Connector{}, err
	}
	if c.Error != "" {
		return state.Connector{}, fmt.Errorf("%w: %s", ErrNotConnected, c.Error)
	}
	if len(c.AccessToken) == 0 {
		return state.Connector{}, fmt.Errorf("%w: nobody has signed in to %s yet: agentbox connector connect %s %s", ErrNotConnected, c.Name, target(c), c.Name)
	}
	// Forced by a refusal of a token that has since been replaced: the new
	// one hasn't been tried yet.
	if force && string(stale.AccessToken) != string(c.AccessToken) {
		return c, nil
	}
	due := !c.ExpiresAt.IsZero() && time.Until(c.ExpiresAt) < ahead
	if !force && !due {
		return c, nil
	}
	if len(c.RefreshToken) == 0 {
		if !force && time.Until(c.ExpiresAt) > 0 {
			return c, nil // no way to refresh it, and it still works for now
		}
		return state.Connector{}, s.failLocked(ctx, c, "its sign-in expired and the server gave no way to renew it: connect it again")
	}
	refresh, err := s.Secrets.Open(c.RefreshToken)
	if err != nil {
		return state.Connector{}, err
	}
	client, err := s.client(c)
	if err != nil {
		return state.Connector{}, err
	}
	t, err := s.OAuth.Refresh(ctx, c.TokenEndpoint, client, refresh, c.Resource)
	if errors.Is(err, ErrGrant) {
		return state.Connector{}, s.failLocked(ctx, c, "the server no longer accepts its sign-in: connect it again ("+err.Error()+")")
	}
	if err != nil {
		// A server that is down now may be up at the next request: the token
		// may even still work until it expires.
		if !force && time.Until(c.ExpiresAt) > 0 {
			return c, nil
		}
		return state.Connector{}, fmt.Errorf("couldn't renew %s's sign-in: %w", c.Name, err)
	}
	if c, err = s.store(ctx, c, t, false); err != nil {
		return state.Connector{}, err
	}
	s.changed(c, false)
	return c, nil
}

// fail marks a connector as needing the user, and says so.
func (s *Service) fail(ctx context.Context, c state.Connector, why string) error {
	l := s.lock(keyOf(c))
	l.Lock()
	defer l.Unlock()
	c, err := s.State.Connector(ctx, c.Project, c.Agent, c.Name)
	if err != nil {
		return err
	}
	return s.failLocked(ctx, c, why)
}

// failLocked is fail with the connector's lock already held, and c just read.
func (s *Service) failLocked(ctx context.Context, c state.Connector, why string) error {
	c.Error, c.UpdatedAt = why, time.Now()
	if err := s.State.UpdateConnector(ctx, c); err != nil {
		return err
	}
	s.changed(c, false)
	return fmt.Errorf("%w: %s: %s", ErrNotConnected, c.Name, why)
}

// store seals and keeps new tokens. A refresh answered without a refresh token
// keeps the old one: the server didn't rotate it. The caller holds the
// connector's lock, and c is what it read under it.
func (s *Service) store(ctx context.Context, c state.Connector, t Token, signedIn bool) (state.Connector, error) {
	access, err := s.Secrets.Seal(t.Access)
	if err != nil {
		return state.Connector{}, err
	}
	c.AccessToken, c.ExpiresAt, c.Error = access, t.Expires, ""
	if t.Refresh != "" {
		if c.RefreshToken, err = s.Secrets.Seal(t.Refresh); err != nil {
			return state.Connector{}, err
		}
	} else if signedIn {
		c.RefreshToken = nil
	}
	if t.Scope != "" || signedIn {
		c.Granted = t.Scope
	}
	now := time.Now()
	if signedIn {
		c.ConnectedAt = now
	}
	c.UpdatedAt = now
	return c, s.State.UpdateConnector(ctx, c)
}

// client is a connector's registered client, secret opened.
func (s *Service) client(c state.Connector) (Client, error) {
	out := Client{ID: c.ClientID, AuthMethod: c.TokenAuth}
	if len(c.ClientSecret) > 0 {
		secret, err := s.Secrets.Open(c.ClientSecret)
		if err != nil {
			return Client{}, err
		}
		out.Secret = secret
	}
	return out, nil
}

// RefreshDue refreshes every connected OAuth connector whose token expires
// within sweepAhead. The daemon runs it every few minutes; failures are left
// on the connectors themselves, and returned together for the log.
func (s *Service) RefreshDue(ctx context.Context) error {
	all, err := s.State.AllConnectors(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range all {
		if c.Auth != api.ConnectorOAuth || c.Error != "" || len(c.AccessToken) == 0 || len(c.RefreshToken) == 0 ||
			c.ExpiresAt.IsZero() || time.Until(c.ExpiresAt) > sweepAhead {
			continue
		}
		if _, err := s.fresh(ctx, c, sweepAhead, false); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", target(c), c.Name, err))
		}
	}
	return errors.Join(errs...)
}

// target is how the command line names a connector's scope.
func target(c state.Connector) string {
	if c.Agent == "" {
		return c.Project
	}
	return c.Project + "/" + c.Agent
}

// Close ends every sign-in still waiting on a browser.
func (s *Service) Close() {
	s.mu.Lock()
	var keys []key
	for k := range s.pending {
		keys = append(keys, k)
	}
	s.mu.Unlock()
	for _, k := range keys {
		s.cancelSignIn(k)
	}
}
