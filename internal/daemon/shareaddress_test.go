package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hubtest"
	"agentbox/internal/state"
)

// movingPair is an owner sharing "shared" with a peer, before the owner's
// address changes.
type movingPair struct {
	owner, peer testDaemon
	remote      api.ShareRemote
	peerID      string // the pairing's id on the owner
	token       string // the peer's, which no move may change
	ownerPort   int
}

type movingOpts struct {
	peerLAN bool                     // the peer has an address of its own: the owner can tell it
	link    func(link string) string // what the peer is given in place of the owner's link
}

func pairMoving(t *testing.T, o movingOpts) movingPair {
	t.Helper()
	ctx := context.Background()
	m := movingPair{owner: startTestDaemon(t, t.TempDir(), fakeIncus), peer: startTestDaemon(t, t.TempDir(), fakeIncus)}
	m.owner.srv.peering.running = func(context.Context, []string) int { return 0 }
	name := "Alice"
	m.ownerPort = phonePageOn(t, m.owner)
	if o.peerLAN {
		phonePageOn(t, m.peer)
	}
	if err := m.owner.srv.store.AddProject(ctx, state.Project{Name: "shared", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.owner.client.UpdateShare(ctx, api.UpdateShareRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	pairing, err := m.owner.client.PairShare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	link := pairing.Link
	if o.link != nil {
		link = o.link(link)
	}
	if m.remote, err = m.peer.client.AddShareRemote(ctx, link); err != nil {
		t.Fatal(err)
	}
	st, err := m.owner.client.Share(ctx)
	if err != nil || len(st.Peers) != 1 {
		t.Fatalf("Share() = %+v, %v", st, err)
	}
	m.peerID = st.Peers[0].ID
	three := 3
	if _, err := m.owner.client.UpdateSharePeer(ctx, m.peerID, api.UpdateSharePeerRequest{Projects: &[]string{"shared"}, Slots: &three}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the shared project on the peer", func() bool {
		return slices.Equal(remoteNames(t, m.peer), []string{m.remote.ID + "~shared"})
	})
	m.token = m.peerToken(t)
	return m
}

// phonePageOn opens d's phone page on a free port of 127.0.0.1, its one
// address.
func phonePageOn(t *testing.T, d testDaemon) int {
	t.Helper()
	d.srv.lan.mu.Lock()
	d.srv.lan.own = true
	d.srv.lan.addresses = func() []string { return []string{"127.0.0.1"} }
	d.srv.lan.mu.Unlock()
	port, on := freePort(t), true
	if _, err := d.client.UpdateLAN(context.Background(), api.UpdateLANRequest{Enabled: &on, Port: &port}); err != nil {
		t.Fatal(err)
	}
	return port
}

func (m movingPair) peerToken(t *testing.T) string {
	t.Helper()
	remotes, err := m.peer.srv.store.ShareRemotes(context.Background())
	if err != nil || len(remotes) != 1 {
		t.Fatalf("the peer's remotes = %+v, %v", remotes, err)
	}
	token, err := m.peer.srv.secrets().Open(remotes[0].Token)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (m movingPair) remoteNow(t *testing.T) api.ShareRemote {
	t.Helper()
	st, err := m.peer.client.Share(context.Background())
	if err != nil || len(st.Remotes) != 1 {
		t.Fatalf("the peer's Share() = %+v, %v", st, err)
	}
	return st.Remotes[0]
}

// moveOwner moves the owner's phone page to another port: its address
// changes, and what the peer has open there ends.
func (m *movingPair) moveOwner(t *testing.T) string {
	t.Helper()
	return m.moveOwnerTo(t, freePort(t))
}

func (m *movingPair) moveOwnerTo(t *testing.T, port int) string {
	t.Helper()
	if _, err := m.owner.client.UpdateLAN(context.Background(), api.UpdateLANRequest{Port: &port}); err != nil {
		t.Fatal(err)
	}
	m.ownerPort = port
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

// stillPaired checks the peer reaches its owner at want, with the token and
// the one pairing it had, the project and the slots it was given, and the
// owner's events.
func (m movingPair) stillPaired(t *testing.T, want string) {
	t.Helper()
	ctx := context.Background()
	var last api.ShareRemote
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if last = m.remoteNow(t); last.URL == want && last.Error == "" && slices.Equal(remoteNames(t, m.peer), []string{m.remote.ID + "~shared"}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the peer's remote = %+v; want it at %s, with its project", last, want)
		}
	}
	if r := m.remoteNow(t); r.ID != m.remote.ID {
		t.Errorf("the remote is %s now, was %s: it should have moved, not paired again", r.ID, m.remote.ID)
	}
	if token := m.peerToken(t); token != m.token {
		t.Error("the peer's token changed")
	}
	st, err := m.owner.client.Share(ctx)
	if err != nil || len(st.Peers) != 1 || st.Peers[0].ID != m.peerID || !slices.Equal(st.Peers[0].Projects, []string{"shared"}) || st.Peers[0].Slots != 3 {
		t.Fatalf("the owner's peers = %+v, %v; want the one pairing, with its project and slots", st.Peers, err)
	}
	local := m.remote.ID + "~shared"
	events, unsubscribe := m.peer.srv.events.subscribe()
	defer unsubscribe()
	waitFor(t, "the owner's chat event on the peer", func() bool {
		m.owner.srv.events.publish(api.EventChat, api.ChatEvent{Agent: "shared/lead", Append: &api.ChatAppend{ID: "reply", Text: " word"}})
		for {
			select {
			case ev := <-events:
				if ev.Type == api.EventChat && strings.Contains(string(ev.Data), `"agent":"`+local+`/lead"`) {
					return true
				}
			case <-time.After(100 * time.Millisecond):
				return false
			}
		}
	})
	app := newPhone(t, "http://agentbox", m.peer.client.HTTPClient().Transport)
	if code, body := app.do("GET", "/v1/projects/"+local+"/sessions", ""); code != http.StatusOK {
		t.Errorf("GET the shared project's sessions after the move = %d %s", code, body)
	}
}

// The owner's address changes, and it tells the peer, which said at pairing
// where it can be reached: the peer moves to the new address on its own.
func TestShareOwnerTellsPeerItsNewAddress(t *testing.T) {
	t.Parallel()
	m := pairMoving(t, movingOpts{peerLAN: true})
	ctx := context.Background()
	old := "http://127.0.0.1:" + strconv.Itoa(m.ownerPort)
	peers, _ := m.owner.srv.store.SharePeers(ctx)
	if len(peers) != 1 || len(peers[0].URLs) != 1 || !strings.HasPrefix(peers[0].URLs[0], "http://127.0.0.1:") {
		t.Fatalf("where the owner reaches the peer = %+v; want its phone page", peers)
	}
	// An address that doesn't change stays: telling the peer the same one
	// again moves nothing.
	m.owner.srv.publishLAN(ctx)
	time.Sleep(300 * time.Millisecond)
	if r := m.remoteNow(t); r.URL != old {
		t.Fatalf("the peer is at %s with its owner unmoved; want %s", r.URL, old)
	}

	m.stillPaired(t, m.moveOwner(t))
}

// The owner's address changes, the peer has none the owner could tell it at,
// and both are on one hub account: the peer asks the hub's machines for its
// owner.
func TestSharePeerFindsOwnerThroughTheHub(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := pairMoving(t, movingOpts{})
	if peers, _ := m.owner.srv.store.SharePeers(ctx); len(peers) != 1 || len(peers[0].URLs) != 0 {
		t.Fatalf("the owner's peers = %+v; want one with no address", peers)
	}
	h := hubtest.New(t)
	for _, d := range []testDaemon{m.owner, m.peer} {
		env := h.AddEnvironment("machine")
		if st, err := d.client.ConnectRemote(ctx, api.RemoteConnectRequest{Hub: h.URL, Token: env.Token}); err != nil || !st.Connected {
			t.Fatalf("ConnectRemote = %+v, %v", st, err)
		}
	}
	// A machine of the account that isn't the owner, or a request not
	// signed by the pairing, learns nothing.
	if code := askHubForOwner(t, m.peer, m.owner, "peer_000000000000", m.token); code != http.StatusNotFound {
		t.Errorf("asking for a pairing the owner doesn't have = %d, want 404", code)
	}
	if code := askHubForOwner(t, m.peer, m.owner, m.peerID, "share_"+m.peerID+".wrong"); code != http.StatusUnauthorized {
		t.Errorf("asking with another key = %d, want 401", code)
	}

	m.stillPaired(t, m.moveOwner(t))
}

// A peer that can't reach its owner where it is tries the other addresses
// the owner gave it before it moved: one that took the owner's LAN address
// while the owner's tunnel was off is back on the tunnel once it leaves
// that network, though nothing tells it.
func TestSharePeerTriesAddressesItWasTold(t *testing.T) {
	t.Parallel()
	m := pairMoving(t, movingOpts{})
	// The owner's "tunnel": its phone page at the port it is about to move
	// to, which it gave the peer earlier.
	port := freePort(t)
	tunnel := httptest.NewServer(httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}))
	defer tunnel.Close()
	a := m.peer.srv.peering.addrs
	a.mu.Lock()
	a.known[m.remote.ID] = []string{tunnel.URL, "http://127.0.0.1:" + strconv.Itoa(m.ownerPort)}
	a.mu.Unlock()

	m.moveOwnerTo(t, port)
	m.stillPaired(t, tunnel.URL)
}

// push sends the peer the owner's addresses, at the peer's address, as the
// owner does, and says how it answered.
func (m movingPair) push(t *testing.T, urls ...string) int {
	t.Helper()
	peers, err := m.owner.srv.store.SharePeers(context.Background())
	if err != nil || len(peers) != 1 || len(peers[0].URLs) == 0 {
		t.Fatalf("the owner's peers = %+v, %v; want one with an address", peers, err)
	}
	body, _ := json.Marshal(api.ShareAddress{Peer: m.peerID, URLs: urls, At: time.Now()})
	req, _ := http.NewRequest(http.MethodPost, peers[0].URLs[0]+"/share/owner-address", bytes.NewReader(body))
	req.Header.Set(headerShareProof, shareMAC(state.HashPhoneToken(m.token), "address", string(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// A peer stays at the address it paired at while that works, whatever else
// the owner has; one that took another address only because the owner's
// first didn't answer yet (a new quick tunnel's name not in DNS) moves up to
// the first once it does.
func TestSharePeerMovesUpToTheOwnersFirstAddress(t *testing.T) {
	t.Parallel()
	m := pairMoving(t, movingOpts{peerLAN: true})
	paired := "http://127.0.0.1:" + strconv.Itoa(m.ownerPort)
	var down atomic.Bool
	down.Store(true)
	tunnel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "error code: 1033", 530)
			return
		}
		httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(m.ownerPort)}).ServeHTTP(w, r)
	}))
	defer tunnel.Close()

	if code := m.push(t, tunnel.URL, paired); code != http.StatusNoContent || m.remoteNow(t).URL != paired {
		t.Fatalf("telling a peer an address before the one it paired at = %d, at %s; want 204, staying at %s", code, m.remoteNow(t).URL, paired)
	}
	// Its address is gone: it takes the owner's other one, for now.
	other := "http://localhost:" + strconv.Itoa(m.ownerPort)
	if code := m.push(t, tunnel.URL, other); code != http.StatusAccepted || m.remoteNow(t).URL != other {
		t.Fatalf("an owner whose first address doesn't answer = %d, at %s; want 202, at %s", code, m.remoteNow(t).URL, other)
	}
	if code := m.push(t, tunnel.URL, other); code != http.StatusAccepted || m.remoteNow(t).URL != other {
		t.Fatalf("the same while it still doesn't = %d, at %s", code, m.remoteNow(t).URL)
	}
	down.Store(false)
	if code := m.push(t, tunnel.URL, other); code != http.StatusNoContent {
		t.Fatalf("once the owner's first address answers = %d, want 204", code)
	}
	m.stillPaired(t, tunnel.URL)
}

