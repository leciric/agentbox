//go:build tailscale

package tunnel

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTailscaleForReal joins a real tailnet with the pinned tailscale,
// downloaded and checked as the daemon does, and serves an origin on it:
//
//	AGENTBOX_TS_AUTHKEY=… [AGENTBOX_TS_LOGIN_SERVER=http://127.0.0.1:8085] go test -tags tailscale -run ForReal -v ./internal/tunnel
//
// With a login server (Headscale, say), which gives out no certificates, it
// stops at what serve answers, and checks the node joined.
func TestTailscaleForReal(t *testing.T) {
	key := os.Getenv("AGENTBOX_TS_AUTHKEY")
	if key == "" {
		t.Skip("AGENTBOX_TS_AUTHKEY isn't set")
	}
	origin := http.Server{Addr: "127.0.0.1:17781", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "agentbox over the tailnet, for "+r.Header.Get("X-Forwarded-For"))
	})}
	go func() { _ = origin.ListenAndServe() }()
	defer origin.Close()

	tools := os.Getenv("AGENTBOX_TS_TOOLS")
	if tools == "" {
		tools = t.TempDir()
	}
	up := make(chan TailscaleNode, 1)
	ts := StartTailscale(context.Background(), TailscaleConfig{
		Binaries: TailscaleInstaller{Dir: tools}.Ensure,
		Origin:   "http://127.0.0.1:17781", AuthKey: key, Hostname: "agentbox-real-test",
		StateDir: filepath.Join(t.TempDir(), "state"), Owner: "test",
		LoginServer: os.Getenv("AGENTBOX_TS_LOGIN_SERVER"),
		OnUp:        func(n TailscaleNode) { up <- n },
	}, func(st Status) { t.Logf("status: %+v", st) }, t.Logf)
	defer ts.Stop()
	select {
	case n := <-up:
		t.Logf("serving at %s (node %s, %v, tailnet %q)", n.URL, n.NodeID, n.IPs, n.Tailnet)
	case <-time.After(3 * time.Minute):
		t.Fatalf("not up: %+v", ts.Status())
	}
}
