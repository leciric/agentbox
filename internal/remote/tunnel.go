package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"agentbox/hubapi"
)

// ErrNoTunnels says the hub doesn't give out stable addresses for phones: it
// predates them, or has no Cloudflare account to make them on.
var ErrNoTunnels = errors.New("this hub doesn't give out addresses for phones")

// RequestTunnel asks hub for the environment's stable address for phones, with
// the environment's token: the hostname, always the same one, and the token
// cloudflared runs the tunnel to it with.
func RequestTunnel(ctx context.Context, client *http.Client, hub, token string) (hubapi.HubEnvironmentTunnel, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(hub, "/")+hubapi.PathEnvironmentTunnel, bytes.NewReader(nil))
	if err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return hubapi.HubEnvironmentTunnel{}, err
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		return hubapi.HubEnvironmentTunnel{}, ErrNoTunnels
	}
	if resp.StatusCode != http.StatusOK {
		var e hubapi.Error
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return hubapi.HubEnvironmentTunnel{}, fmt.Errorf("the hub answered %d: %s", resp.StatusCode, e.Error)
		}
		return hubapi.HubEnvironmentTunnel{}, fmt.Errorf("the hub answered %s", resp.Status)
	}
	var out hubapi.HubEnvironmentTunnel
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("the hub's answer isn't a tunnel: %w", err)
	}
	if out.Hostname == "" || out.Token == "" {
		return out, errors.New("the hub's answer has no hostname or token")
	}
	return out, nil
}