// askHubForOwner asks owner, through the hub, where it is for pairing id,
// signing with token, and says how it answered.
func askHubForOwner(t *testing.T, peer, owner testDaemon, id, token string) int {
	t.Helper()
	cfg := peer.srv.readRemoteConfig()
	_, machines, err := peer.srv.hubPeers(context.Background(), true)
	if err != nil || len(machines) != 1 {
		t.Fatalf("the peer's hub machines = %+v, %v", machines, err)
	}
	at := strconv.FormatInt(time.Now().Unix(), 10)
	req, _ := http.NewRequest(http.MethodGet, strings.TrimRight(cfg.Hub, "/")+"/v1/environment/peers/"+machines[0].ID+"/api/v1/peer/share-address", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set(headerSharePeer, id)
	req.Header.Set(headerShareAt, at)
	req.Header.Set(headerShareProof, shareMAC(state.HashPhoneToken(token), "lookup", id, at))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// The owner is on a quick tunnel that went away, and nothing could tell the
// peer where it went: the peer says the owner's address changed, and a new
// link from the owner moves the pairing, which keeps its token, project and
// slots. Another AgentBox's link still pairs anew.
func TestShareNewLinkMovesThePairing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// The owner's "quick tunnels": each passes requests on to its phone
	// page until it goes, then answers as Cloudflare does for one gone.
	var ownerAt atomic.Pointer[url.URL] // the owner's phone page
	tunnel := func() (*httptest.Server, *atomic.Bool) {
		gone := &atomic.Bool{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if gone.Load() {
				http.Error(w, "error code: 1033", 530)
				return
			}
			httputil.NewSingleHostReverseProxy(ownerAt.Load()).ServeHTTP(w, r)
		}))
		t.Cleanup(srv.Close)
		return srv, gone
	}
	first, firstGone := tunnel()
	m := pairMoving(t, movingOpts{link: func(link string) string {
		u, _ := url.Parse(link)
		ownerAt.Store(&url.URL{Scheme: u.Scheme, Host: u.Host})
		return first.URL + "/#" + u.Fragment
	}})
	if r := m.remoteNow(t); r.URL != first.URL {
		t.Fatalf("the peer paired at %s, want %s", r.URL, first.URL)
	}
	m.peer.srv.peering.remotes.quick = func(host string) bool { return true }

	// cloudflared starts again: the old tunnel is gone, a new one is up.
	firstGone.Store(true)
	moved, _ := url.Parse(m.moveOwner(t))
	ownerAt.Store(moved)
	second, _ := tunnel()
	waitFor(t, "the peer saying its owner's address changed", func() bool {
		return strings.Contains(m.remoteNow(t).Error, "Alice's address changed")
	})

	pairing, err := m.owner.client.PairShare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(pairing.Link)
	got, err := m.peer.client.AddShareRemote(ctx, second.URL+"/#"+u.Fragment)
	if err != nil || got.ID != m.remote.ID || got.URL != second.URL || got.Projects != 1 {
		t.Fatalf("AddShareRemote(the owner's new link) = %+v, %v; want %s moved to %s, with its project", got, err, m.remote.ID, second.URL)
	}
	// The app lists the projects again as the pairing moves: the shared one
	// is there at once, not once the owner has been asked at its new address.
	if names := remoteNames(t, m.peer); !slices.Equal(names, []string{m.remote.ID + "~shared"}) {
		t.Errorf("the peer's remote projects right after the move = %v", names)
	}
	m.stillPaired(t, second.URL)

	// Another AgentBox doesn't take the proof of a pairing it never made.
	other := startTestDaemon(t, t.TempDir(), fakeIncus)
	phonePageOn(t, other)
	pairing, err = other.client.PairShare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.peer.client.AddShareRemote(ctx, pairing.Link); err != nil || got.ID == m.remote.ID {
		t.Fatalf("pairing with another AgentBox = %+v, %v; want a new remote", got, err)
	}
	if st, _ := other.client.Share(ctx); len(st.Peers) != 1 {
		t.Errorf("the other AgentBox's peers = %+v; want the one it just paired", st.Peers)
	}
}

