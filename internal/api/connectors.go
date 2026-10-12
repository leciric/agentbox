package api

import "time"

// The connectors API: remote MCP servers — Notion, Linear, Figma… — that a
// project's agents use as tools, signed in once by the user and held by the
// daemon. Like the secrets API, **no response here ever carries a token**: the
// daemon attaches it to each request it relays to the server, and neither the
// app nor the agent ever sees it. Package connectors has how it works.

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

// ConnectorWide is the Scope of an AgentBox-wide connector, which every
// project gets.
const ConnectorWide = "agentbox"

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

// Connector is one remote MCP server a project, or one agent of it, uses, or
// an AgentBox-wide one every project gets: GET /v1/connectors lists those,
// and a project's list (GET /v1/projects/{project}/connectors) has them after
// its own, with Enabled and Override as the project has them. A project's own
// connector replaces an AgentBox-wide one of the same name, which its list
// then leaves out.
type Connector struct {
	// Name is what the agents' AI tools know it as — its tools are
	// mcp__<name>__* in Claude Code. Lowercase letters, digits, - and _.
	Name string `json:"name"`
	// Scope is "agentbox" (every project gets it), "project" (every agent of
	// the project gets it) or "agent".
	Scope string `json:"scope"`
	// Project is empty for an AgentBox-wide connector.
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
	// sign-in and is simply left out. An AgentBox-wide one's is its
	// AgentBox-wide switch, or in a project's list whether that project's
	// agents get it.
	Enabled bool `json:"enabled"`
	// Override is, for an AgentBox-wide connector in a project's list, what
	// the project says: "on", "off", or "" to follow the AgentBox-wide switch.
	Override string `json:"override,omitempty"`
	// Overrides are, for an AgentBox-wide connector, the projects that say
	// otherwise, by name: true for on.
	Overrides map[string]bool `json:"overrides,omitempty"`
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
	// same name), the one agent for an agent connector, and for an
	// AgentBox-wide one every agent of the projects it is on in that has no
	// connector of its own by that name (of that project's, in its list).
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
	// SecretValue, for an AgentBox-wide connector only, is the value of
	// Secret, stored with it first: AgentBox keeps it to send to the server
	// and gives it to no agent as a variable. A project's or an agent's
	// connector sends one of its secrets, set the usual way. Left out, the
	// value stored before is kept.
	SecretValue string `json:"secretValue,omitempty"`
	// Enabled defaults to true for a new connector, and to what it was for
	// an existing one.
	Enabled *bool `json:"enabled,omitempty"`
}

