package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/hubapi"
)

// fakeTailscale is the part of Tailscale's API the hub uses with an OAuth
// client: its token, making auth keys, and removing devices.
type fakeTailscale struct {
	mu      sync.Mutex
	n       int
	keys    []map[string]any // what each key was made with
	devices map[string]bool  // the tailnet's nodes, by ID
	removed []string
	signIns int
}

const (
	fakeTSClient = "k123CNTRL"
	fakeTSSecret = "tskey-client-k123CNTRL-secret"
)

func newFakeTailscale(t *testing.T) (*fakeTailscale, *Tailscale) {
	f := &fakeTailscale{devices: map[string]bool{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &Tailscale{Tailnet: "example.com", ClientID: fakeTSClient, ClientSecret: fakeTSSecret, API: srv.URL}
}

func (f *fakeTailscale) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/api/v2/oauth/token" {
		if r.FormValue("client_id") != fakeTSClient || r.FormValue("client_secret") != fakeTSSecret || r.FormValue("grant_type") != "client_credentials" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "invalid client"})
			return
		}
		f.signIns++
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-1", "expires_in": 3600, "token_type": "Bearer"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer access-1" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "API token invalid"})
		return
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/api/v2/tailnet/example.com/keys":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.keys = append(f.keys, body)
		f.n++
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("k%d", f.n), "key": fmt.Sprintf("tskey-auth-k%d-secret", f.n)})
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/v2/device/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/v2/device/")
		if !f.devices[id] {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "device not found"})
			return
		}
		delete(f.devices, id)
		f.removed = append(f.removed, id)
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "no route"})
	}
}

func TestEnvironmentJoinsTheTailnet(t *testing.T) {
	h := newTestHub(t)
	ts, client := newFakeTailscale(t)
	h.server.Tailscale = client
	owner := h.signIn("owner@example.com")
	env := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: "vps"}), 201)

	// Only an environment asks, with its own token.
	decode[hubapi.Error](t, h.do("POST", hubapi.PathEnvironmentTunnel, owner, nil), http.StatusUnauthorized)
	got := decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), 200)
	node := "agentbox-vps-" + strings.TrimPrefix(env.Environment.ID, "env_")[:6]
	if got.Transport != hubapi.TransportTailscale || got.Hostname != node || got.Token != "tskey-auth-k1-secret" || got.Tailnet != "example.com" {
		t.Fatalf("the answer = %+v", got)
	}
	ts.mu.Lock()
	made, _ := json.Marshal(ts.keys[0]["capabilities"])
	life := ts.keys[0]["expirySeconds"]
	ts.mu.Unlock()
	if want := `{"devices":{"create":{"ephemeral":false,"preauthorized":true,"reusable":false,"tags":["tag:agentbox"]}}}`; string(made) != want || life != float64(3600) {
		t.Fatalf("the key was made with %s, for %v s", made, life)
	}
	// Asked again, a new key for the same node; the hub signed in once.
	again := decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), 200)
	if again.Hostname != node || again.Token != "tskey-auth-k2-secret" || ts.signIns != 1 {
		t.Fatalf("asked again = %+v, signed in %d times", again, ts.signIns)
	}

	// Before it reports, it's listed without an address; after, with it.
	list := decode[[]hubapi.HubEnvironment](t, h.do("GET", hubapi.PathEnvironments, owner, nil), 200)
	if len(list) != 1 || list[0].Address != "" {
		t.Fatalf("listed before reporting: %+v", list)
	}
	addr := hubapi.HubEnvironmentAddress{Transport: hubapi.TransportTailscale, URL: "https://" + node + ".tail1234.ts.net/", NodeID: "nNode1CNTRL", IPs: []string{"100.101.102.103"}}
	decode[hubapi.Error](t, h.do("POST", hubapi.PathEnvironmentAddress, owner, addr), http.StatusUnauthorized)
	for _, bad := range []hubapi.HubEnvironmentAddress{
		{Transport: hubapi.TransportCloudflare, URL: addr.URL},
		{Transport: hubapi.TransportTailscale, URL: "http://" + node + ".tail1234.ts.net/"},
		{Transport: hubapi.TransportTailscale, URL: "https://" + node + ".tail1234.ts.net/elsewhere"},
		{Transport: hubapi.TransportTailscale, URL: "https://user@" + node + ".tail1234.ts.net/"},
		{Transport: hubapi.TransportTailscale, URL: addr.URL, NodeID: "n/../../x"},
	} {
		decode[hubapi.Error](t, h.do("POST", hubapi.PathEnvironmentAddress, env.Token, bad), http.StatusBadRequest)
	}
	resp := h.do("POST", hubapi.PathEnvironmentAddress, env.Token, addr)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reporting the address: %s", resp.Status)
	}
	list = decode[[]hubapi.HubEnvironment](t, h.do("GET", hubapi.PathEnvironments, owner, nil), 200)
	if list[0].Transport != hubapi.TransportTailscale || list[0].Address != addr.URL {
		t.Fatalf("listed after reporting: %+v", list[0])
	}

	// Deleting the environment takes its node out of the tailnet.
	ts.mu.Lock()
	ts.devices["nNode1CNTRL"] = true
	ts.mu.Unlock()
	resp = h.do("DELETE", "/v1/environments/"+env.Environment.ID, owner, nil)
	_ = resp.Body.Close()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		ts.mu.Lock()
		removed := len(ts.removed)
		ts.mu.Unlock()
		if removed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the deleted environment's node is still in the tailnet")
		}
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := h.store.EnvironmentTunnel(context.Background(), env.Environment.ID); err == ErrNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the deleted environment's node is still in the store")
		}
	}
}

