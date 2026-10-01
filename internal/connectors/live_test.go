package connectors

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

// TestLiveServers runs discovery and registration against the real Notion and
// Figma servers, as far as a sign-in can go without a person: Notion's
// authorization page must take the request (it redirects to its sign-in, not
// back with an error), and Figma must refuse to register AgentBox, which is
// what the package doc says it does. It reaches the internet and registers
// a client with Notion, so it only runs when asked:
//
//	AGENTBOX_LIVE_CONNECTORS=1 go test ./internal/connectors -run Live -v
func TestLiveServers(t *testing.T) {
	if os.Getenv("AGENTBOX_LIVE_CONNECTORS") == "" {
		t.Skip("reaches mcp.notion.com and mcp.figma.com: set AGENTBOX_LIVE_CONNECTORS=1")
	}
	ctx := context.Background()
	o := OAuth{ClientName: "AgentBox"}

	notion, err := o.Discover(ctx, "https://mcp.notion.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Notion: issuer %s, resource %s, scope %q, registration %s", notion.Server.Issuer, notion.Resource, notion.Scope, notion.Server.RegistrationEndpoint)
	redirect := "http://127.0.0.1:53682/callback"
	client, err := o.Register(ctx, notion.Server, redirect, notion.Scope)
	if err != nil {
		t.Fatalf("Notion refused to register AgentBox: %v", err)
	}
	t.Logf("Notion registered a %s client", client.AuthMethod)
	_, challenge := PKCE()
	authURL := AuthorizationURL(notion, client, redirect, "live-test", challenge)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	where := resp.Header.Get("Location")
	t.Logf("Notion's authorization page: HTTP %d, to %.120s", resp.StatusCode, where)
	if strings.HasPrefix(where, redirect) {
		u, _ := url.Parse(where)
		t.Fatalf("Notion sent the request straight back: %s", u.Query().Get("error_description"))
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("Notion's authorization page answered HTTP %d", resp.StatusCode)
	}

	figma, err := o.Discover(ctx, "https://mcp.figma.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Figma: issuer %s, scope %q, registration %s", figma.Server.Issuer, figma.Scope, figma.Server.RegistrationEndpoint)
	if _, err := o.Register(ctx, figma.Server, redirect, figma.Scope); !errors.Is(err, ErrRegistration) {
		t.Errorf("Figma's registration: %v, want it refused", err)
	} else {
		t.Logf("Figma: %v", err)
	}
}
