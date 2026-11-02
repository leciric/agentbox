package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Cloudflare makes the tunnels environments' phones reach them through, on
// the hub's own Cloudflare zone: a remotely-managed Cloudflare Tunnel for each
// environment, and a proxied CNAME, <hostname> → <tunnel>.cfargotunnel.com,
// for its hostname in the zone. Phone traffic goes from Cloudflare straight to
// the environment's cloudflared; the hub only makes the tunnel and hands its
// token to the environment.
//
// Its API token needs Account › Cloudflare Tunnel › Edit, and Zone › DNS ›
// Edit and Zone › Zone › Read on the zone.
type Cloudflare struct {
	Token  string // the API token, from the hub's configuration: never logged
	ZoneID string // the zone the hostnames are made in
	// API is Cloudflare's API, https://api.cloudflare.com/client/v4 when
	// empty; tests point it at a fake.
	API  string
	HTTP *http.Client

	mu        sync.Mutex
	zoneName  string // looked up once, with the account it belongs to
	accountID string
}

// cloudflareError is Cloudflare's answer when it refuses.
type cloudflareError struct {
	status  int
	message string
}

func (e cloudflareError) Error() string {
	return fmt.Sprintf("Cloudflare answered %d: %s", e.status, e.message)
}

func isCloudflareNotFound(err error) bool {
	var ce cloudflareError
	return errors.As(err, &ce) && ce.status == http.StatusNotFound
}

