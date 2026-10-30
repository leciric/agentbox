package api

// EventTailnet is published when the tailnet listener changes: turned on or
// off, allowed someone else in, or failed to open.
const EventTailnet = "tailnet"

// TailnetStatus is GET /v1/tailnet: the daemon listening for `tailscale serve`,
// which publishes it as https://<machine>.<tailnet>.ts.net. The publishing is
// tailscale's own business, so nothing here says whether it is up; the
// command line tool asks tailscale for that.
type TailnetStatus struct {
	Enabled bool `json:"enabled"`
	// Addr is the loopback address the daemon listens on, like
	// 127.0.0.1:7781. The tailnet reaches it through the proxy, never
	// directly.
	Addr string `json:"addr"`
	// Listening says the address is open. False with Enabled means it
	// couldn't be: Error says why.
	Listening bool   `json:"listening"`
	Error     string `json:"error,omitempty"`
	// Allow are the Tailscale logins let in, as Tailscale spells them
	// (alice@example.com, or name@github).
	Allow []string `json:"allow"`
}

// UpdateTailnetRequest is PATCH /v1/tailnet. A nil field is left as it is.
type UpdateTailnetRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
	// Allow replaces the logins let in. It is refused empty while enabling:
	// the listener would otherwise take anyone the tailnet carries.
	Allow *[]string `json:"allow,omitempty"`
}
