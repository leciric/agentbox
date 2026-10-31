package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agentbox/hubapi"
)

// Stable addresses for environments' phones (POST hubapi.PathEnvironmentTunnel):
// the hub makes each environment a Cloudflare Tunnel and a hostname in its
// zone the first time the environment asks, and answers the same hostname, and
// the tunnel's token, every time after. The environment runs cloudflared with
// the token, and phones reach it through Cloudflare, never through the hub.
// The token runs the tunnel and nothing else: phones still pair with the
// environment's own one-time secret, which the hub never sees.
//
// A hub started with --transport tailscale answers the same request with an
// auth key into its tailnet instead (tailscale.go): the environment joins as
// a node of its own, serves there, and reports the address it got
// (POST hubapi.PathEnvironmentAddress), which the hub lists for the user's
// devices. Pairing is the same either way.

// DefaultTunnelIdle is how long an environment's tunnel is kept after the
// environment was last connected or last asked for it.
const DefaultTunnelIdle = 60 * 24 * time.Hour

// environmentTunnel answers an environment asking for its stable address,
// with its environment token.
func (s *Server) environmentTunnel(w http.ResponseWriter, r *http.Request) error {
	if s.Cloudflare == nil && s.Tailscale == nil {
		return httpError{http.StatusNotImplemented, "this hub doesn't give out addresses for phones: it has no Cloudflare zone or tailnet"}
	}
	e, err := s.environmentOf(r)
	if err != nil {
		return err
	}
	if s.Tailscale != nil {
		out, err := s.ensureNode(r.Context(), e)
		if err != nil {
			return httpError{http.StatusBadGateway, "the hub couldn't put this environment in its tailnet: " + err.Error()}
		}
		return writeJSON(w, http.StatusOK, out)
	}
	out, err := s.ensureTunnel(r.Context(), e)
	if err != nil {
		return httpError{http.StatusBadGateway, "the hub couldn't make this environment's address: " + err.Error()}
	}
	return writeJSON(w, http.StatusOK, out)
}

// environmentOf is the environment whose token r carries.
func (s *Server) environmentOf(r *http.Request) (Environment, error) {
	token := bearer(r)
	if !strings.HasPrefix(token, environmentPrefix) {
		return Environment{}, httpError{http.StatusUnauthorized, "ask with an environment token"}
	}
	e, err := s.Store.EnvironmentByToken(r.Context(), hashToken(token))
	if errors.Is(err, ErrNotFound) {
		return Environment{}, httpError{http.StatusUnauthorized, "unknown environment token"}
	}
	return e, err
}

// tunnelHostname is an environment's hostname in the zone: its ID's random
// part, which no other environment has, and which stays the same when its
// tunnel is made again.
func tunnelHostname(e Environment, zone string) string {
	return strings.TrimPrefix(e.ID, "env_") + "." + zone
}

func (s *Server) ensureTunnel(ctx context.Context, e Environment) (hubapi.HubEnvironmentTunnel, error) {
	// One at a time: two requests from one environment make one tunnel.
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	cf := s.Cloudflare
	now := time.Now()
	t, err := s.Store.EnvironmentTunnel(ctx, e.ID)
	switch {
	case err == nil && t.transport() != hubapi.TransportCloudflare:
		// Its node from when the hub was on Tailscale: a tunnel replaces it.
		if err := s.removeTunnelLocked(ctx, t); err != nil {
			return hubapi.HubEnvironmentTunnel{}, err
		}
	case err == nil:
		token, err := cf.tunnelToken(ctx, t.TunnelID)
		if err == nil {
			_ = s.Store.TunnelRequested(ctx, e.ID, now)
			return hubapi.HubEnvironmentTunnel{Hostname: t.Hostname, Token: token}, nil
		}
		if !isCloudflareNotFound(err) {
			return hubapi.HubEnvironmentTunnel{}, err
		}
		// Deleted in Cloudflare's dashboard: made again, on the same hostname.
		s.logf("the tunnel for environment %s is gone from Cloudflare: making it again", e.Name)
	case !errors.Is(err, ErrNotFound):
		return hubapi.HubEnvironmentTunnel{}, err
	}
	zone, _, err := cf.zone(ctx)
	if err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	hostname := tunnelHostname(e, zone)
	tunnelID, err := cf.ensureTunnel(ctx, "agentbox-"+strings.TrimPrefix(e.ID, "env_"))
	if err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	if err := cf.routeTunnel(ctx, tunnelID, hostname, hubapi.EnvironmentTunnelOrigin); err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	recordID, err := cf.ensureDNS(ctx, hostname, tunnelID)
	if err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	t = EnvironmentTunnel{EnvironmentID: e.ID, Transport: hubapi.TransportCloudflare, Hostname: hostname, TunnelID: tunnelID, DNSRecordID: recordID, CreatedAt: now, RequestedAt: now}
	if err := s.Store.SaveEnvironmentTunnel(ctx, t); err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	token, err := cf.tunnelToken(ctx, tunnelID)
	if err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	s.logf("made the tunnel for environment %s, on %s", e.Name, hostname)
	return hubapi.HubEnvironmentTunnel{Hostname: hostname, Token: token}, nil
}

