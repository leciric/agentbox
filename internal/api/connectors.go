package api

import "time"

// The connectors API: remote MCP servers — Notion, Linear, Figma… — that a
// project's agents use as tools, signed in once by the user and held by the
// daemon. Like the secrets API, **no response here ever carries a token**: the
// daemon attaches it to each request it relays to the server, and neither the
// app nor the agent ever sees it. docs/connectors.md is the reference.

// How a connector signs in to its server.
const (
	// ConnectorOAuth signs in through the browser, with the MCP
	// authorization flow: discovery, dynamic client registration and PKCE.
	ConnectorOAuth = "oauth"
	// ConnectorSecret sends one of the project's secrets as a static header,
	// for a server with no OAuth or one that won't register AgentBox: a
	// Notion integration token, a Figma personal access token.
	ConnectorSecret = "secret"
	// ConnectorNone sends nothing: a public server.
	ConnectorNone = "none"
)

// Where a connector stands.
const (
	// ConnectorConnected can be used: signed in, or its secret is set.
	ConnectorConnected = "connected"
	// ConnectorDisconnected is an OAuth connector nobody has signed in yet,
	// or one that was disconnected.
	ConnectorDisconnected = "disconnected"
	// ConnectorConnecting is waiting on the user to finish signing in, in
	// the browser, at the URL connect answered with.
	ConnectorConnecting = "connecting"
	// ConnectorError can't be used until somebody acts: Error says what
	// happened, and usually that it needs connecting again.
	ConnectorError = "error"
)

// EventConnector is published with a Connector whenever one is added,
// changed, removed (with Removed set), or changes status — a sign-in
// finishing is the one to wait for after connect.
const EventConnector = "connector"

// Connector is one remote MCP server a project, or one agent of it, uses.
type Connector struct {
	// Name is what the agents' AI tools know it as — its tools are
	// mcp__<name>__* in Claude Code. Lowercase letters, digits, - and _.
	Name string `json:"name"`
	// Scope is "project" (every agent of the project gets it) or "agent".
	Scope   string `json:"scope"`
	Project string `json:"project"`
	// Agent is empty for a project connector.
	Agent string `json:"agent,omitempty"`
	// URL is the server's streamable HTTP endpoint, like
	// https://mcp.notion.com/mcp.
	URL string `json:"url"`
	// Auth is ConnectorOAuth, ConnectorSecret or ConnectorNone.
	Auth string `json:"auth"`
	// Secret, Header and Scheme are ConnectorSecret's: the secret sent, the
	// header it goes in ("Authorization" unless said otherwise), and what
	// comes before it there ("Bearer" for Authorization, nothing for any
	// other header unless said otherwise). The secret's value is never here.
	Secret string `json:"secret,omitempty"`
	Header string `json:"header,omitempty"`
	Scheme string `json:"scheme,omitempty"`
	// Enabled connectors are given to agents. A disabled one keeps its
	// sign-in and is simply left out.
	Enabled bool `json:"enabled"`
	// Status is ConnectorConnected, ConnectorDisconnected,
	// ConnectorConnecting or ConnectorError.
	Status string `json:"status"`
	// Error is why Status is ConnectorError, in words for the user.
	Error string `json:"error,omitempty"`
	// Issuer is the authorization server an OAuth connector signed in with.
	Issuer string `json:"issuer,omitempty"`
	// Scopes are what the server granted, space-separated.
	Scopes string `json:"scopes,omitempty"`
	// ExpiresAt is when the current access token runs out. The daemon
	// refreshes it before then; nil when it doesn't expire or there is none.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	// ConnectedAt is when the user last signed in.
	ConnectedAt *time.Time `json:"connectedAt,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	// Agents are the agents this connector is given to, by ref: every agent
	// of the project for a project connector (unless one has its own of the
	// same name), the one agent for an agent connector.
	Agents []string `json:"agents"`
	// Removed is set on the EventConnector for a connector that is gone.
	Removed bool `json:"removed,omitempty"`
}

// SetConnectorRequest is the body of PUT .../connectors/{name}: it adds a
// connector or changes one. Changing the URL or the way it signs in forgets
// its sign-in.
type SetConnectorRequest struct {
	URL string `json:"url"`
	// Auth defaults to ConnectorOAuth, or ConnectorSecret when Secret is set.
	Auth   string `json:"auth,omitempty"`
	Secret string `json:"secret,omitempty"`
	Header string `json:"header,omitempty"`
	Scheme string `json:"scheme,omitempty"`
	// Enabled defaults to true for a new connector, and to what it was for
	// an existing one.
	Enabled *bool `json:"enabled,omitempty"`
}

// ConnectResult is the answer to POST .../connectors/{name}/connect: the
// page to open in the user's browser. When the user signs in, the server
// redirects the browser to RedirectURI, a listener of the daemon's on
// 127.0.0.1, which finishes the sign-in and publishes EventConnector.
type ConnectResult struct {
	AuthorizationURL string `json:"authorizationUrl"`
	RedirectURI      string `json:"redirectUri"`
	// ExpiresAt is when the daemon stops waiting for the browser.
	ExpiresAt time.Time `json:"expiresAt"`
	Connector Connector `json:"connector"`
}

// SelfConnector is a connector as the agent it is given to sees it, on the
// in-agent socket: GET /v1/self/connectors.
type SelfConnector struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// QuestionConnector is the Kind of a Question that is a connector request:
// an agent asking the user to connect a connector it needs, with
// request_connector. Like a credential request, only the user answers it,
// from the app; unlike one, it carries nothing secret either way — the user
// signs in on the server's own page, and the tokens stay with the daemon.
const QuestionConnector = "connector"

// ConnectorRequest is the body of POST /v1/self/connector, on the in-agent
// socket: request_connector. The call waits until the user has connected it
// (answered) or declined (an error, with their reason), and says so.
type ConnectorRequest struct {
	// Name is the connector: one the project has, or the name to add it as.
	Name string `json:"name"`
	// URL is the server's streamable HTTP endpoint, for a connector the
	// project doesn't have yet. It is ignored for one it has.
	URL string `json:"url,omitempty"`
	// Reason is what the agent needs it for, for the user to decide on.
	Reason string `json:"reason"`
}
