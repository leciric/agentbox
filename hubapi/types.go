package hubapi

import "time"

// The JSON a hub's own API exchanges. A hub serves these; the CLI, the desktop
// app and the web app read them.

// HubUser is an account on a hub. The owner, who claimed the hub with its
// setup link, has no email; accounts from before pairing have one.
type HubUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type HubLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Label    string `json:"label,omitempty"`
}

// HubInfo answers PathInfo.
type HubInfo struct {
	Version string `json:"version"`
	// Claimed is false until someone opens the setup link: until then
	// nobody can approve a pairing.
	Claimed bool `json:"claimed"`
	// Passwords is true when the hub has accounts from before pairing,
	// which can still sign in with their password (PathLogin).
	Passwords bool `json:"passwords,omitempty"`
}

// HubSetupRequest takes a setup link's secret.
type HubSetupRequest struct {
	Secret string `json:"secret"`
	Label  string `json:"label,omitempty"` // what's connecting, as in HubPairRequest
}

// Kinds of pairing: a device signs in (the app, the CLI, a browser, a phone)
// and gets a HubSession; an environment gets a HubEnvironmentToken.
const (
	PairDevice      = "device"
	PairEnvironment = "environment"
)

// HubPairRequest asks a hub to approve a device or an environment.
type HubPairRequest struct {
	Kind string `json:"kind"` // PairDevice or PairEnvironment
	// Label says what's asking, like "AgentBox app on lint-pc": the approval
	// shows it, and the hub lists the device under it.
	Label string `json:"label"`
	// Name is the environment's name, for PairEnvironment: lowercase letters,
	// digits and dashes. A hub adds a number when the user already has one.
	Name     string `json:"name,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Version  string `json:"version,omitempty"`
}

// HubPairing answers a HubPairRequest. Secret stays with whoever asked; Code
// and ApproveURL are shown, for a connected device to approve the pairing
// with: open ApproveURL in a browser signed in to the hub, scan QR (the same
// URL) with a phone, or type Code into an AgentBox app.
type HubPairing struct {
	Secret     string    `json:"secret"`
	Code       string    `json:"code"` // like "BCDF-GHJK"
	ApproveURL string    `json:"approveURL"`
	ExpiresAt  time.Time `json:"expiresAt"`
	// Interval is how often to poll PathPairToken, in seconds.
	Interval int `json:"interval"`
	// QR is ApproveURL as a QR code, one string a row, '1' for a dark module
	// and '0' for a light one, without the quiet zone.
	QR []string `json:"qr,omitempty"`
}

type HubPairTokenRequest struct {
	Secret string `json:"secret"`
}

// What a pairing comes to.
const (
	PairPending  = "pending"
	PairApproved = "approved"
	PairDenied   = "denied"
	PairExpired  = "expired"
)

// HubPairResult answers polling PathPairToken. An approved pairing answers
// once, with Session for a device or Environment for an environment; after
// that its secret is unknown.
type HubPairResult struct {
	Status      string               `json:"status"`
	Session     *HubSession          `json:"session,omitempty"`
	Environment *HubEnvironmentToken `json:"environment,omitempty"`
}

// HubPairingInfo is a pending pairing, as the device approving it sees it.
type HubPairingInfo struct {
	Code      string    `json:"code"`
	Kind      string    `json:"kind"`
	Label     string    `json:"label"`
	Name      string    `json:"name,omitempty"`
	Hostname  string    `json:"hostname,omitempty"`
	Version   string    `json:"version,omitempty"`
	Address   string    `json:"address"` // the IP address it asked from
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// HubDevice is a device signed in to a hub: one of its tokens.
type HubDevice struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt,omitzero,omitempty"`
	// Current is the device asking.
	Current bool `json:"current,omitempty"`
}

// HubSession is a signed-in client. Send the token as "Authorization: Bearer <token>".
type HubSession struct {
	Token     string    `json:"token"`
	User      HubUser   `json:"user"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// HubEnvironment is a machine running AgentBox that connects to a hub.
type HubEnvironment struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Online      bool      `json:"online"`
	ConnectedAt time.Time `json:"connectedAt,omitzero,omitempty"` // while online
	LastSeenAt  time.Time `json:"lastSeenAt,omitzero,omitempty"`  // when it last connected or disconnected
	Version     string    `json:"version,omitempty"`              // its AgentBox version
	Hostname    string    `json:"hostname,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	// Transport and Address are where its devices reach it directly, once
	// the hub gave it an address: TransportCloudflare and its public https
	// address, or TransportTailscale and its address in the tailnet.
	Transport string `json:"transport,omitempty"`
	Address   string `json:"address,omitempty"`
}

type HubCreateEnvironmentRequest struct {
	Name string `json:"name"`
}

// HubEnvironmentToken answers adding an environment. The hub shows the token once.
type HubEnvironmentToken struct {
	Environment HubEnvironment `json:"environment"`
	Token       string         `json:"token"`
}

// HubEnvironmentTunnel answers POST PathEnvironmentTunnel: how the
// environment's devices reach it directly, never through the hub. They still
// pair with the daemon's own one-time secret, which the hub never sees: Token
// puts the environment on the transport and does nothing else.
//
// On TransportCloudflare (Transport empty, as from hubs that predate
// Tailscale) it's a Cloudflare Tunnel the hub made, on the public Hostname,
// which the environment runs cloudflared with Token for; devices open
// https://<Hostname>/.
//
// On TransportTailscale, Token is an auth key that joins the environment to
// the hub's tailnet as a node named Hostname, tagged by the key. The
// environment serves on its node with tailscale serve, and reports the
// address that gives it (PathEnvironmentAddress); only the tailnet's devices
// reach it.
type HubEnvironmentTunnel struct {
	Transport string `json:"transport,omitempty"`
	Hostname  string `json:"hostname"`
	Token     string `json:"token"`
	// Tailnet is the tailnet the environment joins, as the hub names it
	// (like "example.com"; "-" is the auth key's own): what a device not on
	// it is told to join.
	Tailnet string `json:"tailnet,omitempty"`
}

// HubEnvironmentAddress is what an environment reports of its node in the
// hub's tailnet once it serves there.
type HubEnvironmentAddress struct {
	Transport string `json:"transport"` // TransportTailscale
	// URL is where devices on the tailnet open it, like
	// https://agentbox-5f3a.tail1234.ts.net/.
	URL string `json:"url"`
	// NodeID is its node's stable ID, which the hub removes it from the
	// tailnet by when the environment is deleted.
	NodeID string `json:"nodeID,omitempty"`
	// IPs are its addresses in the tailnet, 100.x.y.z and fd7a:….
	IPs []string `json:"ips,omitempty"`
}

// Error is how a hub reports a failure, in any answer that isn't a success. It
// is the daemon's shape too, so a client reading through the tunnel doesn't
// have to know whether the hub or the environment behind it refused.
type Error struct {
	Error string `json:"error"`
}