// CleanTunnels removes from Cloudflare, and then from the hub, the tunnels of
// environments that were deleted, and of those neither connected nor asking
// for their tunnel for longer than TunnelIdle. One that comes back gets its
// tunnel again, on the same hostname.
func (s *Server) CleanTunnels(ctx context.Context, now time.Time) error {
	if s.Cloudflare == nil && s.Tailscale == nil {
		return nil
	}
	idle := s.TunnelIdle
	if idle == 0 {
		idle = DefaultTunnelIdle
	}
	tunnels, err := s.Store.EnvironmentTunnels(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tunnels {
		if !s.canRemove(t) {
			continue
		}
		e, err := s.Store.Environment(ctx, t.EnvironmentID)
		why := ""
		switch {
		case errors.Is(err, ErrNotFound):
			why = "its environment was deleted"
		case err != nil:
			errs = append(errs, err)
			continue
		case s.online(e.ID):
		case now.Sub(latest(e.LastSeenAt, t.RequestedAt, t.CreatedAt)) > idle:
			why = "environment " + e.Name + " hasn't been seen since " + latest(e.LastSeenAt, t.RequestedAt).Format(time.DateOnly)
		}
		if why == "" {
			continue
		}
		if err := s.removeTunnel(ctx, t); err != nil {
			errs = append(errs, err)
			continue
		}
		s.logf("removed the %s address of %s: %s", t.transport(), t.Hostname, why)
	}
	return errors.Join(errs...)
}

// canRemove says the hub has what it takes to remove t: its Cloudflare
// account for a tunnel, its tailnet for a node. One it can't is kept until it
// can.
func (s *Server) canRemove(t EnvironmentTunnel) bool {
	if t.transport() == hubapi.TransportTailscale {
		return s.Tailscale != nil
	}
	return s.Cloudflare != nil
}

func (s *Server) removeTunnel(ctx context.Context, t EnvironmentTunnel) error {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	return s.removeTunnelLocked(ctx, t)
}

// removeTunnelLocked removes t from Cloudflare or the tailnet, as far as the
// hub can, and then from the hub, with tunnelMu held. A tunnel the hub has no
// Cloudflare account for any more, or a node it made with an auth key of its
// own rather than an OAuth client, is left to the dashboard.
func (s *Server) removeTunnelLocked(ctx context.Context, t EnvironmentTunnel) error {
	switch {
	case t.transport() == hubapi.TransportTailscale && s.Tailscale != nil:
		if err := s.Tailscale.removeNode(ctx, t.TunnelID); err != nil {
			return err
		}
	case t.transport() == hubapi.TransportCloudflare && s.Cloudflare != nil:
		if err := s.Cloudflare.removeTunnel(ctx, t.TunnelID, t.DNSRecordID); err != nil {
			return err
		}
	default:
		s.logf("forgetting the %s address of %s, which this hub can't remove: remove it in %s's dashboard", t.transport(), t.Hostname, t.transport())
	}
	return s.Store.DeleteEnvironmentTunnel(ctx, t.EnvironmentID)
}

// nodeName is the name the hub gives an environment's node in its tailnet:
// its name, which the user knows it by, and the start of its ID, which no
// other environment's starts with. Tailscale adds a number to a name it
// already has; the environment reports the one it got.
func nodeName(e Environment) string {
	id := strings.TrimPrefix(e.ID, "env_")
	if len(id) > 6 {
		id = id[:6]
	}
	return "agentbox-" + e.Name + "-" + id
}

// ensureNode answers an environment asking for its address on a Tailscale
// hub: the name of its node and a key to join the tailnet with. It is made
// each time, so a node removed in the admin console can join again.
func (s *Server) ensureNode(ctx context.Context, e Environment) (hubapi.HubEnvironmentTunnel, error) {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	ts := s.Tailscale
	now := time.Now()
	t, err := s.Store.EnvironmentTunnel(ctx, e.ID)
	switch {
	case err == nil && t.transport() == hubapi.TransportTailscale:
	case err == nil:
		// Its Cloudflare Tunnel from before the hub moved to Tailscale.
		if err := s.removeTunnelLocked(ctx, t); err != nil {
			return hubapi.HubEnvironmentTunnel{}, err
		}
		t = EnvironmentTunnel{}
	case !errors.Is(err, ErrNotFound):
		return hubapi.HubEnvironmentTunnel{}, err
	}
	if t.EnvironmentID == "" {
		t = EnvironmentTunnel{EnvironmentID: e.ID, Transport: hubapi.TransportTailscale, Hostname: nodeName(e), CreatedAt: now}
	}
	key, err := ts.authKey(ctx, e.ID)
	if err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	t.RequestedAt = now
	if err := s.Store.SaveEnvironmentTunnel(ctx, t); err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	// The key is a password to the tailnet: it's never logged.
	s.logf("gave environment %s a key into the tailnet %s, as %s", e.Name, ts.tailnet(), t.Hostname)
	return hubapi.HubEnvironmentTunnel{Transport: hubapi.TransportTailscale, Hostname: t.Hostname, Token: key, Tailnet: ts.tailnet()}, nil
}

var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

// environmentAddress takes what an environment on a Tailscale hub reports of
// its node: where devices on the tailnet reach it.
func (s *Server) environmentAddress(w http.ResponseWriter, r *http.Request) error {
	if s.Tailscale == nil {
		return httpError{http.StatusConflict, "this hub doesn't put environments in a tailnet"}
	}
	e, err := s.environmentOf(r)
	if err != nil {
		return err
	}
	var req hubapi.HubEnvironmentAddress
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		return httpError{http.StatusBadRequest, "that isn't an address: " + err.Error()}
	}
	if req.Transport != hubapi.TransportTailscale {
		return httpError{http.StatusBadRequest, "this hub's environments are on Tailscale, not " + strconv.Quote(req.Transport)}
	}
	address, err := tailnetAddress(req.URL)
	if err != nil {
		return httpError{http.StatusBadRequest, err.Error()}
	}
	if req.NodeID != "" && !nodeIDPattern.MatchString(req.NodeID) {
		return httpError{http.StatusBadRequest, "that isn't a node's ID"}
	}
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	t, err := s.Store.EnvironmentTunnel(r.Context(), e.ID)
	if errors.Is(err, ErrNotFound) || (err == nil && t.transport() != hubapi.TransportTailscale) {
		return httpError{http.StatusConflict, "ask for this environment's address first (POST " + hubapi.PathEnvironmentTunnel + ")"}
	} else if err != nil {
		return err
	}
	if t.Address != address || t.TunnelID != req.NodeID {
		t.Address, t.TunnelID = address, req.NodeID
		if err := s.Store.SaveEnvironmentTunnel(r.Context(), t); err != nil {
			return err
		}
		s.logf("environment %s is at %s in the tailnet", e.Name, address)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// tailnetAddress is an environment's address in the tailnet, from what it
// reported: an https URL on its MagicDNS name, with nothing after the host.
func tailnetAddress(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("%q isn't an address in a tailnet, like https://agentbox-vps-3fa9c0.tail1234.ts.net/", raw)
	}
	host := strings.ToLower(u.Hostname())
	if !hostnamePattern.MatchString(host) {
		return "", fmt.Errorf("%q isn't a MagicDNS name", host)
	}
	if u.Port() != "" {
		host += ":" + u.Port()
	}
	return "https://" + host + "/", nil
}

var hostnamePattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// CleanTunnelsEvery runs CleanTunnels every interval until ctx ends.
func (s *Server) CleanTunnelsEvery(ctx context.Context, interval time.Duration) {
	for {
		if err := s.CleanTunnels(ctx, time.Now()); err != nil && ctx.Err() == nil {
			s.logf("cleaning up tunnels: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (s *Server) online(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tunnels[id] != nil
}

func latest(times ...time.Time) time.Time {
	var out time.Time
	for _, t := range times {
		if t.After(out) {
			out = t
		}
	}
	return out
}
