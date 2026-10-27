package api

import (
	"context"
	"net/http"
)

// TailnetStatus is GET /v1/tailnet: whether the daemon listens for
// `tailscale serve`, where, and who it lets in.
func (c *Client) TailnetStatus(ctx context.Context) (TailnetStatus, error) {
	var out TailnetStatus
	return out, c.do(ctx, http.MethodGet, "/v1/tailnet", nil, &out)
}

// UpdateTailnet opens or closes the tailnet listener, and sets who may use it.
func (c *Client) UpdateTailnet(ctx context.Context, req UpdateTailnetRequest) (TailnetStatus, error) {
	var out TailnetStatus
	return out, c.do(ctx, http.MethodPatch, "/v1/tailnet", req, &out)
}
