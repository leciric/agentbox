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

// fakeCloudflare is the part of Cloudflare's API the hub uses, for one zone
// in one account, which only answers its own API token.
type fakeCloudflare struct {
	mu      sync.Mutex
	n       int
	tunnels map[string]string         // id → name
	ingress map[string][]any          // tunnel id → its ingress rules
	records map[string]map[string]any // id → the record
	calls   []string
}

const (
	fakeZoneID  = "zone-1"
	fakeZone    = "agentbox.example"
	fakeAccount = "account-1"
	fakeCFToken = "cf-api-token"
)

func newFakeCloudflare(t *testing.T) (*fakeCloudflare, *Cloudflare) {
	f := &fakeCloudflare{tunnels: map[string]string{}, ingress: map[string][]any{}, records: map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &Cloudflare{Token: fakeCFToken, ZoneID: fakeZoneID, API: srv.URL}
}

func (f *fakeCloudflare) id(prefix string) string {
	f.n++
	return fmt.Sprintf("%s-%d", prefix, f.n)
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	ok := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	}
	fail := func(status, code int, msg string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": code, "message": msg}}, "result": nil})
	}
	if r.Header.Get("Authorization") != "Bearer "+fakeCFToken {
		fail(http.StatusForbidden, 10000, "Authentication error")
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	zone, account := "/zones/"+fakeZoneID, "/accounts/"+fakeAccount+"/cfd_tunnel"
	path := r.URL.Path
	switch {
	case r.Method == "GET" && path == zone:
		ok(map[string]any{"id": fakeZoneID, "name": fakeZone, "account": map[string]any{"id": fakeAccount}})
	case r.Method == "GET" && path == account:
		found := []any{}
		for id, name := range f.tunnels {
			if name == r.URL.Query().Get("name") {
				found = append(found, map[string]any{"id": id, "name": name})
			}
		}
		ok(found)
	case r.Method == "POST" && path == account:
		if body["config_src"] != "cloudflare" {
			fail(400, 1001, "only remotely-managed tunnels here")
			return
		}
		id := f.id("tunnel")
		f.tunnels[id] = body["name"].(string)
		ok(map[string]any{"id": id, "name": body["name"]})
	case strings.HasPrefix(path, account+"/"):
		id, rest, _ := strings.Cut(strings.TrimPrefix(path, account+"/"), "/")
		if _, exists := f.tunnels[id]; !exists {
			fail(404, 1003, "Tunnel not found")
			return
		}
		switch {
		case r.Method == "GET" && rest == "token":
			ok("token-for-" + id)
		case r.Method == "PUT" && rest == "configurations":
			f.ingress[id] = body["config"].(map[string]any)["ingress"].([]any)
			ok(map[string]any{})
		case r.Method == "DELETE" && rest == "connections":
			ok(nil)
		case r.Method == "DELETE" && rest == "":
			delete(f.tunnels, id)
			delete(f.ingress, id)
			ok(map[string]any{"id": id})
		default:
			fail(404, 7003, "No route")
		}
	case r.Method == "GET" && path == zone+"/dns_records":
		found := []any{}
		for id, rec := range f.records {
			if rec["name"] == r.URL.Query().Get("name") && rec["type"] == r.URL.Query().Get("type") {
				found = append(found, map[string]any{"id": id})
			}
		}
		ok(found)
	case r.Method == "POST" && path == zone+"/dns_records":
		id := f.id("record")
		f.records[id] = body
		ok(map[string]any{"id": id})
	case strings.HasPrefix(path, zone+"/dns_records/"):
		id := strings.TrimPrefix(path, zone+"/dns_records/")
		if _, exists := f.records[id]; !exists {
			fail(404, 81044, "Record does not exist")
			return
		}
		switch r.Method {
		case "PATCH":
			f.records[id] = body
			ok(map[string]any{"id": id})
		case "DELETE":
			delete(f.records, id)
			ok(map[string]any{"id": id})
		}
	default:
		fail(404, 7003, "No route")
	}
}

// state is how many tunnels and DNS records there are, and the one record.
func (f *fakeCloudflare) state() (tunnels, records int, record map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.records {
		record = r
	}
	return len(f.tunnels), len(f.records), record
}

func TestEnvironmentTunnel(t *testing.T) {
	h := newTestHub(t)
	owner := h.signIn("owner@example.com")
	env := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: "vps"}), 201)

	// A hub without a zone gives out no addresses, which AgentBox reads as
	// a quick tunnel instead.
	decode[hubapi.Error](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), http.StatusNotImplemented)

	cf, client := newFakeCloudflare(t)
	h.server.Cloudflare = client
	// Only an environment asks, with its own token.
	decode[hubapi.Error](t, h.do("POST", hubapi.PathEnvironmentTunnel, owner, nil), http.StatusUnauthorized)
	decode[hubapi.Error](t, h.do("POST", hubapi.PathEnvironmentTunnel, environmentPrefix+"guess", nil), http.StatusUnauthorized)

	got := decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), 200)
	hostname := strings.TrimPrefix(env.Environment.ID, "env_") + "." + fakeZone
	if got.Hostname != hostname || got.Token != "token-for-tunnel-1" {
		t.Fatalf("the tunnel = %+v, want %s and tunnel-1's token", got, hostname)
	}
	tunnels, records, record := cf.state()
	if tunnels != 1 || records != 1 || record["type"] != "CNAME" || record["name"] != hostname ||
		record["content"] != "tunnel-1.cfargotunnel.com" || record["proxied"] != true {
		t.Fatalf("Cloudflare has %d tunnels and %d records: %v", tunnels, records, record)
	}
	cf.mu.Lock()
	ingress, _ := json.Marshal(cf.ingress["tunnel-1"])
	cf.mu.Unlock()
	if want := fmt.Sprintf(`[{"hostname":%q,"service":%q},{"service":"http_status:404"}]`, hostname, hubapi.EnvironmentTunnelOrigin); string(ingress) != want {
		t.Fatalf("the tunnel's ingress = %s, want %s", ingress, want)
	}

	// Asked again: the same address, and nothing new in Cloudflare.
	again := decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), 200)
	if again != got {
		t.Fatalf("asked again = %+v, want %+v", again, got)
	}
	if tunnels, records, _ := cf.state(); tunnels != 1 || records != 1 {
		t.Fatalf("asked again, Cloudflare has %d tunnels and %d records", tunnels, records)
	}

	// Deleted in Cloudflare's dashboard: made again on the same hostname,
	// whose record now points at the new tunnel.
	cf.mu.Lock()
	delete(cf.tunnels, "tunnel-1")
	cf.mu.Unlock()
	remade := decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), 200)
	if tunnels, records, record := cf.state(); remade.Hostname != hostname || remade.Token == got.Token || tunnels != 1 || records != 1 ||
		!strings.HasSuffix(record["content"].(string), ".cfargotunnel.com") || record["content"] == "tunnel-1.cfargotunnel.com" {
		t.Fatalf("made again = %+v; Cloudflare has %d tunnels and %d records: %v", remade, tunnels, records, record)
	}

	// Cloudflare refusing is the hub's failure, which the environment tries
	// again after.
	client.Token = "revoked"
	cf.mu.Lock()
	clear(cf.tunnels)
	cf.mu.Unlock()
	decode[hubapi.Error](t, h.do("POST", hubapi.PathEnvironmentTunnel, env.Token, nil), http.StatusBadGateway)
	client.Token = fakeCFToken
}

