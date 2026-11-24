package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// mcpGuess is a call through the tunnel with a token, from an address on the
// internet, as cloudflared brings it: its status.
func mcpGuess(t *testing.T, origin, token, from string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, origin+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cf-Connecting-IP", from)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestAppsWithoutAHub pairs apps with no hub: one on this computer, at the
// local port, and one through the tunnel phones use. Each needs its own
// token, by its own route; nothing but /mcp is reachable either way; the
// local port is open only while an app is paired for it; and the tunnel runs
// for apps with phones off, which it still turns away.
func TestAppsWithoutAHub(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	_, find := fakeCloudflared(t)
	d.srv.lan.mu.Lock()
	d.srv.lan.cloudflared = find
	d.srv.lan.resolve = func(context.Context, string) error { return nil }
	d.srv.lan.tunnelPort = freePort(t)
	d.srv.lan.mu.Unlock()
	port := freePort(t)
	local := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)

	list, err := d.client.UpdateMCP(ctx, api.UpdateMCPRequest{Port: &port})
	if err != nil || list.Reason != "no-hub" || list.Local.URL != local || list.Local.Listening || list.Tunnel.Enabled || list.Tunnel.State != "off" {
		t.Fatalf("with no hub and nothing paired: %+v, %v", list, err)
	}
	if _, err := http.Post(local, "application/json", nil); err == nil {
		t.Fatal("the local port is open with no app paired for it")
	}

	// On this computer, which is what pairing with no hub means.
	code, err := d.client.PairMCP(ctx, api.MCPPairRequest{Client: api.MCPClientClaudeCode, Access: api.MCPAccessRead})
	if err != nil {
		t.Fatal(err)
	}
	if p := code.Pairing; p.Route != api.MCPRouteLocal || p.Label != "Claude Code" || code.URL != local || code.Code != "" || !strings.HasPrefix(code.Token, "abx_m_") {
		t.Fatalf("a local pairing = %+v", code)
	}
	if list, _ := d.client.MCPPairings(ctx); !list.Local.Listening || list.Local.Error != "" || len(list.Pairings) != 1 || list.Pairings[0].Route != api.MCPRouteLocal {
		t.Fatalf("after pairing: %+v", list)
	}
	if status, _ := mcpCall(t, local, "", "tools/list", map[string]any{}); status != http.StatusUnauthorized {
		t.Errorf("no token got %d", status)
	}
	if status, _ := mcpCall(t, local, "abx_m_guess", "tools/list", map[string]any{}); status != http.StatusUnauthorized {
		t.Errorf("a wrong token got %d", status)
	}
	if text, failed := mcpTool(t, local, code.Token, "list_projects", nil); failed || !strings.Contains(text, `"project": "hello-stack"`) {
		t.Errorf("list_projects = %q, %v", text, failed)
	}
	if names := strings.Join(mcpToolNames(t, local, code.Token), " "); names != "list_projects list_agents list_pull_requests read_lead_chat" {
		t.Errorf("a read-only pairing's tools = %s", names)
	}
	// /mcp and nothing else: the daemon's API isn't there.
	if resp, err := http.Get(strings.TrimSuffix(local, "/mcp") + "/v1/projects"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /v1/projects on the local port = %v, %v", resp, err)
	}

	// The tunnel is for apps only once it's turned on for them.
	if _, err := d.client.PairMCP(ctx, api.MCPPairRequest{Route: api.MCPRouteTunnel, Access: api.MCPAccessRead}); err == nil {
		t.Error("paired an app through the tunnel with the tunnel off")
	}
	yes, no := true, false
	if _, err := d.client.UpdateMCP(ctx, api.UpdateMCPRequest{Tunnel: &yes}); err != nil {
		t.Fatal(err)
	}
	const tunnelURL = "https://quiet-river-1234.trycloudflare.com/mcp"
	waitFor(t, "the tunnel to run with phones off", func() bool {
		list, err = d.client.MCPPairings(ctx)
		return err == nil && list.Tunnel.State == "running"
	})
	if !list.Tunnel.Enabled || list.Tunnel.Mode != "quick" || list.Tunnel.URL != tunnelURL {
		t.Fatalf("the tunnel for apps = %+v", list.Tunnel)
	}
	if st, err := d.client.LAN(ctx); err != nil || st.Tunnel.State != "off" || st.Tunnel.URL != "" {
		t.Errorf("phones' tunnel, with phones off = %+v, %v", st, err)
	}
	remote, err := d.client.PairMCP(ctx, api.MCPPairRequest{Route: api.MCPRouteTunnel, Client: api.MCPClientCursor, Access: api.MCPAccessChat})
	if err != nil || remote.URL != tunnelURL || remote.Pairing.Route != api.MCPRouteTunnel || remote.Token == "" {
		t.Fatalf("a tunnel pairing = %+v, %v", remote, err)
	}
	d.srv.lan.mu.Lock()
	origin := d.srv.lan.tunnel.origin
	d.srv.lan.mu.Unlock()
	if text, failed := mcpTool(t, origin+"/mcp", remote.Token, "list_projects", nil); failed || !strings.Contains(text, "hello-stack") {
		t.Errorf("list_projects through the tunnel = %q, %v", text, failed)
	}
	// Each token by its own route only.
	if status, _ := mcpCall(t, origin+"/mcp", code.Token, "tools/list", map[string]any{}); status != http.StatusUnauthorized {
		t.Errorf("a local token through the tunnel got %d", status)
	}
	if status, _ := mcpCall(t, local, remote.Token, "tools/list", map[string]any{}); status != http.StatusUnauthorized {
		t.Errorf("a tunnel token at the local port got %d", status)
	}
	// Phones are off: the tunnel has nothing else for anybody.
	for _, path := range []string{"/", "/api/v1/projects", "/lan/session", "/.well-known/oauth-authorization-server"} {
		resp, err := http.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s through the tunnel, phones off = %d", path, resp.StatusCode)
		}
	}
	// Guessing tokens through the tunnel gets an address nowhere fast.
	for i := range 20 {
		if status := mcpGuess(t, origin, fmt.Sprintf("abx_m_guess%d", i), "203.0.113.9"); status != http.StatusUnauthorized {
			t.Fatalf("guess %d = %d", i, status)
		}
	}
	if status := mcpGuess(t, origin, remote.Token, "203.0.113.9"); status != http.StatusTooManyRequests {
		t.Errorf("the right token after 20 guesses = %d", status)
	}
	if status := mcpGuess(t, origin, remote.Token, "198.51.100.7"); status != http.StatusOK {
		t.Errorf("the right token from elsewhere = %d", status)
	}

	// A new token ends the old one.
	again, err := d.client.RenewMCPCode(ctx, code.Pairing.ID)
	if err != nil || again.Token == "" || again.Token == code.Token || again.URL != local || again.Code != "" {
		t.Fatalf("a new token = %+v, %v", again, err)
	}
	if status, _ := mcpCall(t, local, code.Token, "tools/list", map[string]any{}); status != http.StatusUnauthorized {
		t.Errorf("the old token got %d", status)
	}
	if _, failed := mcpTool(t, local, again.Token, "list_projects", nil); failed {
		t.Error("the new token doesn't work")
	}

	// Revoking the last local app closes the port; the tunnel's goes on.
	if err := d.client.RevokeMCPPairing(ctx, code.Pairing.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := http.Post(local, "application/json", nil); err == nil {
		t.Error("the local port is open with no app paired for it")
	}
	if list, _ := d.client.MCPPairings(ctx); list.Local.Listening || len(list.Pairings) != 1 || list.Pairings[0].ID != remote.Pairing.ID {
		t.Errorf("after revoking the local app: %+v", list)
	}
	// Turned off for apps, the tunnel turns them away, and stops.
	if _, err := d.client.UpdateMCP(ctx, api.UpdateMCPRequest{Tunnel: &no}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the tunnel to stop", func() bool {
		list, err = d.client.MCPPairings(ctx)
		return err == nil && list.Tunnel.State == "off" && list.Tunnel.URL == ""
	})
	if _, err := http.Post(origin+"/mcp", "application/json", nil); err == nil {
		t.Error("the tunnel's listener is still open")
	}
}
