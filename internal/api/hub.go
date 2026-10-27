package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentbox/hubapi"
)

// HubClient talks to a hub's own API: pairing, devices and environments.
type HubClient struct {
	URL   string // like https://hub.example.com
	Token string // the session; empty before signing in
}

func (h HubClient) client() *Client {
	return NewRemoteClient(strings.TrimRight(h.URL, "/"), h.Token)
}

func (h HubClient) Info(ctx context.Context) (HubInfo, error) {
	var out HubInfo
	return out, h.client().do(ctx, http.MethodGet, hubapi.PathInfo, nil, &out)
}

// Setup takes a setup link's secret: the hub's owner signs in with it, or
// becomes its owner.
func (h HubClient) Setup(ctx context.Context, req HubSetupRequest) (HubSession, error) {
	var out HubSession
	return out, h.client().do(ctx, http.MethodPost, hubapi.PathSetup, req, &out)
}

// Pair asks the hub to approve a device or an environment.
func (h HubClient) Pair(ctx context.Context, req HubPairRequest) (HubPairing, error) {
	var out HubPairing
	return out, h.client().do(ctx, http.MethodPost, hubapi.PathPair, req, &out)
}

// PairToken asks whether a pairing was approved: its status, and once, its token.
func (h HubClient) PairToken(ctx context.Context, secret string) (HubPairResult, error) {
	var out HubPairResult
	return out, h.client().do(ctx, http.MethodPost, hubapi.PathPairToken, HubPairTokenRequest{Secret: secret}, &out)
}

// WaitForPairing polls a pairing until it's approved, denied or expired,
// every p.Interval seconds.
func (h HubClient) WaitForPairing(ctx context.Context, p HubPairing) (HubPairResult, error) {
	interval := time.Duration(max(p.Interval, 1)) * time.Second
	for {
		select {
		case <-ctx.Done():
			return HubPairResult{}, ctx.Err()
		case <-time.After(interval):
		}
		r, err := h.PairToken(ctx, p.Secret)
		if err != nil {
			return HubPairResult{}, err
		}
		switch r.Status {
		case hubapi.PairPending:
			continue
		case hubapi.PairApproved:
			return r, nil
		case hubapi.PairDenied:
			return r, errors.New("the hub's owner denied it")
		default:
			return r, errors.New("nobody approved it in time: try again")
		}
	}
}

// Pairing is a pending pairing, by its code, for approving it.
func (h HubClient) Pairing(ctx context.Context, code string) (HubPairingInfo, error) {
	var out HubPairingInfo
	return out, h.client().do(ctx, http.MethodGet, hubapi.PathPairings+"/"+url.PathEscape(code), nil, &out)
}

func (h HubClient) ApprovePairing(ctx context.Context, code string) (HubPairingInfo, error) {
	var out HubPairingInfo
	return out, h.client().do(ctx, http.MethodPost, hubapi.PathPairings+"/"+url.PathEscape(code)+"/approve", nil, &out)
}

func (h HubClient) DenyPairing(ctx context.Context, code string) (HubPairingInfo, error) {
	var out HubPairingInfo
	return out, h.client().do(ctx, http.MethodPost, hubapi.PathPairings+"/"+url.PathEscape(code)+"/deny", nil, &out)
}

func (h HubClient) Devices(ctx context.Context) ([]HubDevice, error) {
	var out []HubDevice
	return out, h.client().do(ctx, http.MethodGet, hubapi.PathDevices, nil, &out)
}

func (h HubClient) RevokeDevice(ctx context.Context, id string) error {
	return h.client().do(ctx, http.MethodDelete, hubapi.PathDevices+"/"+url.PathEscape(id), nil, nil)
}

// ParseHubLink reads what someone pasted to connect to a hub: its address,
// or a setup link (https://hub.example.com/hub/setup#abx_l_…), which carries
// the secret that makes them its owner or signs the owner in. It answers the
// hub's address, and the secret when there is one.
func ParseHubLink(s string) (hub, secret string, err error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", "", fmt.Errorf("%q isn't a hub's address: use one like https://hub.example.com", strings.TrimSpace(s))
	}
	if strings.HasPrefix(u.Fragment, hubapi.SetupSecretPrefix) {
		secret = u.Fragment
	}
	return u.Scheme + "://" + u.Host, secret, nil
}

func (h HubClient) Login(ctx context.Context, req HubLoginRequest) (HubSession, error) {
	var out HubSession
	return out, h.client().do(ctx, http.MethodPost, hubapi.PathLogin, req, &out)
}

func (h HubClient) Logout(ctx context.Context) error {
	return h.client().do(ctx, http.MethodPost, hubapi.PathLogout, nil, nil)
}

func (h HubClient) Me(ctx context.Context) (HubUser, error) {
	var out HubUser
	return out, h.client().do(ctx, http.MethodGet, hubapi.PathMe, nil, &out)
}

func (h HubClient) Environments(ctx context.Context) ([]HubEnvironment, error) {
	var out []HubEnvironment
	return out, h.client().do(ctx, http.MethodGet, hubapi.PathEnvironments, nil, &out)
}

// CreateEnvironment adds an environment; its token is in the answer, and only there.
func (h HubClient) CreateEnvironment(ctx context.Context, name string) (HubEnvironmentToken, error) {
	var out HubEnvironmentToken
	return out, h.client().do(ctx, http.MethodPost, hubapi.PathEnvironments, HubCreateEnvironmentRequest{Name: name}, &out)
}

func (h HubClient) DeleteEnvironment(ctx context.Context, id string) error {
	return h.client().do(ctx, http.MethodDelete, hubapi.PathEnvironments+"/"+url.PathEscape(id), nil, nil)
}

// Environment is a client for an environment's daemon, through the hub.
func (h HubClient) Environment(id string) *Client {
	return NewRemoteClient(strings.TrimRight(h.URL, "/")+hubapi.EnvironmentAPI(url.PathEscape(id)), h.Token)
}
