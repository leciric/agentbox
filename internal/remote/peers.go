package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"agentbox/hubapi"
)

// ErrNoPeers says the hub doesn't let its environments reach each other: it
// predates peers.
var ErrNoPeers = errors.New("this hub doesn't let machines reach each other: update it")

// Peers lists the other environments of this one's owner, with the
// environment's token: the machines its leads can direct agents on.
func Peers(ctx context.Context, client *http.Client, hub, token string) ([]hubapi.HubEnvironment, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(hub, "/")+hubapi.PathPeers, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoPeers
	}
	if resp.StatusCode != http.StatusOK {
		var e hubapi.Error
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("the hub answered %d: %s", resp.StatusCode, e.Error)
		}
		return nil, fmt.Errorf("the hub answered %s", resp.Status)
	}
	var out []hubapi.HubEnvironment
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("the hub's answer isn't a list of machines: %w", err)
	}
	return out, nil
}

// PeerURL is where a peer's daemon API is reached through hub: requests to
// it carry the environment's own token, and the peer sees which environment
// sent them (hubapi.HeaderPeer).
func PeerURL(hub, id string) string {
	return strings.TrimRight(hub, "/") + hubapi.PeerAPI(url.PathEscape(id))
}
