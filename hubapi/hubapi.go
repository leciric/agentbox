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
	// SessionTokenPrefix marks a device's token: what a device gets when it
	// is approved (or, on hubs from before pairing, signs in with a
	// password), sent as "Authorization: Bearer <token>". The hub lists each
	// as a device, and revoking one ends it.
	SessionTokenPrefix = "abx_s_"
	// EnvironmentTokenPrefix marks a machine's own token, which it connects
	// to its hub with. A hub shows it once, when the environment is added.
	EnvironmentTokenPrefix = "abx_e_"
	// PairingSecretPrefix marks the secret a device asking to be approved
	// holds, and polls PathPairToken with. It is never shown to anyone.
	PairingSecretPrefix = "abx_p_"
	// SetupSecretPrefix marks the secret in a setup link (PathSetupPage),
	// which makes whoever opens it first the hub's owner, or signs the owner
	// in once on a hub that has one.
	SetupSecretPrefix = "abx_l_"
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
	// PathInfo answers HubInfo, without a session: what a client checks
	// before connecting.
	PathInfo = "/v1/hub"
	// PathLogin signs in with a password. Only accounts made before pairing
	// have one; a client asks for a password only when HubInfo.Passwords.
	PathLogin  = "/v1/auth/login"
	PathLogout = "/v1/auth/logout"
	PathMe     = "/v1/me"
	// PathSetup takes a setup link's secret (HubSetupRequest) and answers a
	// HubSession: the first to send it becomes the hub's owner.
	PathSetup = "/v1/setup"
	// PathPair is where a device or an environment asks to be approved:
	// POST a HubPairRequest, without a session, and get a HubPairing.
	PathPair = "/v1/pair"
	// PathPairToken is polled with the pairing's secret
	// (HubPairTokenRequest) until it answers a HubPairResult that isn't
	// pending.
	PathPairToken = "/v1/pair/token"
	// PathPairings is where a signed-in device looks up a pairing by its
	// code (GET PathPairings/<code>, a HubPairingInfo) and approves or denies
	// it (POST PathPairings/<code>/approve or /deny).
	PathPairings = "/v1/pairings"
	// PathDevices lists the signed-in user's devices (GET, []HubDevice); a
	// DELETE of PathDevices/<id> revokes one.
	PathDevices      = "/v1/devices"
	PathEnvironments = "/v1/environments"
	// PathConnect is the WebSocket an environment dials to open its tunnel,
	// authenticating with its environment token.
	PathConnect = "/v1/connect"
	// PathEnvironmentTunnel is where an environment asks for its stable
	// address for phones: POST, with its environment token, and no body. A hub
	// that gives them out answers HubEnvironmentTunnel, the same hostname every
	// time; one that doesn't answers 404 or 501.
	PathEnvironmentTunnel = "/v1/environment/tunnel"
	// PathHealth answers without a session, for a load balancer.
	PathHealth = "/healthz"
)

// A hub's own pages, for a browser: where a setup link and a pairing's
// approval go. A hub serves them whether or not it serves the web app.
const (
	// PathSetupPage is a setup link without its secret, which follows as the
	// URL's fragment (#abx_l_…), so it never reaches a server's logs.
	PathSetupPage = "/hub/setup"
	// PathApprovePage approves a pairing: PathApprovePage?code=<code>.
	PathApprovePage = "/hub/approve"
)

// EnvironmentAPI is the prefix under which a hub passes requests through to an
// environment's daemon: a client speaks the daemon's own API below it, exactly
// as it would over the daemon's socket.
//
// The id is the environment's, and must already be escaped for a path.
func EnvironmentAPI(id string) string {
	return PathEnvironments + "/" + id + "/api"
}

// EnvironmentTunnelOrigin is where the tunnel a hub makes for an environment
// reaches the environment's daemon, on the loopback address of the machine
// cloudflared runs on: the hub configures its ingress with it, and the daemon
// listens on it.
const EnvironmentTunnelOrigin = "http://127.0.0.1:7781"