// An owner's new address reaches the peer only signed with the pairing's key,
// newer than the last, and at an address where the owner shows it is.
func TestShareOwnerAddressIsSigned(t *testing.T) {
	t.Parallel()
	m := pairMoving(t, movingOpts{peerLAN: true})
	ctx := context.Background()
	old := m.remoteNow(t).URL
	peers, _ := m.owner.srv.store.SharePeers(ctx)
	to := peers[0].URLs[0] + "/share/owner-address"
	key := state.HashPhoneToken(m.token)
	post := func(a api.ShareAddress, sign func(body []byte) string) int {
		body, _ := json.Marshal(a)
		req, _ := http.NewRequest(http.MethodPost, to, bytes.NewReader(body))
		req.Header.Set(headerShareProof, sign(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	signed := func(body []byte) string { return shareMAC(key, "address", string(body)) }
	stranger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the peer sent its token to an address its owner never answered at: %s %s", r.Method, r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer stranger.Close()
	now := time.Now()
	for _, c := range []struct {
		what string
		a    api.ShareAddress
		sign func([]byte) string
		want int
	}{
		{"unsigned", api.ShareAddress{Peer: m.peerID, URLs: []string{stranger.URL}, At: now}, func([]byte) string { return "" }, http.StatusUnauthorized},
		{"signed with another key", api.ShareAddress{Peer: m.peerID, URLs: []string{stranger.URL}, At: now}, func(b []byte) string { return shareMAC("x", "address", string(b)) }, http.StatusUnauthorized},
		{"a pairing it doesn't have", api.ShareAddress{Peer: "peer_000000000000", URLs: []string{stranger.URL}, At: now}, signed, http.StatusNotFound},
		{"an address where the owner isn't", api.ShareAddress{Peer: m.peerID, URLs: []string{stranger.URL}, At: now}, signed, http.StatusBadGateway},
		{"older than the last", api.ShareAddress{Peer: m.peerID, URLs: []string{stranger.URL}, At: now.Add(-time.Second)}, signed, http.StatusConflict},
		{"far from now", api.ShareAddress{Peer: m.peerID, URLs: []string{stranger.URL}, At: now.Add(time.Hour)}, signed, http.StatusConflict},
		{"where it is already", api.ShareAddress{Peer: m.peerID, URLs: []string{old}, At: now.Add(time.Second)}, signed, http.StatusNoContent},
	} {
		if got := post(c.a, c.sign); got != c.want {
			t.Errorf("%s = %d, want %d", c.what, got, c.want)
		}
	}
	if r := m.remoteNow(t); r.URL != old {
		t.Errorf("the peer moved to %s", r.URL)
	}
}

func TestShareMAC(t *testing.T) {
	a := shareMAC("key", "hello", "ab", "c")
	for _, other := range []string{shareMAC("key", "hello", "a", "bc"), shareMAC("key", "again", "ab", "c"), shareMAC("other", "hello", "ab", "c")} {
		if other == a {
			t.Errorf("two different messages signed the same: %s", a)
		}
	}
	if !shareMACOK("key", "hello", a, "ab", "c") || shareMACOK("key", "hello", "", "ab", "c") {
		t.Error("shareMACOK doesn't check what shareMAC signs")
	}
	if got := sharePeerIDOf("share_peer_0a1b2c3d4e5f.c2VjcmV0"); got != "peer_0a1b2c3d4e5f" {
		t.Errorf("sharePeerIDOf = %q", got)
	}
	for _, bad := range []string{"", "phone_x.y", "share_nodot"} {
		if got := sharePeerIDOf(bad); got != "" {
			t.Errorf("sharePeerIDOf(%q) = %q", bad, got)
		}
	}
}

func TestShareURLList(t *testing.T) {
	got, err := shareURLList([]string{"https://a.trycloudflare.com/", "http://192.168.1.5:7780/", "https://a.trycloudflare.com"})
	if err != nil || !slices.Equal(got, []string{"https://a.trycloudflare.com", "http://192.168.1.5:7780"}) {
		t.Errorf("shareURLList = %v, %v", got, err)
	}
	for _, bad := range []string{"ftp://x", "https://user@x", "https://x/path", "https://x/?q", "x"} {
		if _, err := shareURLList([]string{bad}); err == nil {
			t.Errorf("shareURLList(%q) took it", bad)
		}
	}
}
