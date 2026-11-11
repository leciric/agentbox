package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Presence is how a client (the desktop app, the phone, the native app) tells
// the daemon what someone is looking at, so the daemon spends its background
// work where it is seen: it samples an agent's resources only while one of
// its views asks for them, and backs off the housekeeping it polls for (the
// pull request watch, base syncs, the usage events) while nobody views a
// project, or while the computer is on battery or hot.
//
// A client holds one lease, renewed with PUT /v1/presence while it is open
// (every third of PresenceResponse.TTLSeconds), each renewal replacing what
// it views. A lease not renewed in time lapses on its own, so a client that
// crashes or loses its connection stops counting without saying so; one that
// closes cleanly says DELETE /v1/presence/{client}.
type PresenceRequest struct {
	// Client names the lease: the same one each renewal, a different one per
	// window or device. Up to 200 characters.
	Client string `json:"client"`
	// Kind is what the client is ("desktop", "phone", "native"), for the
	// daemon's diagnostics.
	Kind string `json:"kind,omitempty"`
	// Visible is whether the client is on screen at all: false while it is
	// minimised, hidden or the screen is locked. A visible client showing no
	// project (Home, Settings) still keeps the app-wide meters live.
	Visible bool `json:"visible"`
	// Views is what is on screen now, empty when the client isn't visible.
	Views []PresenceView `json:"views"`
	// Power is the computer's power, when the client knows it: the desktop app
	// reads it from the operating system.
	Power *HostPower `json:"power,omitempty"`
}

// PresenceView is one thing a client shows: a project, or one of its agents.
type PresenceView struct {
	Project string `json:"project"`
	// Agent is the agent's ref ("project/name"), when the view is an agent's.
	Agent string `json:"agent,omitempty"`
	// Resources asks for the agent's live resource samples: while any lease
	// does, the daemon samples it every AgentResourceHistory.IntervalSeconds
	// and publishes each as an EventAgentResources.
	Resources bool `json:"resources,omitempty"`
}

// HostPower is the power of the computer the client runs on.
type HostPower struct {
	OnBattery bool `json:"onBattery"`
	// Thermal is the operating system's thermal state: "nominal", "fair",
	// "serious" or "critical" (macOS and Windows report it; "" elsewhere).
	Thermal string `json:"thermal,omitempty"`
}

// Saving is whether the computer wants work kept down: on battery, or
// running hot.
func (p HostPower) Saving() bool {
	return p.OnBattery || p.Thermal == "serious" || p.Thermal == "critical"
}

// PresenceResponse is the answer to a renewal.
type PresenceResponse struct {
	// TTLSeconds is how long the lease lasts unrenewed.
	TTLSeconds int `json:"ttlSeconds"`
	// Idle is whether the daemon's app-wide housekeeping is backed off now:
	// no client is visible, or the computer is saving power.
	Idle bool `json:"idle"`
}

// PresenceState is GET /v1/presence: every live lease, and what the daemon
// makes of them.
type PresenceState struct {
	Leases []PresenceLease `json:"leases"`
	Idle   bool            `json:"idle"`
	Power  HostPower       `json:"power"`
	// Sampling is the agents whose resources are being sampled, by ref.
	Sampling []string `json:"sampling"`
}

// PresenceLease is one client's lease.
type PresenceLease struct {
	Client  string         `json:"client"`
	Kind    string         `json:"kind,omitempty"`
	Visible bool           `json:"visible"`
	Views   []PresenceView `json:"views"`
	Power   *HostPower     `json:"power,omitempty"`
	Expires time.Time      `json:"expires"`
}

// AgentResourcePoint is one live sample of an agent's machine: CPU in percent
// of one core (100 is a full core, so a busy build can pass it), memory and
// swap held at the time in bytes, and disk read and written over the interval
// in bytes a second.
type AgentResourcePoint struct {
	At        time.Time `json:"at"`
	CPU       float64   `json:"cpu"`
	Memory    int64     `json:"memory"`
	Swap      int64     `json:"swap"`
	DiskRead  int64     `json:"diskRead"`
	DiskWrite int64     `json:"diskWrite"`
}

// AgentResourceHistory is GET /v1/agents/{project}/{agent}/resources: the
// samples taken while someone watched, oldest first, over at most
// WindowSeconds. They are kept in the daemon's memory only, and only for an
// agent watched since the daemon started.
type AgentResourceHistory struct {
	Ref             string `json:"ref"`
	IntervalSeconds int    `json:"intervalSeconds"`
	WindowSeconds   int    `json:"windowSeconds"`
	// Sampling is whether a lease asks for this agent's samples now.
	Sampling bool                 `json:"sampling"`
	Points   []AgentResourcePoint `json:"points"`
}

// AgentResourcesEvent is EventAgentResources' data: one new sample of an
// agent someone watches.
type AgentResourcesEvent struct {
	Ref   string             `json:"ref"`
	Point AgentResourcePoint `json:"point"`
}

// EventAgentResources is a new live sample of a watched agent's resources.
const EventAgentResources = "agent-resources"

// RenewPresence takes or renews the client's lease.
func (c *Client) RenewPresence(ctx context.Context, req PresenceRequest) (PresenceResponse, error) {
	var out PresenceResponse
	err := c.do(ctx, http.MethodPut, "/v1/presence", req, &out)
	return out, err
}

// EndPresence gives the client's lease up.
func (c *Client) EndPresence(ctx context.Context, client string) error {
	return c.do(ctx, http.MethodDelete, "/v1/presence/"+url.PathEscape(client), nil, nil)
}

// Presence reads every live lease.
func (c *Client) Presence(ctx context.Context) (PresenceState, error) {
	var out PresenceState
	err := c.do(ctx, http.MethodGet, "/v1/presence", nil, &out)
	return out, err
}

// AgentResources reads an agent's live resource samples.
func (c *Client) AgentResources(ctx context.Context, project, name string) (AgentResourceHistory, error) {
	var out AgentResourceHistory
	err := c.do(ctx, http.MethodGet, "/v1/agents/"+url.PathEscape(project)+"/"+url.PathEscape(name)+"/resources", nil, &out)
	return out, err
}
