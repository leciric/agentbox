package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"agentbox/hubapi"
)

// HubClient talks to a hub's own API: signing in, and environments.
type HubClient struct {
	URL   string // like https://hub.example.com
	Token string // the session; empty before signing in
}

func (h HubClient) client() *Client {
	return NewRemoteClient(strings.TrimRight(h.URL, "/"), h.Token)
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
