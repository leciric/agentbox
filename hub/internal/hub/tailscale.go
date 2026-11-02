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
	"time"
)

// Tailscale puts environments in the hub's tailnet, as the second way their
// devices reach them (hubapi.TransportTailscale), instead of a Cloudflare
// Tunnel: the hub gives each environment an auth key, the environment joins
// the tailnet with it as a node of its own and serves there with tailscale
// serve, and only the tailnet's devices reach it. Nothing is exposed to the
// internet, and the hub sees none of the traffic.
//
// The hub gets its keys one of two ways:
//
//   - AuthKey: a reusable, pre-approved auth key, tagged with Tag, made in
//     the tailnet's admin console. Every environment gets the same one.
//   - ClientID and ClientSecret: an OAuth client with the auth_keys and
//     devices:core scopes, owning Tag. The hub makes each environment a
//     one-off key that lasts an hour, and removes an environment's node from
//     the tailnet when the environment is deleted.
type Tailscale struct {
	// Tailnet is the tailnet's name, as the admin console's Settings show it
	// ("example.com", "user@gmail.com"), or "-" for the one the OAuth client
	// or key belongs to. Environments are told it, for their devices.
	Tailnet string
	// Tag is the tag environments' nodes get, which the tailnet's policy
	// says who reaches: DefaultTailscaleTag when empty.
	Tag string

	AuthKey                string // never logged
	ClientID, ClientSecret string // the secret is never logged

	// API is Tailscale's API, https://api.tailscale.com when empty; tests
	// point it at a fake.
	API  string
	HTTP *http.Client

	mu      sync.Mutex
	token   string // the OAuth client's access token, while it lasts
	expires time.Time
}

// DefaultTailscaleTag is the tag environments' nodes get unless the hub says
// another.
const DefaultTailscaleTag = "tag:agentbox"

// tailscaleKeyLife is how long a key the hub makes lasts: long enough for an
// environment to join with it, which it does at once.
const tailscaleKeyLife = time.Hour

func (t *Tailscale) tag() string {
	if t.Tag == "" {
		return DefaultTailscaleTag
	}
	return t.Tag
}

func (t *Tailscale) tailnet() string {
	if t.Tailnet == "" {
		return "-"
	}
	return t.Tailnet
}

// oauth says the hub makes keys and removes nodes itself.
func (t *Tailscale) oauth() bool { return t.ClientID != "" && t.ClientSecret != "" }

// tailscaleError is Tailscale's answer when it refuses.
type tailscaleError struct {
	status  int
	message string
}

func (e tailscaleError) Error() string {
	return fmt.Sprintf("Tailscale answered %d: %s", e.status, e.message)
}

func (t *Tailscale) client() *http.Client {
	if t.HTTP != nil {
		return t.HTTP
	}
	return http.DefaultClient
}

func (t *Tailscale) base() string {
	if t.API != "" {
		return strings.TrimRight(t.API, "/")
	}
	return "https://api.tailscale.com"
}

// accessToken is the OAuth client's access token, asked for again shortly
// before it expires.
func (t *Tailscale) accessToken(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && time.Until(t.expires) > time.Minute {
		return t.token, nil
	}
	form := url.Values{"client_id": {t.ClientID}, "client_secret": {t.ClientSecret}, "grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base()+"/api/v2/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := t.do(req, &out); err != nil {
		return "", fmt.Errorf("signing in to Tailscale with the OAuth client: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("signing in to Tailscale with the OAuth client: no access token in its answer")
	}
	t.token, t.expires = out.AccessToken, time.Now().Add(time.Duration(out.ExpiresIn)*time.Second)
	return t.token, nil
}

// call sends one request to Tailscale's API with the OAuth client's token.
func (t *Tailscale) call(ctx context.Context, method, path string, body, out any) error {
	token, err := t.accessToken(ctx)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.base()+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return t.do(req, out)
}

func (t *Tailscale) do(req *http.Request, out any) error {
	resp, err := t.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Message
		if msg == "" {
			msg = e.Error
		}
		if msg == "" {
			msg = resp.Status
		}
		return tailscaleError{resp.StatusCode, msg}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// authKey is the key environment id joins the tailnet with: the hub's own, or
// a one-off one made for it, pre-approved and tagged.
func (t *Tailscale) authKey(ctx context.Context, id string) (string, error) {
	if !t.oauth() {
		return t.AuthKey, nil
	}
	body := map[string]any{
		"capabilities": map[string]any{"devices": map[string]any{"create": map[string]any{
			"reusable": false, "ephemeral": false, "preauthorized": true, "tags": []string{t.tag()},
		}}},
		"expirySeconds": int(tailscaleKeyLife / time.Second),
		"description":   "AgentBox " + strings.TrimPrefix(id, "env_"),
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := t.call(ctx, http.MethodPost, "/api/v2/tailnet/"+url.PathEscape(t.tailnet())+"/keys", body, &out); err != nil {
		return "", fmt.Errorf("making an auth key: %w", err)
	}
	if out.Key == "" {
		return "", errors.New("making an auth key: Tailscale's answer has no key")
	}
	return out.Key, nil
}

// removeNode takes an environment's node out of the tailnet. Without an OAuth
// client the hub can't, and leaves it to the tailnet's admin console.
func (t *Tailscale) removeNode(ctx context.Context, nodeID string) error {
	if !t.oauth() || nodeID == "" {
		return nil
	}
	err := t.call(ctx, http.MethodDelete, "/api/v2/device/"+url.PathEscape(nodeID), nil, nil)
	var te tailscaleError
	if errors.As(err, &te) && te.status == http.StatusNotFound {
		return nil // removed in the admin console already
	}
	return err
}
