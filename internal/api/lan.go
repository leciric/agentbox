package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
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
	// URLs are the addresses a phone can open, the likeliest first: the
	// tunnel's, when it's running, then those on the local network.
	URLs []string `json:"urls"`
	// Tunnel is reaching the page from anywhere, through a Cloudflare Tunnel.
	Tunnel LANTunnel `json:"tunnel"`
	// WebVersion is the version of the app the phone is served, which the
	// desktop app installs (PUT /v1/lan/web/...); "" until it has.
	WebVersion string     `json:"webVersion,omitempty"`
	Phones     []LANPhone `json:"phones"`
}

// LANTunnel is where the phone tunnel has got to: a Cloudflare Tunnel the
// daemon runs, which brings the phone page to an https address on the
// internet, so a phone can chat from anywhere. Off by default.
type LANTunnel struct {
	Enabled bool `json:"enabled"`
	// Named says it's the user's own named tunnel, from a token they made in
	// Cloudflare's dashboard, on Hostname; otherwise it's a quick tunnel, on
	// a trycloudflare.com address that changes each time it starts.
	Named    bool   `json:"named"`
	Hostname string `json:"hostname,omitempty"`
	// State is "off", "starting" (downloading cloudflared, or connecting),
	// "running" or "failed" (it's tried again after a while).
	State string `json:"state"`
	// URL is the https address phones open, once it's running.
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
	// Origin is where the tunnel reaches the daemon, in AgentBox's own
	// machine or VM: what a named tunnel's public hostname has to point at in
	// Cloudflare's dashboard.
	Origin string `json:"origin"`
}

// LANPhone is a paired phone.
type LANPhone struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Paired   time.Time `json:"paired"`
	LastSeen time.Time `json:"lastSeen,omitzero,omitempty"`
	LastAddr string    `json:"lastAddr,omitempty"`
	// Scopes are what it may do: "read", "chat" (send messages, cancel,
	// answer a chat's permission requests), "answer" (agents' questions) and
	// "merge" (pull requests, each confirmed on the phone). Pairing gives the
	// first three; merge is only ever turned on in the app.
	Scopes []string `json:"scopes"`
}

// UpdateLANPhoneRequest is PATCH /v1/lan/phones/{id}: what the phone may do
// from now on, every scope it keeps.
type UpdateLANPhoneRequest struct {
	Scopes []string `json:"scopes"`
}

// LANAuditEntry is one write a phone made, or tried to: GET
// /v1/lan/phones/{id}/audit, newest first.
type LANAuditEntry struct {
	ID int64 `json:"id"`
	// Scope is what the route needs; empty for one no phone may use.
	Scope string `json:"scope,omitempty"`
	Route string `json:"route"` // method and path
	// Status is the HTTP status it got; Result is "ok", "refused" (the phone
	// may not) or "failed".
	Status int       `json:"status"`
	Result string    `json:"result"`
	Addr   string    `json:"addr"`
	At     time.Time `json:"at"`
}

// LANAudit is a page of a phone's audit. More says there are older entries:
// ask again with ?before= the last one's ID.
type LANAudit struct {
	Entries []LANAuditEntry `json:"entries"`
	More    bool            `json:"more,omitempty"`
}

// UpdateLANRequest is PATCH /v1/lan.
type UpdateLANRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
	Port    *int  `json:"port,omitempty"`
	// Tunnel turns reaching the page from anywhere on or off.
	Tunnel *bool `json:"tunnel,omitempty"`
	// TunnelToken and TunnelHostname make it a named tunnel: both, or ""
	// for both to go back to a quick one. The token is stored sealed and is
	// never sent back.
	TunnelToken    *string `json:"tunnelToken,omitempty"`
	TunnelHostname *string `json:"tunnelHostname,omitempty"`
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
	// Bearer asks for the phone's token in the answer (LANSession.Token), for
	// an app that sends it as "Authorization: Bearer" rather than keeping the
	// cookie: the native phone app (mobile/), which keeps it in the phone's
	// secure storage. The web page never asks, so its token stays HttpOnly.
	Bearer bool `json:"bearer,omitempty"`
}

// LANSession is GET /lan/session on the network port: the phone this is, or
// 401 when it isn't paired.
type LANSession struct {
	Phone LANPhone `json:"phone"`
	// Token is the phone's token, only in the answer to a pairing that asked
	// for it (LANPairRequest.Bearer).
	Token string `json:"token,omitempty"`
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

// UpdateLANPhone changes what a phone may do.
func (c *Client) UpdateLANPhone(ctx context.Context, id string, req UpdateLANPhoneRequest) (LANPhone, error) {
	var out LANPhone
	return out, c.do(ctx, http.MethodPatch, "/v1/lan/phones/"+url.PathEscape(id), req, &out)
}

// LANPhoneAudit is a phone's writes, newest first, from before the entry
// before (0: the newest).
func (c *Client) LANPhoneAudit(ctx context.Context, id string, before int64) (LANAudit, error) {
	var out LANAudit
	path := "/v1/lan/phones/" + url.PathEscape(id) + "/audit"
	if before > 0 {
		path += "?before=" + strconv.FormatInt(before, 10)
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ReportLANHost is the VM supervisor's report on the port it opens on the
// host for phones.
func (c *Client) ReportLANHost(ctx context.Context, req LANHostReport) error {
	return c.do(ctx, http.MethodPut, "/v1/lan/host", req, nil)
}
