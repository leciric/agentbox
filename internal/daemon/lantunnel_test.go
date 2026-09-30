package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// fakeCloudflared writes a cloudflared that says what the real one does when
// its tunnel is up, and records how it was run in dir.
func fakeCloudflared(t *testing.T) (dir string, find func(context.Context, func(string)) (string, error)) {
	dir = t.TempDir()
	bin := filepath.Join(dir, "cloudflared")
	script := `#!/bin/sh
d=$(dirname "$0")
echo "$@" >"$d/args"
printf '%s' "$TUNNEL_TOKEN" >"$d/token"
rm -f "$d/stopped"
echo "2026-09-29T10:00:00Z INF |  https://quiet-river-1234.trycloudflare.com  |" >&2
echo "2026-09-29T10:00:01Z INF Registered tunnel connection connIndex=0" >&2
trap 'echo stopped >"$d/stopped"; exit 0' TERM
while :; do sleep 0.05; done
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, func(_ context.Context, status func(string)) (string, error) {
		status("Downloading cloudflared, once for this machine")
		return bin, nil
	}
}

// tunnelRequest is a request as cloudflared brings it to the daemon: to the
// tunnel's listener, for the public hostname, from an address on the internet.
func tunnelRequest(t *testing.T, origin, method, path, body, from string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, origin+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "quiet-river-1234.trycloudflare.com"
	req.Header.Set("Cf-Connecting-IP", from)
	req.Header.Set("Origin", "https://quiet-river-1234.trycloudflare.com")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestPhoneThroughATunnel(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	dir, find := fakeCloudflared(t)
	d.srv.lan.mu.Lock()
	// As in AgentBox's VM on Linux: the daemon opens no port on the network
	// of its own, and the tunnel works all the same.
	d.srv.lan.own = false
	d.srv.lan.cloudflared = find
	d.srv.lan.tunnelPort = freePort(t)
	d.srv.lan.mu.Unlock()

	st, err := d.client.LAN(ctx)
	if err != nil || st.Tunnel.Enabled || st.Tunnel.State != "off" {
		t.Fatalf("LAN() at first = %+v, %v; want the tunnel off", st.Tunnel, err)
	}
	// The tunnel on, with chatting from a phone still off: nothing runs.
	yes := true
	if st, err = d.client.UpdateLAN(ctx, api.UpdateLANRequest{Tunnel: &yes}); err != nil || !st.Tunnel.Enabled || st.Tunnel.State != "off" {
		t.Fatalf("tunnel on, phones off = %+v, %v", st.Tunnel, err)
	}
	if _, err := d.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &yes}); err != nil {
		t.Fatal(err)
	}
	const url = "https://quiet-river-1234.trycloudflare.com/"
	waitFor(t, "the tunnel to run", func() bool {
		st, err = d.client.LAN(ctx)
		return err == nil && st.Tunnel.State == "running"
	})
	if st.Tunnel.URL != url || len(st.URLs) == 0 || st.URLs[0] != url || st.Tunnel.Named {
		t.Fatalf("LAN() with the tunnel running = %+v", st)
	}
	d.srv.lan.mu.Lock()
	origin := d.srv.lan.tunnel.origin
	d.srv.lan.mu.Unlock()
	if args, _ := os.ReadFile(filepath.Join(dir, "args")); strings.TrimSpace(string(args)) != "tunnel --no-autoupdate --url "+origin ||
		!strings.HasPrefix(origin, "http://127.0.0.1:") {
		t.Fatalf("cloudflared ran with %q, the listener is %s", args, origin)
	}

	// The QR code is the tunnel's address.
	pairing, err := d.client.PairLAN(ctx)
	if err != nil || !strings.HasPrefix(pairing.URLs[0], url+"#pair=") {
		t.Fatalf("PairLAN() = %+v, %v", pairing, err)
	}

	// Guessing pairing codes gets an address nowhere fast.
	for i := range 20 {
		if resp := tunnelRequest(t, origin, "POST", "/lan/pair", `{"secret":"guess","name":"x"}`, "203.0.113.9", nil); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("guess %d = %d", i, resp.StatusCode)
		}
	}
	if resp := tunnelRequest(t, origin, "POST", "/lan/pair", `{"secret":"`+secretOf(t, pairing)+`","name":"x"}`, "203.0.113.9", nil); resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("pairing after 20 guesses = %d", resp.StatusCode)
	}
	// The guesser's address is shut out, a real phone's isn't, and the code
	// the guesser was refused with still pairs.
	resp := tunnelRequest(t, origin, "POST", "/lan/pair", `{"secret":"`+secretOf(t, pairing)+`","name":"Pixel"}`, "198.51.100.7", nil)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("pairing = %d %s", resp.StatusCode, b)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == lanCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("the cookie over the tunnel = %+v", cookie)
	}
	if resp.Header.Get("Strict-Transport-Security") == "" {
		t.Error("no Strict-Transport-Security over https")
	}
	if resp := tunnelRequest(t, origin, "GET", "/api/v1/projects", "", "198.51.100.7", cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("a paired phone's GET /api/v1/projects = %d", resp.StatusCode)
	}
	if st, _ := d.client.LAN(ctx); len(st.Phones) != 1 || st.Phones[0].LastAddr != "198.51.100.7" {
		t.Fatalf("phones = %+v", st.Phones)
	}
	// Still a phone: the chat and nothing else.
	if resp := tunnelRequest(t, origin, "GET", "/api/v1/lan", "", "198.51.100.7", cookie); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a phone's GET /api/v1/lan = %d", resp.StatusCode)
	}
	// A page elsewhere can't post as the phone.
	req, _ := http.NewRequest("POST", origin+"/lan/unpair", nil)
	req.Host = "quiet-river-1234.trycloudflare.com"
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(cookie)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cross-site POST = %v, %v", resp, err)
	}
	// Guessing tokens is counted the same way.
	for range 20 {
		tunnelRequest(t, origin, "GET", "/api/v1/projects", "", "203.0.113.10", &http.Cookie{Name: lanCookie, Value: "guess"})
	}
	if resp := tunnelRequest(t, origin, "GET", "/api/v1/projects", "", "203.0.113.10", &http.Cookie{Name: lanCookie, Value: "guess"}); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("a token guessed the 21st time = %d", resp.StatusCode)
	}

	// A named tunnel: its token is checked, kept sealed, and handed to
	// cloudflared in its environment only.
	bad := "not-a-token"
	if _, err := d.client.UpdateLAN(ctx, api.UpdateLANRequest{TunnelToken: &bad, TunnelHostname: new("chat.example.com")}); err == nil {
		t.Error("a token that isn't one was taken")
	}
	raw, _ := json.Marshal(map[string]string{"a": "account", "t": "tunnel-id", "s": "c2VjcmV0"})
	token := base64.StdEncoding.EncodeToString(raw)
	pasted := "sudo cloudflared service install " + token
	if _, err := d.client.UpdateLAN(ctx, api.UpdateLANRequest{TunnelToken: &pasted}); err == nil {
		t.Error("a named tunnel without a hostname was taken")
	}
	st, err = d.client.UpdateLAN(ctx, api.UpdateLANRequest{TunnelToken: &pasted, TunnelHostname: new("https://Chat.Example.com/")})
	if err != nil || !st.Tunnel.Named || st.Tunnel.Hostname != "chat.example.com" {
		t.Fatalf("a named tunnel = %+v, %v", st.Tunnel, err)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), token) {
		t.Fatal("GET /v1/lan says the token")
	}
	if stored, _ := d.srv.store.Setting(ctx, "lan_tunnel_token"); stored == "" || strings.Contains(stored, token) {
		t.Fatalf("the token is stored as %q", stored)
	}
	waitFor(t, "the named tunnel to run", func() bool {
		st, err = d.client.LAN(ctx)
		return err == nil && st.Tunnel.URL == "https://chat.example.com/"
	})
	if got, _ := os.ReadFile(filepath.Join(dir, "token")); string(got) != token {
		t.Errorf("cloudflared's TUNNEL_TOKEN = %q", got)
	}
	if args, _ := os.ReadFile(filepath.Join(dir, "args")); strings.Contains(string(args), token) || strings.TrimSpace(string(args)) != "tunnel --no-autoupdate run" {
		t.Errorf("cloudflared's arguments = %q", args)
	}

	// Chatting from a phone off: cloudflared goes, and so does its listener.
	no := false
	if st, err = d.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &no}); err != nil || st.Tunnel.State != "off" || !st.Tunnel.Enabled {
		t.Fatalf("phones off = %+v, %v", st.Tunnel, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stopped")); err != nil {
		t.Error("cloudflared wasn't stopped")
	}
	if _, err := http.Get(origin + "/"); err == nil {
		t.Error("the tunnel's listener is still open")
	}
	// And back to a quick tunnel, forgetting the token.
	if st, err = d.client.UpdateLAN(ctx, api.UpdateLANRequest{TunnelToken: new("")}); err != nil || st.Tunnel.Named || st.Tunnel.Hostname != "" {
		t.Fatalf("back to quick = %+v, %v", st.Tunnel, err)
	}
	if stored, _ := d.srv.store.Setting(ctx, "lan_tunnel_token"); stored != "" {
		t.Error("the token is still stored")
	}
}
