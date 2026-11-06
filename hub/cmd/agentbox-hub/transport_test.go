package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearHubEnv unsets every variable transportConfig reads, so the machine's
// own don't change what a test sees.
func clearHubEnv(t *testing.T) {
	for _, name := range []string{
		"AGENTBOX_HUB_TRANSPORT", "AGENTBOX_HUB_CLOUDFLARE_ZONE", "AGENTBOX_HUB_CLOUDFLARE_TOKEN", "AGENTBOX_HUB_CLOUDFLARE_TOKEN_FILE",
		"AGENTBOX_HUB_TAILSCALE_TAILNET", "AGENTBOX_HUB_TAILSCALE_TAG", "AGENTBOX_HUB_TAILSCALE_OAUTH_CLIENT",
		"AGENTBOX_HUB_TAILSCALE_AUTHKEY", "AGENTBOX_HUB_TAILSCALE_AUTHKEY_FILE",
		"AGENTBOX_HUB_TAILSCALE_OAUTH_SECRET", "AGENTBOX_HUB_TAILSCALE_OAUTH_SECRET_FILE",
	} {
		t.Setenv(name, "")
	}
}

func TestTransportDefaultsToCloudflare(t *testing.T) {
	clearHubEnv(t)
	cf, ts, err := transportConfig(tailscaleFlags{}, "")
	if err != nil || cf != nil || ts != nil {
		t.Fatalf("with nothing set: %v, %v, %v", cf, ts, err)
	}
	t.Setenv("AGENTBOX_HUB_CLOUDFLARE_TOKEN", "cf-token")
	cf, ts, err = transportConfig(tailscaleFlags{}, "zone-1")
	if err != nil || cf == nil || cf.ZoneID != "zone-1" || ts != nil {
		t.Fatalf("with a zone: %v, %v, %v", cf, ts, err)
	}
	if _, _, err := transportConfig(tailscaleFlags{transport: "pigeon"}, ""); err == nil || !strings.Contains(err.Error(), "cloudflare or tailscale") {
		t.Fatalf("an unknown transport: %v", err)
	}
}

func TestTransportTailscale(t *testing.T) {
	clearHubEnv(t)
	if _, _, err := transportConfig(tailscaleFlags{transport: "tailscale"}, ""); err == nil || !strings.Contains(err.Error(), "AGENTBOX_HUB_TAILSCALE_AUTHKEY") {
		t.Fatalf("with no key: %v", err)
	}

	// An auth key, from the environment; the transport from it too.
	t.Setenv("AGENTBOX_HUB_TRANSPORT", "tailscale")
	t.Setenv("AGENTBOX_HUB_TAILSCALE_AUTHKEY", "tskey-auth-kShared-secret")
	t.Setenv("AGENTBOX_HUB_TAILSCALE_TAILNET", "example.com")
	cf, ts, err := transportConfig(tailscaleFlags{}, "")
	if err != nil || cf != nil || ts == nil || ts.AuthKey != "tskey-auth-kShared-secret" || ts.Tailnet != "example.com" || ts.ClientID != "" {
		t.Fatalf("with an auth key: %v, %+v, %v", cf, ts, err)
	}
	// Flags win over the environment.
	if _, ts, err = transportConfig(tailscaleFlags{tailnet: "other.org", tag: "tag:machines"}, ""); err != nil || ts.Tailnet != "other.org" || ts.Tag != "tag:machines" {
		t.Fatalf("with flags: %+v, %v", ts, err)
	}
	if _, _, err = transportConfig(tailscaleFlags{tag: "machines"}, ""); err == nil {
		t.Fatal("a tag without tag: was taken")
	}
	// Keeping the Cloudflare zone, to remove its tunnels.
	t.Setenv("AGENTBOX_HUB_CLOUDFLARE_TOKEN", "cf-token")
	if cf, ts, err = transportConfig(tailscaleFlags{}, "zone-1"); err != nil || cf == nil || ts == nil {
		t.Fatalf("on Tailscale with a zone: %v, %v, %v", cf, ts, err)
	}
	t.Setenv("AGENTBOX_HUB_CLOUDFLARE_TOKEN", "")
	t.Setenv("AGENTBOX_HUB_TAILSCALE_AUTHKEY", "not-a-key")
	if _, _, err = transportConfig(tailscaleFlags{}, ""); err == nil || strings.Contains(err.Error(), "not-a-key") {
		t.Fatalf("a key that isn't one: %v", err)
	}

	// An OAuth client, its secret from a file; not with a key as well.
	t.Setenv("AGENTBOX_HUB_TAILSCALE_AUTHKEY", "")
	file := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(file, []byte("tskey-client-k1-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = transportConfig(tailscaleFlags{clientID: "k1CNTRL"}, ""); err == nil || !strings.Contains(err.Error(), "not its secret") {
		t.Fatalf("a client with no secret: %v", err)
	}
	t.Setenv("AGENTBOX_HUB_TAILSCALE_OAUTH_SECRET_FILE", file)
	if _, ts, err = transportConfig(tailscaleFlags{clientID: "k1CNTRL"}, ""); err != nil || ts.ClientID != "k1CNTRL" || ts.ClientSecret != "tskey-client-k1-secret" {
		t.Fatalf("with an OAuth client: %+v, %v", ts, err)
	}
	if _, _, err = transportConfig(tailscaleFlags{}, ""); err == nil || !strings.Contains(err.Error(), "not its ID") {
		t.Fatalf("a secret with no client: %v", err)
	}
	t.Setenv("AGENTBOX_HUB_TAILSCALE_AUTHKEY", "tskey-auth-kShared-secret")
	if _, _, err = transportConfig(tailscaleFlags{clientID: "k1CNTRL"}, ""); err == nil || !strings.Contains(err.Error(), "one of them") {
		t.Fatalf("a key and a client: %v", err)
	}
}