func TestTailnetWithAnAuthKey(t *testing.T) {
	h := newTestHub(t)
	h.server.Tailscale = &Tailscale{AuthKey: "tskey-auth-kShared-secret", API: "http://127.0.0.1:1"}
	owner := h.signIn("owner@example.com")
	env := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: "pc"}), 201)
	got := decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), 200)
	if got.Transport != hubapi.TransportTailscale || got.Token != "tskey-auth-kShared-secret" || got.Tailnet != "-" {
		t.Fatalf("the answer = %+v", got)
	}
	// Without an OAuth client the hub can't remove nodes: cleaning only
	// forgets them, without calling Tailscale.
	if err := h.server.CleanTunnels(context.Background(), time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.EnvironmentTunnel(context.Background(), env.Environment.ID); err != ErrNotFound {
		t.Fatalf("an idle node in the store: %v", err)
	}
}

func TestHubMovesFromCloudflareToTailscale(t *testing.T) {
	h := newTestHub(t)
	cf, cfClient := newFakeCloudflare(t)
	h.server.Cloudflare = cfClient
	owner := h.signIn("owner@example.com")
	env := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: "vps"}), 201)
	old := decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), 200)
	if old.Transport != "" {
		t.Fatalf("a Cloudflare answer says transport %q: daemons from before Tailscale read it", old.Transport)
	}
	list := decode[[]hubapi.HubEnvironment](t, h.do("GET", hubapi.PathEnvironments, owner, nil), 200)
	if list[0].Transport != hubapi.TransportCloudflare || list[0].Address != "https://"+old.Hostname+"/" {
		t.Fatalf("listed on Cloudflare: %+v", list[0])
	}
	// On Tailscale now, keeping the zone: its tunnel goes, and a node replaces it.
	_, h.server.Tailscale = newFakeTailscale(t)
	got := decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), 200)
	if got.Transport != hubapi.TransportTailscale {
		t.Fatalf("the answer = %+v", got)
	}
	if tunnels, records, _ := cf.state(); tunnels != 0 || records != 0 {
		t.Fatalf("Cloudflare still has %d tunnels and %d records", tunnels, records)
	}
	// Back on Cloudflare: a tunnel again.
	h.server.Tailscale = nil
	back := decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), 200)
	if back.Transport != "" || back.Hostname != old.Hostname {
		t.Fatalf("back on Cloudflare = %+v", back)
	}
	// A hub on Cloudflare takes no tailnet addresses.
	decode[hubapi.Error](t, h.do("POST", hubapi.PathEnvironmentAddress, env.Token, hubapi.HubEnvironmentAddress{Transport: hubapi.TransportTailscale, URL: "https://x.tail1234.ts.net/"}), http.StatusConflict)
}

func TestTailnetAddress(t *testing.T) {
	for in, want := range map[string]string{
		"https://agentbox-vps-3fa9c0.tail1234.ts.net/":     "https://agentbox-vps-3fa9c0.tail1234.ts.net/",
		"https://Agentbox-VPS-3fa9c0.tail1234.ts.net":      "https://agentbox-vps-3fa9c0.tail1234.ts.net/",
		"https://agentbox-vps-3fa9c0.tail1234.ts.net:8443": "https://agentbox-vps-3fa9c0.tail1234.ts.net:8443/",
	} {
		if got, err := tailnetAddress(in); err != nil || got != want {
			t.Errorf("tailnetAddress(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "agentbox.ts.net", "https://100.101.102.103/", "https://x.ts.net/?a=b", "https://x.ts.net/#f", "ftp://x.ts.net/"} {
		if _, err := tailnetAddress(bad); err == nil {
			t.Errorf("tailnetAddress(%q) took it", bad)
		}
	}
}
