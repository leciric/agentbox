package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Chatting from a phone on the local network (internal/daemon/lan.go): the
// daemon serves the app's web version on a port of the network, to phones
// paired with a QR code, and to nothing else.

// EventLAN is published, with no data, when anything in LANStatus changes:
// turned on or off, a phone paired or revoked, the host's forward coming up.
const EventLAN = "lan"

// LANStatus is GET /v1/lan.
type LANStatus struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
	// Listening says the port is open on the network: the daemon's own when
	// it runs on this machine, the host's when it runs in AgentBox's VM.
	Listening bool `json:"listening"`
	// Error is why it isn't, when it should be.
	Error string `json:"error,omitempty"`
	// URLs are the addresses a phone on the network can open, the likeliest
	// first.
	URLs []string `json:"urls"`
	// WebVersion is the version of the app the phone is served, which the
	// desktop app installs (PUT /v1/lan/web/...); "" until it has.
	WebVersion string     `json:"webVersion,omitempty"`
	Phones     []LANPhone `json:"phones"`
}

// LANPhone is a paired phone.
type LANPhone struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Paired   time.Time `json:"paired"`
	LastSeen time.Time `json:"lastSeen,omitzero,omitempty"`
	LastAddr string    `json:"lastAddr,omitempty"`
}

// UpdateLANRequest is PATCH /v1/lan.
type UpdateLANRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
	Port    *int  `json:"port,omitempty"`
}

// LANPairing is POST /v1/lan/pairings: a one-time secret, valid until
// Expires, in a URL for each of LANStatus.URLs. QR is the first URL as a QR
// code, one string of '1' (dark) and '0' a row, without its quiet zone.
type LANPairing struct {
	URLs    []string  `json:"urls"`
	QR      []string  `json:"qr"`
	Expires time.Time `json:"expires"`
}

// LANHostReport is PUT /v1/lan/host: what the supervisor of AgentBox's VM on
// a Linux host says about the port it opens on the host's network for the
// daemon in the VM. It sends one every few seconds, and a report older than
// a few of those is taken as the supervisor gone.
type LANHostReport struct {
	Port      int      `json:"port"`
	Listening bool     `json:"listening"`
	Error     string   `json:"error,omitempty"`
	Addresses []string `json:"addresses"`
}

// LANPairRequest is what the phone sends to pair: POST /lan/pair on the
// network port, with the secret from the QR code.
type LANPairRequest struct {
	Secret string `json:"secret"`
	Name   string `json:"name"`
}

// LANSession is GET /lan/session on the network port: the phone this is, or
// 401 when it isn't paired.
type LANSession struct {
	Phone LANPhone `json:"phone"`
}

// LAN reports whether phones can chat from the local network, and which are
// paired.
func (c *Client) LAN(ctx context.Context) (LANStatus, error) {
	var out LANStatus
	return out, c.do(ctx, http.MethodGet, "/v1/lan", nil, &out)
}

// UpdateLAN turns chatting from a phone on or off, or moves its port.
func (c *Client) UpdateLAN(ctx context.Context, req UpdateLANRequest) (LANStatus, error) {
	var out LANStatus
	return out, c.do(ctx, http.MethodPatch, "/v1/lan", req, &out)
}

// PairLAN makes a one-time pairing for a phone.
func (c *Client) PairLAN(ctx context.Context) (LANPairing, error) {
	var out LANPairing
	return out, c.do(ctx, http.MethodPost, "/v1/lan/pairings", nil, &out)
}

// RemoveLANPhone unpairs a phone.
func (c *Client) RemoveLANPhone(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/lan/phones/"+url.PathEscape(id), nil, nil)
}

// ReportLANHost is the VM supervisor's report on the port it opens on the
// host for phones.
func (c *Client) ReportLANHost(ctx context.Context, req LANHostReport) error {
	return c.do(ctx, http.MethodPut, "/v1/lan/host", req, nil)
}
