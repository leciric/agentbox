package hubapi

import "time"

// Another app — an assistant such as Claude, ChatGPT or Poke — reaches a
// machine's AgentBox over remote MCP (Streamable HTTP) through the hub: the
// hub serves PathMCP, knows each caller by its token, and passes the call down
// the environment's tunnel. Nothing is opened on the machine itself.
//
// What lets an app in is an MCP pairing, which the machine makes
// (PathMCPPairings, with its environment token) and may revoke. The machine
// decides what a pairing may do and keeps that to itself: the hub only says
// which pairing is calling (HeaderMCPPairing), and shows what the machine told
// it on the page where the pairing's code is typed.
const (
	// MCPTokenPrefix marks a token an app calls PathMCP with: one an OAuth
	// client was issued, or the one a pairing was made with for an app that
	// takes a bearer token instead.
	MCPTokenPrefix = "abx_m_"
	// MCPRefreshPrefix marks an OAuth refresh token, taken once at
	// PathOAuthToken for a new pair.
	MCPRefreshPrefix = "abx_r_"
)

const (
	// PathMCP is the MCP endpoint apps are given, the same for every
	// pairing: the token says which machine it reaches.
	PathMCP = "/mcp"
	// PathMCPPairings lists (GET) and makes (POST) the MCP pairings of the
	// environment whose token the request carries; DELETE …/{id} revokes
	// one, and POST …/{id}/code gives it a new code.
	PathMCPPairings = "/v1/environment/mcp/pairings"
	// PathMCPAuthorizePage is the OAuth authorization endpoint: a page that
	// asks for a pairing's code.
	PathMCPAuthorizePage = "/hub/mcp/authorize"
	// PathOAuthToken and PathOAuthRegister are the OAuth token endpoint and
	// dynamic client registration (RFC 7591).
	PathOAuthToken    = "/v1/oauth/token"
	PathOAuthRegister = "/v1/oauth/register"
	// PathOAuthResource and PathOAuthServer are the metadata an MCP client
	// discovers the rest from (RFC 9728, RFC 8414).
	PathOAuthResource = "/.well-known/oauth-protected-resource"
	PathOAuthServer   = "/.well-known/oauth-authorization-server"

	// EnvironmentMCPPath is where, on the environment's side of the tunnel,
	// the hub sends what arrives at PathMCP.
	EnvironmentMCPPath = "/v1/mcp"
)

// HeaderMCPPairing names, on a request the hub passes to
// EnvironmentMCPPath, the pairing whose token it came with. The hub sets it
// and strips it from every other request, so an environment can trust it.
const HeaderMCPPairing = "X-AgentBox-MCP-Pairing"

// What a pairing lets its app do. Each level includes the ones before it.
const (
	// MCPAccessRead reads: projects, agents, pull requests, the lead's chat.
	MCPAccessRead = "read"
	// MCPAccessChat also sends messages to a project's lead.
	MCPAccessChat = "chat"
	// MCPAccessFull also calls the lead's own tools directly.
	MCPAccessFull = "full"
)

// HubMCPPairingRequest asks for a pairing. Access and Projects are what the
// machine will enforce, sent so the hub can show them where the code is typed;
// no Projects means all of them.
type HubMCPPairingRequest struct {
	Label    string   `json:"label"` // what the user called it: "Claude", "Poke on my phone"
	Access   string   `json:"access"`
	Projects []string `json:"projects,omitempty"`
	// Token asks for a bearer token too, for an app that can't do OAuth.
	Token bool `json:"token,omitempty"`
}

// HubMCPPairing is an MCP pairing. Code and Token are only in the answer that
// made them: the hub keeps the token's hash, and a code works once.
type HubMCPPairing struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Access   string   `json:"access"`
	Projects []string `json:"projects,omitempty"`
	// URL is the MCP endpoint to give the app.
	URL string `json:"url"`
	// Code is typed on the hub's page when the app signs in with OAuth.
	Code          string    `json:"code,omitempty"`
	CodeExpiresAt time.Time `json:"codeExpiresAt,omitzero,omitempty"`
	Token         string    `json:"token,omitempty"`
	// Clients are the OAuth clients signed in with it, by the name each
	// registered with.
	Clients    []string  `json:"clients,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt,omitzero,omitempty"`
}