// call sends one request to Cloudflare's API and decodes the result of its
// envelope into out.
func (c *Cloudflare) call(ctx context.Context, method, path string, body, out any) error {
	base := c.API
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Cloudflare: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var envelope struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return cloudflareError{resp.StatusCode, "an answer that isn't JSON"}
	}
	if resp.StatusCode >= 300 || !envelope.Success {
		var msgs []string
		for _, e := range envelope.Errors {
			msgs = append(msgs, fmt.Sprintf("%s (%d)", e.Message, e.Code))
		}
		if len(msgs) == 0 {
			msgs = append(msgs, resp.Status)
		}
		return cloudflareError{resp.StatusCode, strings.Join(msgs, "; ")}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

// zone is the zone's name, which hostnames end in, and its account's ID,
// which tunnels are made in.
func (c *Cloudflare) zone(ctx context.Context) (name, account string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.zoneName != "" {
		return c.zoneName, c.accountID, nil
	}
	var z struct {
		Name    string `json:"name"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := c.call(ctx, http.MethodGet, "/zones/"+url.PathEscape(c.ZoneID), nil, &z); err != nil {
		return "", "", fmt.Errorf("the zone %s: %w", c.ZoneID, err)
	}
	if z.Name == "" || z.Account.ID == "" {
		return "", "", fmt.Errorf("the zone %s: Cloudflare didn't say its name and account", c.ZoneID)
	}
	c.zoneName, c.accountID = z.Name, z.Account.ID
	return c.zoneName, c.accountID, nil
}

// cloudflareTunnel is the part of a Cloudflare Tunnel the hub reads.
type cloudflareTunnel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ensureTunnel returns the tunnel named name, making it when there's none: a
// tunnel made before the hub could record it is found again, not duplicated.
func (c *Cloudflare) ensureTunnel(ctx context.Context, name string) (string, error) {
	_, account, err := c.zone(ctx)
	if err != nil {
		return "", err
	}
	var found []cloudflareTunnel
	q := url.Values{"name": {name}, "is_deleted": {"false"}}
	if err := c.call(ctx, http.MethodGet, "/accounts/"+url.PathEscape(account)+"/cfd_tunnel?"+q.Encode(), nil, &found); err != nil {
		return "", fmt.Errorf("finding the tunnel %s: %w", name, err)
	}
	for _, t := range found {
		if t.Name == name && t.ID != "" {
			return t.ID, nil
		}
	}
	var made cloudflareTunnel
	if err := c.call(ctx, http.MethodPost, "/accounts/"+url.PathEscape(account)+"/cfd_tunnel",
		map[string]string{"name": name, "config_src": "cloudflare"}, &made); err != nil {
		return "", fmt.Errorf("making the tunnel %s: %w", name, err)
	}
	if made.ID == "" {
		return "", fmt.Errorf("making the tunnel %s: Cloudflare didn't say its ID", name)
	}
	return made.ID, nil
}

// routeTunnel points hostname at origin through the tunnel, and nothing else.
func (c *Cloudflare) routeTunnel(ctx context.Context, tunnelID, hostname, origin string) error {
	_, account, err := c.zone(ctx)
	if err != nil {
		return err
	}
	config := map[string]any{"config": map[string]any{"ingress": []map[string]string{
		{"hostname": hostname, "service": origin},
		{"service": "http_status:404"},
	}}}
	if err := c.call(ctx, http.MethodPut, "/accounts/"+url.PathEscape(account)+"/cfd_tunnel/"+url.PathEscape(tunnelID)+"/configurations", config, nil); err != nil {
		return fmt.Errorf("routing %s through its tunnel: %w", hostname, err)
	}
	return nil
}

// ensureDNS points hostname at the tunnel, proxied, and returns the record's
// ID, updating a record already there rather than adding another.
func (c *Cloudflare) ensureDNS(ctx context.Context, hostname, tunnelID string) (string, error) {
	zone := "/zones/" + url.PathEscape(c.ZoneID) + "/dns_records"
	record := map[string]any{
		"type": "CNAME", "name": hostname, "content": tunnelID + ".cfargotunnel.com", "proxied": true,
		"comment": "AgentBox hub: an environment's address for phones",
	}
	var found []struct {
		ID string `json:"id"`
	}
	q := url.Values{"type": {"CNAME"}, "name": {hostname}}
	if err := c.call(ctx, http.MethodGet, zone+"?"+q.Encode(), nil, &found); err != nil {
		return "", fmt.Errorf("finding %s's DNS record: %w", hostname, err)
	}
	var out struct {
		ID string `json:"id"`
	}
	var err error
	if len(found) > 0 {
		err = c.call(ctx, http.MethodPatch, zone+"/"+url.PathEscape(found[0].ID), record, &out)
	} else {
		err = c.call(ctx, http.MethodPost, zone, record, &out)
	}
	if err != nil {
		return "", fmt.Errorf("pointing %s at its tunnel: %w", hostname, err)
	}
	return out.ID, nil
}

// tunnelToken is what cloudflared runs the tunnel with.
func (c *Cloudflare) tunnelToken(ctx context.Context, tunnelID string) (string, error) {
	_, account, err := c.zone(ctx)
	if err != nil {
		return "", err
	}
	var token string
	if err := c.call(ctx, http.MethodGet, "/accounts/"+url.PathEscape(account)+"/cfd_tunnel/"+url.PathEscape(tunnelID)+"/token", nil, &token); err != nil {
		return "", fmt.Errorf("the tunnel's token: %w", err)
	}
	if token == "" {
		return "", errors.New("the tunnel's token: Cloudflare answered none")
	}
	return token, nil
}

// removeTunnel deletes the DNS record and the tunnel, either of which may
// already be gone. A tunnel with cloudflared still connected is disconnected
// first, which Cloudflare requires before deleting it.
func (c *Cloudflare) removeTunnel(ctx context.Context, tunnelID, dnsRecordID string) error {
	if dnsRecordID != "" {
		err := c.call(ctx, http.MethodDelete, "/zones/"+url.PathEscape(c.ZoneID)+"/dns_records/"+url.PathEscape(dnsRecordID), nil, nil)
		if err != nil && !isCloudflareNotFound(err) {
			return fmt.Errorf("deleting the DNS record: %w", err)
		}
	}
	if tunnelID == "" {
		return nil
	}
	_, account, err := c.zone(ctx)
	if err != nil {
		return err
	}
	tunnel := "/accounts/" + url.PathEscape(account) + "/cfd_tunnel/" + url.PathEscape(tunnelID)
	if err := c.call(ctx, http.MethodDelete, tunnel+"/connections", nil, nil); err != nil && !isCloudflareNotFound(err) {
		return fmt.Errorf("disconnecting the tunnel: %w", err)
	}
	if err := c.call(ctx, http.MethodDelete, tunnel, nil, nil); err != nil && !isCloudflareNotFound(err) {
		return fmt.Errorf("deleting the tunnel: %w", err)
	}
	return nil
}
