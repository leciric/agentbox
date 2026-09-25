package hubapi

import "time"

// The JSON a hub's own API exchanges. A hub serves these; the CLI, the desktop
// app and the web app read them.

// HubUser is an account on a hub.
type HubUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type HubSignupRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name,omitempty"`
	Password string `json:"password"`
	Label    string `json:"label,omitempty"` // what signs in, like "desktop app"
}

type HubLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Label    string `json:"label,omitempty"`
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
}

type HubCreateEnvironmentRequest struct {
	Name string `json:"name"`
}

// HubEnvironmentToken answers adding an environment. The hub shows the token once.
type HubEnvironmentToken struct {
	Environment HubEnvironment `json:"environment"`
	Token       string         `json:"token"`
}

// Error is how a hub reports a failure, in any answer that isn't a success. It
// is the daemon's shape too, so a client reading through the tunnel doesn't
// have to know whether the hub or the environment behind it refused.
type Error struct {
	Error string `json:"error"`
}
