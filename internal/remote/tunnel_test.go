package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/hubapi"
)

func TestRequestTunnelTransports(t *testing.T) {
	var answer hubapi.HubEnvironmentTunnel
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(answer)
	}))
	defer srv.Close()
	ctx := context.Background()

	// A hub from before Tailscale says no transport: it's Cloudflare.
	answer = hubapi.HubEnvironmentTunnel{Hostname: "5f3a.agentbox.example", Token: "cf-token"}
	if got, err := RequestTunnel(ctx, nil, srv.URL, "abx_e_x"); err != nil || got.Transport != hubapi.TransportCloudflare {
		t.Fatalf("no transport: %+v, %v", got, err)
	}
	answer = hubapi.HubEnvironmentTunnel{Transport: hubapi.TransportTailscale, Hostname: "agentbox-5f3a", Token: "tskey-auth-x", Tailnet: "example.com"}
	if got, err := RequestTunnel(ctx, nil, srv.URL, "abx_e_x"); err != nil || got != answer {
		t.Fatalf("tailscale: %+v, %v", got, err)
	}
	answer.Transport = "carrier-pigeon"
	if _, err := RequestTunnel(ctx, nil, srv.URL, "abx_e_x"); err == nil || !strings.Contains(err.Error(), "update it") {
		t.Fatalf("an unknown transport: %v", err)
	}
	answer = hubapi.HubEnvironmentTunnel{Transport: hubapi.TransportTailscale, Hostname: "agentbox-5f3a"}
	if _, err := RequestTunnel(ctx, nil, srv.URL, "abx_e_x"); err == nil {
		t.Fatal("a Tailscale answer with no auth key was taken")
	}
}

func TestReportAddress(t *testing.T) {
	var got hubapi.HubEnvironmentAddress
	var auth string
	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != hubapi.PathEnvironmentAddress {
			http.NotFound(w, r)
			return
		}
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		if status != http.StatusNoContent {
			_ = json.NewEncoder(w).Encode(hubapi.Error{Error: "this hub isn't on Tailscale"})
		}
	}))
	defer srv.Close()
	addr := hubapi.HubEnvironmentAddress{Transport: hubapi.TransportTailscale, URL: "https://agentbox-5f3a.tail1234.ts.net/", NodeID: "n1"}
	if err := ReportAddress(context.Background(), nil, srv.URL+"/", "abx_e_x", addr); err != nil {
		t.Fatal(err)
	}
	if got.URL != addr.URL || got.NodeID != "n1" || auth != "Bearer abx_e_x" {
		t.Fatalf("the hub got %+v with %q", got, auth)
	}
	status = http.StatusConflict
	if err := ReportAddress(context.Background(), nil, srv.URL, "abx_e_x", addr); err == nil || !strings.Contains(err.Error(), "isn't on Tailscale") {
		t.Fatalf("a refusal: %v", err)
	}
}