func TestCleanTunnels(t *testing.T) {
	h := newTestHub(t)
	cf, client := newFakeCloudflare(t)
	h.server.Cloudflare = client
	h.server.TunnelIdle = 24 * time.Hour
	owner := h.signIn("owner@example.com")
	ctx := context.Background()
	idle := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: "idle"}), 201)
	busy := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: "busy"}), 201)
	gone := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: "gone"}), 201)
	for _, e := range []hubapi.HubEnvironmentToken{idle, busy, gone} {
		decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, e.Token, nil), 200)
	}
	if tunnels, records, _ := cf.state(); tunnels != 3 || records != 3 {
		t.Fatalf("Cloudflare has %d tunnels and %d records, want 3 of each", tunnels, records)
	}

	// Deleting an environment takes its address with it.
	resp := h.do("DELETE", "/v1/environments/"+gone.Environment.ID, owner, nil)
	_ = resp.Body.Close()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if tunnels, records, _ := cf.state(); tunnels == 2 && records == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the deleted environment's tunnel is still in Cloudflare")
		}
	}
	if _, err := h.store.EnvironmentTunnel(ctx, gone.Environment.ID); err != ErrNotFound {
		t.Fatalf("the deleted environment's tunnel in the store: %v", err)
	}

	// Two days on, the environment that came back keeps its tunnel, and the
	// one that didn't loses it.
	later := time.Now().Add(48 * time.Hour)
	if err := h.store.SeenEnvironment(ctx, busy.Environment.ID, later.Add(-time.Hour), "0.2.0", "vps"); err != nil {
		t.Fatal(err)
	}
	if err := h.server.CleanTunnels(ctx, later); err != nil {
		t.Fatal(err)
	}
	if tunnels, records, _ := cf.state(); tunnels != 1 || records != 1 {
		t.Fatalf("after cleaning, Cloudflare has %d tunnels and %d records, want 1 of each", tunnels, records)
	}
	if _, err := h.store.EnvironmentTunnel(ctx, idle.Environment.ID); err != ErrNotFound {
		t.Fatalf("the idle environment's tunnel in the store: %v", err)
	}
	if _, err := h.store.EnvironmentTunnel(ctx, busy.Environment.ID); err != nil {
		t.Fatalf("the busy environment's tunnel: %v", err)
	}
	// The idle one asking again gets its hostname back.
	back := decode[hubapi.HubEnvironmentTunnel](t, h.do("POST", hubapi.PathEnvironmentTunnel, idle.Token, nil), 200)
	if back.Hostname != strings.TrimPrefix(idle.Environment.ID, "env_")+"."+fakeZone {
		t.Fatalf("back = %+v", back)
	}
}
