// Package hubapi is the protocol a machine running AgentBox and a hub speak to
// each other: the JSON a hub's own API exchanges, the shape of its tokens, and
// how the tunnel between them is framed.
//
// It is the seam between the two halves of AgentBox. Everything here is
// implemented on both sides — by the client in this repository, and by the hub,
// which is a separate program. It carries no server: nothing here reads a
// database, checks a password or decides who may see what. A hub does all of
// that, and this package says nothing about how.
//
// Unlike the rest of AgentBox's packages this one is not internal, because a
// hub has to be able to import it.
package hubapi

// Tokens a hub issues carry a prefix saying what they are, so a client can tell
// one from the other before sending it anywhere. A hub keeps only their hashes,
// and both are opaque to whoever holds one.
const (
	// SessionTokenPrefix marks a token from signing in, sent as
	// "Authorization: Bearer <token>".
	SessionTokenPrefix = "abx_s_"
	// EnvironmentTokenPrefix marks a machine's own token, which it connects
	// to its hub with. A hub shows it once, when the environment is added.
	EnvironmentTokenPrefix = "abx_e_"
)

// SessionCookie is where a browser on a hub's own pages keeps its session
// token. The CLI and the desktop app send the bearer header instead.
const SessionCookie = "abx_session"

// An environment announces itself in these headers when it dials the tunnel, so
// a hub can show which machine is connected and what it runs.
const (
	HeaderVersion  = "X-AgentBox-Version"
	HeaderHostname = "X-AgentBox-Hostname"
)

// A hub's own endpoints. Everything else it serves belongs to an environment,
// and is reached through EnvironmentAPI.
const (
	PathSignup       = "/v1/auth/signup"
	PathLogin        = "/v1/auth/login"
	PathLogout       = "/v1/auth/logout"
	PathMe           = "/v1/me"
	PathEnvironments = "/v1/environments"
	// PathConnect is the WebSocket an environment dials to open its tunnel,
	// authenticating with its environment token.
	PathConnect = "/v1/connect"
	// PathHealth answers without a session, for a load balancer.
	PathHealth = "/healthz"
)

// EnvironmentAPI is the prefix under which a hub passes requests through to an
// environment's daemon: a client speaks the daemon's own API below it, exactly
// as it would over the daemon's socket.
//
// The id is the environment's, and must already be escaped for a path.
func EnvironmentAPI(id string) string {
	return PathEnvironments + "/" + id + "/api"
}
