// Package hub is AgentBox's hub: accounts, and the environments (machines
// running AgentBox) that connect to it, so the app, the CLI and a browser can
// use every environment from one place. Environments dial out to the hub, and
// the hub proxies the daemon API to them through that connection.
package hub

import (
	"context"
	"errors"
	"time"

	"agentbox/hubapi"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

// User is an account. The owner, who claimed the hub with its setup link, has
// no email and no password; accounts from before pairing have both, and can
// still sign in with them.
type User struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string
	CreatedAt    time.Time
}

// Session is a device: a signed-in client, from an approved pairing or a
// password. Only the token's hash is stored; ID names it for revoking.
type Session struct {
	ID         string
	TokenHash  string
	UserID     string
	Label      string // what signed in: "AgentBox app on lint-pc", "browser"
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// SetupLink is a one-time link made on the hub's machine: with no UserID it
// makes whoever opens it first the owner, and with one it signs that user in.
type SetupLink struct {
	TokenHash string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time // zero: until it's used or replaced
}

// Environment is a machine running AgentBox that connects to the hub with its
// token. Only the token's hash is stored.
type Environment struct {
	ID         string
	OwnerID    string
	Name       string
	TokenHash  string
	CreatedAt  time.Time
	LastSeenAt time.Time // zero until it first connects
	Version    string    // the AgentBox version it last connected with
	Hostname   string
}

// EnvironmentTunnel is how an environment's devices reach it directly. It
// isn't deleted with its environment: the hub needs it to remove the tunnel
// from Cloudflare, or the node from the tailnet, first.
//
// On Cloudflare (Transport hubapi.TransportCloudflare, or "" as kept before
// Tailscale) it's the tunnel the hub made and the DNS record of its
// hostname. On Tailscale, Hostname is the name the hub gave the environment's
// node, and TunnelID and Address are the node's stable ID and its https
// address in the tailnet, once the environment reported them.
type EnvironmentTunnel struct {
	EnvironmentID string
	Transport     string
	Hostname      string
	TunnelID      string
	DNSRecordID   string
	Address       string
	CreatedAt     time.Time
	RequestedAt   time.Time // when the environment last asked for it
}

// transport is t's, with "" read as Cloudflare.
func (t EnvironmentTunnel) transport() string {
	if t.Transport == "" {
		return hubapi.TransportCloudflare
	}
	return t.Transport
}

// address is where devices reach the environment: its Cloudflare hostname,
// or what it reported of its node.
func (t EnvironmentTunnel) address() string {
	if t.transport() == hubapi.TransportCloudflare {
		return "https://" + t.Hostname + "/"
	}
	return t.Address
}

// Store keeps the hub's accounts and environments. SQLStore implements it for
// Postgres and SQLite; another database (Cloudflare D1, say) only needs these
// methods.
type Store interface {
	CountUsers(ctx context.Context) (int, error)
	// CountPasswords counts the accounts that can sign in with a password.
	CountPasswords(ctx context.Context) (int, error)
	// Owner is the first account: the one that claimed the hub, or the
	// oldest of a hub from before pairing.
	Owner(ctx context.Context) (User, error)
	CreateUser(ctx context.Context, u User) error
	UserByEmail(ctx context.Context, email string) (User, error)
	UserByID(ctx context.Context, id string) (User, error)
	SetPassword(ctx context.Context, userID, passwordHash string) error

	CreateSession(ctx context.Context, s Session) error
	// Session returns a session that hasn't expired at now.
	Session(ctx context.Context, tokenHash string, now time.Time) (Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	// DeleteSessions signs a user out everywhere.
	DeleteSessions(ctx context.Context, userID string) error
	// Sessions are a user's devices that haven't expired at now, the most
	// recently seen first.
	Sessions(ctx context.Context, userID string, now time.Time) ([]Session, error)
	// DeleteSessionByID revokes one of a user's devices.
	DeleteSessionByID(ctx context.Context, userID, id string) error
	// TouchSession records that a device was seen, and moves its expiry.
	TouchSession(ctx context.Context, tokenHash string, seen, expires time.Time) error

	// ReplaceSetupLink adds a link, deleting the user's earlier ones (those
	// with no user, for the claim link): only the newest one printed works.
	ReplaceSetupLink(ctx context.Context, l SetupLink) error
	// TakeSetupLink deletes a link that hasn't expired at now and returns
	// it, so that only one caller ever gets it.
	TakeSetupLink(ctx context.Context, tokenHash string, now time.Time) (SetupLink, error)

	CreateEnvironment(ctx context.Context, e Environment) error
	Environments(ctx context.Context, ownerID string) ([]Environment, error)
	Environment(ctx context.Context, id string) (Environment, error)
	EnvironmentByToken(ctx context.Context, tokenHash string) (Environment, error)
	// SeenEnvironment records that an environment connected, or was connected, at seen.
	SeenEnvironment(ctx context.Context, id string, seen time.Time, version, hostname string) error
	DeleteEnvironment(ctx context.Context, id string) error

	// SaveEnvironmentTunnel adds an environment's tunnel or replaces it.
	SaveEnvironmentTunnel(ctx context.Context, t EnvironmentTunnel) error
	EnvironmentTunnel(ctx context.Context, environmentID string) (EnvironmentTunnel, error)
	// EnvironmentTunnels is every tunnel, including those of environments
	// that were deleted.
	EnvironmentTunnels(ctx context.Context) ([]EnvironmentTunnel, error)
	TunnelRequested(ctx context.Context, environmentID string, at time.Time) error
	DeleteEnvironmentTunnel(ctx context.Context, environmentID string) error

	Close() error
}