// ConnectorOverrideRequest is the body of PUT
// /v1/projects/{project}/connectors/{name}/override: a project's say on an
// AgentBox-wide connector, "on", "off", or "" to follow the AgentBox-wide
// switch. It answers with the connector as the project's list has it.
type ConnectorOverrideRequest struct {
	Override string `json:"override"`
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

// How an MCP server found in an AI tool's configuration is reached.
const (
	// TransportHTTP is streamable HTTP: what a connector is.
	TransportHTTP = "http"
	// TransportSSE is the older HTTP+SSE transport, which the daemon's relay
	// doesn't speak.
	TransportSSE = "sse"
	// TransportStdio is a command the AI tool runs on the user's machine.
	TransportStdio = "stdio"
)

// Why a server found can't be imported, as ConnectorCandidate's Problem has it.
const (
	ConnectorProblemStdio = "stdio"
	ConnectorProblemSSE   = "sse"
	// ConnectorProblemURL is an address a connector can't have: not https,
	// or on this machine.
	ConnectorProblemURL = "url"
	// ConnectorProblemVariable is an address with an environment variable in
	// it, which only the user's shell can fill in.
	ConnectorProblemVariable = "variable"
)

// ScanConnectorsRequest is the body of POST /v1/connectors/scan: the MCP
// servers the user's own AI tools have, as they would be imported into
// Project ("" for AgentBox-wide): Claude Code (~/.claude.json, and its
// projects'), Claude Desktop, Codex, OpenCode, Cursor and Gemini CLI.
type ScanConnectorsRequest struct {
	Project string `json:"project,omitempty"`
}

// ConnectorScan is what a scan found.
type ConnectorScan struct {
	Candidates []ConnectorCandidate `json:"candidates"`
	// Unreadable are configurations that are there but couldn't be read.
	Unreadable []ConnectorUnreadable `json:"unreadable"`
}

// ConnectorUnreadable is one AI tool's configuration that couldn't be read.
type ConnectorUnreadable struct {
	Tool  string `json:"tool"`
	Path  string `json:"path"`
	Error string `json:"error"`
}

// ConnectorCandidate is one MCP server found, as it would be imported: the
// same URL in several tools is one. Never a header's value.
type ConnectorCandidate struct {
	// Name is what it would be added as: its name in the tool, made a valid
	// connector name no connector of the scope has, or the name of the one
	// that has its URL (Existing).
	Name string `json:"name"`
	// URL is empty for a stdio server.
	URL string `json:"url,omitempty"`
	// Transport is TransportHTTP, TransportSSE or TransportStdio.
	Transport string `json:"transport"`
	// Command is a stdio server's program, without its arguments.
	Command string            `json:"command,omitempty"`
	Sources []ConnectorSource `json:"sources"`
	// Header is the header the tool's configuration sends, which the
	// connector will send too, as the secret Secret; Scheme comes before its
	// value. With no header, it signs in with OAuth if the server asks for it
	// when imported, and sends nothing otherwise.
	Header string `json:"header,omitempty"`
	Scheme string `json:"scheme,omitempty"`
	Secret string `json:"secret,omitempty"`
	// NeedsValue is a header whose value is an environment variable
	// (Variable) of the user's shell, which AgentBox can't read: the import
	// needs it entered.
	NeedsValue bool   `json:"needsValue,omitempty"`
	Variable   string `json:"variable,omitempty"`
	// Existing is the connector of the scope that has this URL already: it
	// isn't imported again.
	Existing string `json:"existing,omitempty"`
	// Problem is why it can't be imported: ConnectorProblemStdio, …SSE, …URL
	// or …Variable.
	Problem string `json:"problem,omitempty"`
}

// ConnectorSource is one AI tool that has a server.
type ConnectorSource struct {
	// Tool is "claude", "claude-desktop", "codex", "opencode", "cursor" or
	// "gemini".
	Tool string `json:"tool"`
	// Name is what the tool calls it.
	Name string `json:"name"`
	// Path is the configuration file it is in.
	Path string `json:"path"`
	// Project is, for one of Claude Code's projects, its folder.
	Project string `json:"project,omitempty"`
}

// ImportConnectorsRequest is the body of POST /v1/connectors/import: some of
// what a scan finds, by URL, added to Project ("" for AgentBox-wide). The
// daemon scans again, so a header's value comes from the tool's
// configuration rather than through here — but for one that NeedsValue.
type ImportConnectorsRequest struct {
	Project string            `json:"project,omitempty"`
	Servers []ImportConnector `json:"servers"`
}

// ImportConnector is one server to import.
type ImportConnector struct {
	URL string `json:"url"`
	// Name replaces the candidate's.
	Name string `json:"name,omitempty"`
	// Value is the header's value, for a candidate that NeedsValue; left
	// out, a secret of that name the scope already has is sent.
	Value string `json:"value,omitempty"`
}

// ImportConnectorsResult is what an import added, and what it couldn't. An
// OAuth connector added still needs signing in to (POST …/connect).
type ImportConnectorsResult struct {
	Added  []Connector              `json:"added"`
	Failed []ImportConnectorFailure `json:"failed"`
}

// ImportConnectorFailure is a server that wasn't imported, and why.
type ImportConnectorFailure struct {
	URL   string `json:"url"`
	Name  string `json:"name,omitempty"`
	Error string `json:"error"`
}
