package daemon

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"

	"github.com/coder/websocket"
)

// TestShareLatencyProbe times one streamed reply from an owner's event bus to
// a peer's app, hop by hop, through a real Cloudflare quick tunnel, and the
// same events read as server-sent events, which the tunnel holds back:
//
//	AGENTBOX_SHARE_PROBE=$HOME/tools/cloudflared/cloudflared go test -run TestShareLatencyProbe -v -count=1 ./internal/daemon
func TestShareLatencyProbe(t *testing.T) {
	bin := os.Getenv("AGENTBOX_SHARE_PROBE")
	if bin == "" {
		t.Skip("AGENTBOX_SHARE_PROBE (cloudflared's path) isn't set")
	}
	ctx := context.Background()
	owner := startTestDaemon(t, t.TempDir(), fakeIncus)
	peer := startTestDaemon(t, t.TempDir(), fakeIncus)
	owner.srv.lan.mu.Lock()
	owner.srv.lan.own = true
	owner.srv.lan.addresses = func() []string { return []string{"127.0.0.1"} }
	owner.srv.lan.cloudflared = func(context.Context, func(string)) (string, error) { return bin, nil }
	owner.srv.lan.tunnelPort = 0
	owner.srv.lan.mu.Unlock()
	if err := owner.srv.store.AddProject(ctx, state.Project{Name: "shared", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	port, on, quick := freePort(t), true, state.LANTunnelQuick
	if _, err := owner.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &on, Port: &port, Tunnel: &on, TunnelMode: &quick}); err != nil {
		t.Fatal(err)
	}
	var tunnelURL string
	deadline := time.Now().Add(90 * time.Second)
	for tunnelURL == "" && time.Now().Before(deadline) {
		st, _ := owner.client.LAN(ctx)
		tunnelURL = st.Tunnel.URL
		if tunnelURL == "" {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if tunnelURL == "" {
		t.Fatal("the quick tunnel didn't come up")
	}
	tunnelURL = strings.TrimSuffix(tunnelURL, "/")
	t.Logf("quick tunnel: %s", tunnelURL)
	// A new trycloudflare.com name takes a while to resolve everywhere.
	for time.Now().Before(deadline) {
		resp, err := http.Get(tunnelURL + "/lan/session")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusBadGateway && resp.StatusCode < 520 {
				break
			}
		}
		time.Sleep(time.Second)
	}

	pairing, err := owner.client.PairShare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	link := ""
	for _, l := range pairing.Links {
		if strings.HasPrefix(l, tunnelURL) {
			link = l
		}
	}
	if link == "" {
		t.Fatalf("no tunnel link in %v", pairing.Links)
	}
	if _, err := peer.client.AddShareRemote(ctx, link); err != nil {
		t.Fatal(err)
	}
	st, _ := owner.client.Share(ctx)
	if _, err := owner.client.UpdateSharePeer(ctx, st.Peers[0].ID, api.UpdateSharePeerRequest{AllProjects: &on}); err != nil {
		t.Fatal(err)
	}
	sealed, _ := peer.srv.store.ShareRemotes(ctx)
	token, err := peer.srv.secrets().Open(sealed[0].Token)
	if err != nil {
		t.Fatal(err)
	}

	// Each hop's arrival time of every probe event, by its number.
	type hop struct {
		name string
		mu   sync.Mutex
		at   map[int]time.Time
	}
	hops := []*hop{}
	newHop := func(name string) *hop {
		h := &hop{name: name, at: map[int]time.Time{}}
		hops = append(hops, h)
		return h
	}
	probe := regexp.MustCompile(`probe-(\d+)`)
	mark := func(h *hop, b []byte) {
		now := time.Now()
		for _, m := range probe.FindAllSubmatch(b, -1) {
			n, _ := strconv.Atoi(string(m[1]))
			h.mu.Lock()
			if _, ok := h.at[n]; !ok {
				h.at[n] = now
			}
			h.mu.Unlock()
		}
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	sse := func(h *hop, client *http.Client, url string, header ...string) {
		req, _ := http.NewRequestWithContext(runCtx, http.MethodGet, url, nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("%s: %v", h.name, err)
			return
		}
		t.Logf("%s: %s, Content-Type %q, Content-Encoding %q, Uncompressed %v, proto %s", h.name, resp.Status, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Encoding"), resp.Uncompressed, resp.Proto)
		if resp.StatusCode != http.StatusOK {
			b := make([]byte, 600)
			k, _ := resp.Body.Read(b)
			t.Logf("%s: %q", h.name, b[:k])
		}
		go func() {
			defer func() { _ = resp.Body.Close() }()
			br := bufio.NewReader(resp.Body)
			for {
				line, err := br.ReadBytes('\n')
				mark(h, line)
				if err != nil {
					return
				}
			}
		}()
	}
	ws := func(h *hop, url string) {
		conn, _, err := websocket.Dial(runCtx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
		if err != nil {
			t.Logf("%s: %v", h.name, err) // an owner from before
			return
		}
		conn.SetReadLimit(32 << 20)
		go func() {
			defer func() { _ = conn.CloseNow() }()
			for {
				_, data, err := conn.Read(runCtx)
				if err != nil {
					return
				}
				mark(h, data)
			}
		}()
	}
	bus := func(h *hop, b *broker) {
		ch, unsubscribe := b.subscribe()
		go func() {
			defer unsubscribe()
			for {
				select {
				case <-runCtx.Done():
					return
				case ev := <-ch:
					if ev.Type == api.EventChat {
						mark(h, ev.Data)
					}
				}
			}
		}()
	}

	h1 := newHop("1 owner bus")
	bus(h1, owner.srv.events)
	h2local := newHop("2 share SSE, LAN port")
	sse(h2local, &http.Client{}, fmt.Sprintf("http://127.0.0.1:%d/share/api/v1/events", port), "Authorization", "Bearer "+token)
	h2tunnel := newHop("2 share SSE, tunnel")
	sse(h2tunnel, &http.Client{}, tunnelURL+"/share/api/v1/events", "Authorization", "Bearer "+token)
	h2plain := newHop("2 share SSE, tunnel, identity")
	sse(h2plain, &http.Client{}, tunnelURL+"/share/api/v1/events", "Authorization", "Bearer "+token, "Accept-Encoding", "identity")
	h2ws := newHop("2 share WebSocket, tunnel")
	ws(h2ws, tunnelURL+"/share/api/v1/events")
	h3 := newHop("3 peer bus (remoteEvents)")
	bus(h3, peer.srv.events)
	h4 := newHop("4 peer socket SSE (the app)")
	sse(h4, peer.client.HTTPClient(), "http://agentbox/v1/events")

	// The peer's stream starts over once the project is shared.
	warm := time.Now()
	for time.Since(warm) < 30*time.Second {
		owner.srv.events.publish(api.EventChat, api.ChatEvent{Agent: "shared/lead", Seq: 0, Append: &api.ChatAppend{ID: "warm", Text: "probe-999"}})
		h3.mu.Lock()
		_, ok := h3.at[999]
		h3.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("the peer got its first event %s after sharing", time.Since(warm).Round(time.Millisecond))
	time.Sleep(time.Second)

	const n = 40
	sent := map[int]time.Time{}
	item := &api.ChatItem{ID: "reply", Kind: "assistant", Text: ""}
	owner.srv.events.publish(api.EventChat, api.ChatEvent{Agent: "shared/lead", Seq: 1, Item: item})
	for i := range n {
		sent[i] = time.Now()
		owner.srv.events.publish(api.EventChat, api.ChatEvent{Agent: "shared/lead", Seq: int64(i + 2), Append: &api.ChatAppend{ID: "reply", Text: fmt.Sprintf(" probe-%d", i)}})
		time.Sleep(75 * time.Millisecond)
	}
	time.Sleep(8 * time.Second)

	var out bytes.Buffer
	fmt.Fprintf(&out, "\n%-32s %8s %8s %8s %8s %5s\n", "hop", "p50 ms", "p90 ms", "max ms", "first", "got")
	for _, h := range hops {
		h.mu.Lock()
		var d []float64
		first := -1.0
		for i := range n {
			if at, ok := h.at[i]; ok {
				d = append(d, float64(at.Sub(sent[i]).Microseconds())/1000)
				if i == 0 {
					first = d[len(d)-1]
				}
			}
		}
		h.mu.Unlock()
		slices.Sort(d)
		pct := func(p float64) float64 {
			if len(d) == 0 {
				return -1
			}
			return d[min(len(d)-1, int(p*float64(len(d))))]
		}
		fmt.Fprintf(&out, "%-32s %8.1f %8.1f %8.1f %8.1f %3d/%d\n", h.name, pct(0.5), pct(0.9), pct(1), first, len(d), n)
	}
	t.Log(out.String())
}
