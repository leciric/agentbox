package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Sharing projects with another AgentBox (internal/daemon/share.go). The
// owner pairs another AgentBox, a peer, with a one-time link made under
// Settings → Share; the peer gets a revocable token of its own and reaches
// the projects it was given, through the owner's phone page (its LAN port or
// its Cloudflare Tunnel). Agents a peer makes run on the owner's machine and
// AI accounts, within the peer's slots.

// EventShare is published, with no data, when anything in ShareStatus
// changes.
const EventShare = "share"

// ShareStatus is GET /v1/share.
type ShareStatus struct {
	// Name is what this AgentBox is called on the others: the computer's
	// name unless the user chose one.
	Name string `json:"name"`
	// Slots is how many agents a peer may have running here when it has no
	// limit of its own.
	Slots int `json:"slots"`
	// Ready says this machine can make a pairing link: its phone page is on
	// and has an address (Settings → Share → Phones). Error says why not.
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
	// Peers are the AgentBoxes paired to reach this machine's projects.
	Peers []SharePeer `json:"peers"`
	// Remotes are the AgentBoxes this one paired with.
	Remotes []ShareRemote `json:"remotes"`
}

// SharePeer is another AgentBox that reaches some of this machine's projects.
type SharePeer struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Paired   time.Time `json:"paired"`
	LastSeen time.Time `json:"lastSeen,omitzero"`
	LastAddr string    `json:"lastAddr,omitempty"`
	// AllProjects shares every project, those added later included;
	// otherwise Projects, by name.
	AllProjects bool     `json:"allProjects"`
	Projects    []string `json:"projects"`
	// Slots is its own limit of agents running here, -1 for ShareStatus.Slots;
	// Running is how many of the agents it made run now.
	Slots   int `json:"slots"`
	Running int `json:"running"`
}

// UpdateShareRequest is PATCH /v1/share.
type UpdateShareRequest struct {
	Slots *int    `json:"slots,omitempty"`
	Name  *string `json:"name,omitempty"`
}

// UpdateSharePeerRequest is PATCH /v1/share/peers/{id}: what is left out
// stays as it is.
type UpdateSharePeerRequest struct {
	AllProjects *bool     `json:"allProjects,omitempty"`
	Projects    *[]string `json:"projects,omitempty"`
	// Slots is -1 to follow ShareStatus.Slots.
	Slots *int `json:"slots,omitempty"`
}

// SharePairing is POST /v1/share/pairings: a one-time link another
// AgentBox pastes to pair, valid until Expires.
type SharePairing struct {
	Link    string    `json:"link"`
	Links   []string  `json:"links"` // every address it can be reached at, the likeliest first
	Expires time.Time `json:"expires"`
}

// ShareRemote is another AgentBox this one paired with: the owner of the
// projects it shares here.
type ShareRemote struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	URL    string    `json:"url"`
	Paired time.Time `json:"paired"`
	// Projects are how many projects it shares now, and Error why it
	// couldn't be reached the last time it was asked.
	Projects int    `json:"projects"`
	Error    string `json:"error,omitempty"`
}

// AddShareRemoteRequest is POST /v1/share/remotes: the link the owner made.
type AddShareRemoteRequest struct {
	Link string `json:"link"`
}

// ProjectRemote is set on a Project that another AgentBox shares with this
// one: whose it is. Its Name is "<remote>~<project>", which the daemon
// passes on to that AgentBox.
type ProjectRemote struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
}

// SharePairRequest is what a peer's daemon sends the owner's
// /share/pair: the link's secret, and what it calls itself.
type SharePairRequest struct {
	Secret string `json:"secret"`
	Name   string `json:"name"`
}

// SharePairResponse is the owner's answer: the peer's token, and what the
// owner calls itself.
type SharePairResponse struct {
	Token string `json:"token"`
	Owner string `json:"owner"`
}

// ErrorShareSlotsFull is the Code of a request from a paired AgentBox to
// make an agent when as many of its agents run as its slots allow.
const ErrorShareSlotsFull = "share-slots-full"

// Share is what this AgentBox shares, with whom, and whose projects it shows.
func (c *Client) Share(ctx context.Context) (ShareStatus, error) {
	var out ShareStatus
	return out, c.do(ctx, http.MethodGet, "/v1/share", nil, &out)
}

// UpdateShare changes the slots every peer gets, or this AgentBox's name.
func (c *Client) UpdateShare(ctx context.Context, req UpdateShareRequest) (ShareStatus, error) {
	var out ShareStatus
	return out, c.do(ctx, http.MethodPatch, "/v1/share", req, &out)
}

// PairShare makes a one-time link another AgentBox pairs with.
func (c *Client) PairShare(ctx context.Context) (SharePairing, error) {
	var out SharePairing
	return out, c.do(ctx, http.MethodPost, "/v1/share/pairings", nil, &out)
}

// UpdateSharePeer changes what a peer reaches and its slots.
func (c *Client) UpdateSharePeer(ctx context.Context, id string, req UpdateSharePeerRequest) (SharePeer, error) {
	var out SharePeer
	return out, c.do(ctx, http.MethodPatch, "/v1/share/peers/"+url.PathEscape(id), req, &out)
}

// RemoveSharePeer unpairs a peer.
func (c *Client) RemoveSharePeer(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/share/peers/"+url.PathEscape(id), nil, nil)
}

// AddShareRemote pairs with another AgentBox, with the link it made.
func (c *Client) AddShareRemote(ctx context.Context, link string) (ShareRemote, error) {
	var out ShareRemote
	return out, c.do(ctx, http.MethodPost, "/v1/share/remotes", AddShareRemoteRequest{Link: link}, &out)
}

// RemoveShareRemote forgets a pairing with another AgentBox.
func (c *Client) RemoveShareRemote(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/share/remotes/"+url.PathEscape(id), nil, nil)
}
