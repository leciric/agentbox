package daemon

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hubtest"
	"agentbox/internal/state"
)

// TestShareQuickTunnelMoveProbe moves an owner from one real Cloudflare quick
// tunnel to the next, as cloudflared starting again does, with three peers
// paired through the first: one the owner tells (it has a phone page of its
// own), one that asks their hub account, and one the user gives a new link:
//
//	AGENTBOX_SHARE_PROBE=$HOME/tools/cloudflared/cloudflared go test -run TestShareQuickTunnelMoveProbe -v -count=1 ./internal/daemon
func TestShareQuickTunnelMoveProbe(t *testing.T) {
	bin := os.Getenv("AGENTBOX_SHARE_PROBE")
	if bin == "" {
		t.Skip("AGENTBOX_SHARE_PROBE (cloudflared's path) isn't set")
	}
	ctx := context.Background()
	owner := startTestDaemon(t, t.TempDir(), fakeIncus)
	told := startTestDaemon(t, t.TempDir(), fakeIncus)
	hubbed := startTestDaemon(t, t.TempDir(), fakeIncus)
	linked := startTestDaemon(t, t.TempDir(), fakeIncus)
	owner.srv.lan.mu.Lock()
	owner.srv.lan.own = true
	// Its LAN address would do for peers on this machine: only the tunnel's
	// is given out.
	owner.srv.lan.addresses = func() []string { return nil }
	owner.srv.lan.cloudflared = func(context.Context, func(string)) (string, error) { return bin, nil }
	owner.srv.lan.tunnelPort = 0
	owner.srv.lan.mu.Unlock()
	if err := owner.srv.store.AddProject(ctx, state.Project{Name: "shared", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	name := "Alice"
	if _, err := owner.client.UpdateShare(ctx, api.UpdateShareRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	phonePageOn(t, told)
	h := hubtest.New(t)
	for _, d := range []testDaemon{owner, hubbed} {
		env := h.AddEnvironment("machine")
		if st, err := d.client.ConnectRemote(ctx, api.RemoteConnectRequest{Hub: h.URL, Token: env.Token}); err != nil || !st.Connected {
			t.Fatalf("ConnectRemote = %+v, %v", st, err)
		}
	}
	port, on, off, quick := freePort(t), true, false, state.LANTunnelQuick
	if _, err := owner.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &on, Port: &port, Tunnel: &on, TunnelMode: &quick}); err != nil {
		t.Fatal(err)
	}
	first := waitForQuickTunnel(t, owner, "")

	peers := []struct {
		name string
		d    testDaemon
		id   string
	}{{"told", told, ""}, {"hubbed", hubbed, ""}, {"linked", linked, ""}}
	for i := range peers {
		pairing, err := owner.client.PairShare(ctx)
		if err != nil || !strings.HasPrefix(pairing.Link, first) {
			t.Fatalf("PairShare() = %+v, %v; want a link at %s", pairing, err, first)
		}
		r, err := peers[i].d.client.AddShareRemote(ctx, pairing.Link)
		if err != nil {
			t.Fatalf("%s pairing: %v", peers[i].name, err)
		}
		peers[i].id = r.ID
	}
	st, _ := owner.client.Share(ctx)
	for _, p := range st.Peers {
		if _, err := owner.client.UpdateSharePeer(ctx, p.ID, api.UpdateSharePeerRequest{AllProjects: &on}); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range peers {
		waitLong(t, p.name+" showing the shared project", time.Minute, func() bool {
			return slices.Equal(remoteNames(t, p.d), []string{p.id + "~shared"})
		})
	}

	// cloudflared starts again: a new quick tunnel, the old one gone.
	t.Logf("first tunnel %s; starting another", first)
	if _, err := owner.client.UpdateLAN(ctx, api.UpdateLANRequest{Tunnel: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.client.UpdateLAN(ctx, api.UpdateLANRequest{Tunnel: &on}); err != nil {
		t.Fatal(err)
	}
	second := waitForQuickTunnel(t, owner, first)
	t.Logf("second tunnel %s", second)
	moved := time.Now()

	remoteOf := func(d testDaemon) api.ShareRemote {
		st, err := d.client.Share(ctx)
		if err != nil || len(st.Remotes) != 1 {
			t.Fatalf("Share() = %+v, %v", st, err)
		}
		return st.Remotes[0]
	}
	for _, p := range peers[:2] {
		waitLong(t, p.name+" at the second tunnel", 3*time.Minute, func() bool {
			r := remoteOf(p.d)
			return r.URL == strings.TrimRight(second, "/") && r.Error == "" && slices.Equal(remoteNames(t, p.d), []string{p.id + "~shared"})
		})
		t.Logf("%s reached the second tunnel %s after it was up", p.name, time.Since(moved).Round(time.Millisecond))
	}
	waitLong(t, "linked saying its owner's address changed", 2*time.Minute, func() bool {
		return strings.Contains(remoteOf(linked).Error, "Alice's address changed")
	})
	t.Logf("linked says: %s", remoteOf(linked).Error)
	pairing, err := owner.client.PairShare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := linked.client.AddShareRemote(ctx, pairing.Link)
	if err != nil || r.ID != peers[2].id || r.URL != strings.TrimRight(second, "/") {
		t.Fatalf("pasting the new link = %+v, %v; want %s moved to %s", r, err, peers[2].id, second)
	}
	waitLong(t, "linked showing the shared project again", time.Minute, func() bool {
		return remoteOf(linked).Error == "" && slices.Equal(remoteNames(t, linked), []string{peers[2].id + "~shared"})
	})
	if st, _ := owner.client.Share(ctx); len(st.Peers) != 3 {
		t.Errorf("the owner has %d peers; want the same three", len(st.Peers))
	}
}

// waitForQuickTunnel waits for d's quick tunnel to be up at an address other
// than not, and to answer there.
func waitForQuickTunnel(t *testing.T, d testDaemon, not string) string {
	t.Helper()
	var at string
	waitLong(t, "a quick tunnel", 2*time.Minute, func() bool {
		st, _ := d.client.LAN(context.Background())
		if st.Tunnel.URL == "" || st.Tunnel.URL == not {
			return false
		}
		at = st.Tunnel.URL
		u, _ := url.Parse(at)
		resp, err := http.Get(u.JoinPath("lan", "session").String())
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode != http.StatusBadGateway && resp.StatusCode < 520
	})
	return at
}

func waitLong(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}
