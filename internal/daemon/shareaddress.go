package daemon

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/hubapi"
	"agentbox/internal/api"
	"agentbox/internal/remote"
	"agentbox/internal/state"
	"agentbox/internal/tunnel"
)

// A pairing that outlives its owner's address. A peer keeps the address it
// paired at (shareLink), and a quick tunnel's is new every time cloudflared
// starts (lantunnel.go), as a LAN address can be; a named tunnel's never
// changes. The peer finds its owner again by whichever of these it has:
//
//  1. The owner tells it. At pairing the peer says where it can be reached
//     (SharePairRequest.URLs, its phone page's addresses), and again when
//     those change (/share/peer-address). Whenever the owner's own addresses
//     change, it sends them to every peer that has one (/share/owner-address),
//     signed with the pairing's key (shareMAC), and tries again later those
//     it couldn't reach (tellShareAddresses).
//  2. The hub tells it. When both machines are on one hub account, a peer
//     that can't reach its owner asks the account's other machines, through
//     the hub, which of them is its owner and where it is now
//     (/v1/peer/share-address, servePeer).
//  3. The user tells it. A peer whose owner's quick tunnel is gone says
//     "<owner>'s address changed", and a new link from the owner, pasted
//     then, only moves the pairing (SharePairRequest.Again): it keeps its
//     token, and the projects and slots the owner gave it.
//
// The token never crosses the wire for any of it, and a peer sends its token
// to an address it was told of only once the owner has shown it is there
// (/share/hello): an owner's LAN address may be a stranger's on the peer's
// own network.

// shareMAC signs a message between the two sides of a pairing: an HMAC keyed
// with the hash of the peer's token, which the owner keeps
// (state.SharePeerKey) and the peer works out from the token it keeps. what
// says what it signs, so a proof of one thing is none of another.
func shareMAC(key, what string, parts ...string) string {
	m := hmac.New(sha256.New, []byte(key))
	for _, p := range append([]string{"agentbox-share-" + what}, parts...) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		m.Write(n[:])
		m.Write([]byte(p))
	}
	return hex.EncodeToString(m.Sum(nil))
}

func shareMACOK(key, what, proof string, parts ...string) bool {
	return proof != "" && hmac.Equal([]byte(shareMAC(key, what, parts...)), []byte(proof))
}

// sharePeerIDOf is the id on its owner of the pairing a token is for:
// "share_<id>.<secret>" (state.AddSharePeer).
func sharePeerIDOf(token string) string {
	rest, ok := strings.CutPrefix(token, "share_")
	id, _, dot := strings.Cut(rest, ".")
	if !ok || !dot {
		return ""
	}
	return id
}

// Headers of the requests that find a pairing's owner.
const (
	headerSharePeer  = "X-AgentBox-Share-Peer" // the pairing's id on the owner
	headerShareNonce = "X-AgentBox-Share-Nonce"
	headerShareAt    = "X-AgentBox-Share-At" // Unix seconds
	headerShareProof = "X-AgentBox-Share-Proof"
)

// shareSkew is how far from now a signed message's time may be.
const shareSkew = 10 * time.Minute

// How long telling a machine an address waits after it couldn't be reached,
// at first and at most.
const (
	shareTellFirst = 10 * time.Second
	shareTellMax   = 5 * time.Minute
)

// How long a peer that couldn't reach its owner waits before looking for it
// again (findRemoteAgain), after the first time, and at most.
const (
	shareFindFirst = 10 * time.Second
	shareFindMax   = 10 * time.Minute
)

// shareAddrs is who this machine told its addresses (tellShareAddresses):
// "peer/<id>", a peer it owns projects for, and "remote/<id>", an owner it is
// a peer of.
type shareAddrs struct {
	wake chan struct{}

	mu     sync.Mutex
	told   map[string]string        // the addresses last told each, joined
	trying map[string]string        // the addresses being told one that couldn't be reached
	next   map[string]time.Time     // when to try that one again
	wait   map[string]time.Duration // how long it waited last
	// heard is, by remote, when the newest address its owner sent was
	// signed: an older one sent again is refused.
	heard map[string]time.Time
	// hint is, by remote, the machine of the hub account that answered as
	// its owner the last time: asked first.
	hint map[string]string
	// moving makes moving a remote to a new address one step at a time.
	moving sync.Mutex
}

func newShareAddrs() *shareAddrs {
	return &shareAddrs{wake: make(chan struct{}, 1), told: map[string]string{}, trying: map[string]string{},
		next: map[string]time.Time{}, wait: map[string]time.Duration{}, heard: map[string]time.Time{}, hint: map[string]string{}}
}

// poke has tellShareAddresses look again.
func (a *shareAddrs) poke() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// due says whether who is to be told addresses (joined in key) now.
func (a *shareAddrs) due(who, key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if told, ok := a.told[who]; ok && told == key {
		return false
	}
	if a.trying[who] != key {
		delete(a.next, who)
		delete(a.wait, who)
	}
	return !time.Now().Before(a.next[who])
}

// done records how telling who key went: told, or to be tried again later.
func (a *shareAddrs) done(who, key string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.told[who] = key
		delete(a.trying, who)
		delete(a.next, who)
		delete(a.wait, who)
		return
	}
	a.trying[who] = key
	a.wait[who] = min(max(2*a.wait[who], shareTellFirst), shareTellMax)
	a.next[who] = time.Now().Add(a.wait[who])
}

// soon has who told at once if it is waiting: it was just reached.
func (a *shareAddrs) soon(who string) {
	a.mu.Lock()
	delete(a.next, who)
	delete(a.wait, who)
	a.mu.Unlock()
	a.poke()
}

// keep forgets everyone but who, and says when the first one waiting is
// tried again (zero for none).
func (a *shareAddrs) keep(who map[string]bool) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	var first time.Time
	for _, m := range []map[string]string{a.told, a.trying} {
		for k := range m {
			if !who[k] {
				delete(m, k)
			}
		}
	}
	for k, at := range a.next {
		switch {
		case !who[k]:
			delete(a.next, k)
			delete(a.wait, k)
		case first.IsZero() || at.Before(first):
			first = at
		}
	}
	return first
}

// shareURLs are this machine's addresses another AgentBox reaches it at, the
// likeliest first (its phone page's: GET /v1/lan), and whether they are
// settled: not while its tunnel starts, its address about to change.
func (s *Server) shareURLs(ctx context.Context) ([]string, bool) {
	st, err := s.lanStatus(ctx)
	if err != nil {
		return nil, false
	}
	urls := []string{}
	if st.Enabled {
		for _, u := range st.URLs {
			urls = append(urls, strings.TrimRight(u, "/"))
		}
	}
	return urls, !st.Enabled || !st.Tunnel.Enabled || st.Tunnel.State != tunnel.StateStarting
}

// shareURLList is a list of addresses one side of a pairing said it is at:
// an http or https host with nothing after it, at most eight.
func shareURLList(in []string) ([]string, error) {
	if len(in) > 8 {
		return nil, errors.New("more than 8 addresses")
	}
	out := []string{}
	for _, raw := range in {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("%q isn't an AgentBox's address", raw)
		}
		if v := u.Scheme + "://" + u.Host; !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out, nil
}

// tellShareAddresses runs while the daemon does. Whenever this machine's
// addresses change (publishLAN pokes it), it tells them to every peer it
// owns projects for that said where it can be reached, and to every owner it
// is a peer of; one it couldn't reach is tried again later, waiting longer
// each time, until it is or the addresses change again.
func (s *Server) tellShareAddresses(ctx context.Context) {
	a := s.peering.addrs
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		if next := s.tellShareAddressesOnce(ctx); !next.IsZero() {
			timer.Reset(max(time.Until(next), time.Second))
		}
		select {
		case <-ctx.Done():
			return
		case <-a.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (s *Server) tellShareAddressesOnce(ctx context.Context) time.Time {
	a := s.peering.addrs
	urls, settled := s.shareURLs(ctx)
	if !settled {
		return time.Time{} // publishLAN pokes it once the tunnel has its address
	}
	key := strings.Join(urls, " ")
	who := map[string]bool{}
	var wg sync.WaitGroup
	if peers, err := s.store.SharePeers(ctx); err == nil && len(urls) > 0 {
		for _, p := range peers {
			k := "peer/" + p.ID
			who[k] = true
			if len(p.URLs) > 0 && a.due(k, key) {
				wg.Go(func() { a.done(k, key, s.pushShareAddress(ctx, p, urls)) })
			}
		}
	}
	for _, c := range s.remoteConns() {
		k := "remote/" + c.r.ID
		who[k] = true
		if a.due(k, key) {
			wg.Go(func() { a.done(k, key, s.reportShareAddress(ctx, c, urls)) })
		}
	}
	wg.Wait()
	return a.keep(who)
}

// pushShareAddress tells a peer this machine's addresses, at each of the
// peer's in turn. A peer that refuses them for good (it forgot the pairing,
// or isn't that AgentBox any more) isn't asked again until they change.
func (s *Server) pushShareAddress(ctx context.Context, p state.SharePeer, urls []string) error {
	key, err := s.store.SharePeerKey(ctx, p.ID)
	if err != nil {
		return nil // unpaired meanwhile
	}
	body, _ := json.Marshal(api.ShareAddress{Peer: p.ID, URLs: urls, At: time.Now()})
	proof := shareMAC(key, "address", string(body))
	var errs []error
	refused := 0
	for _, u := range p.URLs {
		code, msg, err := s.sharePost(ctx, u+"/share/owner-address", body, http.Header{headerShareProof: {proof}})
		switch {
		case err != nil:
			errs = append(errs, err)
		case code/100 == 2:
			s.logf("share: told %s (%s) at %s that this machine is at %s", p.Name, p.ID, u, strings.Join(urls, ", "))
			return nil
		case code == http.StatusNotFound || code == http.StatusUnauthorized || code == http.StatusConflict:
			refused++
			errs = append(errs, fmt.Errorf("%s: %s", u, msg))
		default:
			errs = append(errs, fmt.Errorf("%s: %s", u, msg))
		}
	}
	err = errors.Join(errs...)
	if refused == len(p.URLs) {
		s.logf("share: %s (%s) didn't take this machine's new address: %v", p.Name, p.ID, err)
		return nil
	}
	s.logf("share: telling %s (%s) this machine's new address: %v", p.Name, p.ID, err)
	return err
}

// reportShareAddress tells an owner where this machine can be reached. An
// owner from before, or one that unpaired it, isn't told again until they
// change.
func (s *Server) reportShareAddress(ctx context.Context, c *remoteConn, urls []string) error {
	body, _ := json.Marshal(api.ShareAddress{Peer: sharePeerIDOf(c.token), URLs: urls, At: time.Now()})
	code, msg, err := s.sharePost(ctx, strings.TrimRight(c.r.URL, "/")+"/share/peer-address", body, http.Header{"Authorization": {"Bearer " + c.token}})
	switch {
	case err != nil:
		return err
	case code/100 == 2, code == http.StatusNotFound, code == http.StatusUnauthorized:
		return nil
	}
	return fmt.Errorf("%s answered %s", c.r.Name, msg)
}

// sharePost posts a JSON body to the other side of a pairing, and says how
// it answered.
func (s *Server) sharePost(ctx context.Context, to string, body []byte, h http.Header) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, to, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	for k, v := range h {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.peering.remotes.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	var e api.Error
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return resp.StatusCode, e.Error, nil
	}
	return resp.StatusCode, resp.Status, nil
}

// sharePeerAddressIn is a peer saying where it can be reached:
// /share/peer-address.
func (s *Server) sharePeerAddressIn(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := s.sharePeerOf(w, r, addr)
	if !ok {
		return
	}
	var in api.ShareAddress
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := readJSON(r, &in); err != nil {
		writeLANError(w, http.StatusBadRequest, err.Error())
		return
	}
	urls, err := shareURLList(in.URLs)
	if err != nil {
		writeLANError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !slices.Equal(urls, p.URLs) {
		if err := s.store.SetSharePeerURLs(r.Context(), p.ID, urls); err != nil {
			writeLANError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.logf("share: %s (%s) can be reached at %s", p.Name, p.ID, cmp.Or(strings.Join(urls, ", "), "no address"))
		s.peering.addrs.soon("peer/" + p.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// shareHelloIn shows a peer that this is its owner, before it sends its
// token here: /share/hello signs the peer's nonce with the pairing's key.
func (s *Server) shareHelloIn(w http.ResponseWriter, r *http.Request, addr string) {
	if s.lan.limiter.Blocked(addr, false) {
		s.tooMany(w)
		return
	}
	nonce := r.Header.Get(headerShareNonce)
	if len(nonce) < 16 || len(nonce) > 128 {
		writeLANError(w, http.StatusBadRequest, "a hello needs a nonce")
		return
	}
	key, err := s.store.SharePeerKey(r.Context(), r.Header.Get(headerSharePeer))
	if errors.Is(err, state.ErrNotFound) {
		s.lan.limiter.Fail(addr)
		writeLANError(w, http.StatusNotFound, "no AgentBox is paired with this one as that")
		return
	} else if err != nil {
		writeLANError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = writeJSON(w, http.StatusOK, api.ShareHello{Proof: shareMAC(key, "hello", nonce)})
}

// shareHello makes sure the owner of c answers at base.
func (s *Server) shareHello(ctx context.Context, c *remoteConn, base string) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	nonce := hex.EncodeToString(raw)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/share/hello", nil)
	if err != nil {
		return err
	}
	req.Header.Set(headerSharePeer, sharePeerIDOf(c.token))
	req.Header.Set(headerShareNonce, nonce)
	resp, err := s.peering.remotes.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var hello api.ShareHello
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&hello) != nil {
		return fmt.Errorf("%s isn't %s: it answered %s", base, c.r.Name, resp.Status)
	}
	if !shareMACOK(state.HashPhoneToken(c.token), "hello", hello.Proof, nonce) {
		return fmt.Errorf("%s isn't %s", base, c.r.Name)
	}
	return nil
}

// remoteConnByPeer is the remote whose pairing has the id peer on its owner.
func (s *Server) remoteConnByPeer(peer string) *remoteConn {
	if peer == "" {
		return nil
	}
	for _, c := range s.remoteConns() {
		if sharePeerIDOf(c.token) == peer {
			return c
		}
	}
	return nil
}

// shareOwnerAddressIn is an owner telling this machine its new addresses:
// /share/owner-address, signed with the pairing's key. It answers once this
// machine reaches the owner at one of them, or says it can't (502), and the
// owner tries again later.
func (s *Server) shareOwnerAddressIn(w http.ResponseWriter, r *http.Request, addr string) {
	if s.lan.limiter.Blocked(addr, false) {
		s.tooMany(w)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		writeLANError(w, http.StatusBadRequest, err.Error())
		return
	}
	var in api.ShareAddress
	if err := json.Unmarshal(body, &in); err != nil {
		writeLANError(w, http.StatusBadRequest, err.Error())
		return
	}
	c := s.remoteConnByPeer(in.Peer)
	if c == nil {
		s.lan.limiter.Fail(addr)
		writeLANError(w, http.StatusNotFound, "this AgentBox isn't paired with that one")
		return
	}
	if !shareMACOK(state.HashPhoneToken(c.token), "address", r.Header.Get(headerShareProof), string(body)) {
		s.lan.limiter.Fail(addr)
		s.logf("share: an address for %s from %s that %s didn't sign", c.r.Name, addr, c.r.Name)
		writeLANError(w, http.StatusUnauthorized, "that address isn't signed by the pairing")
		return
	}
	a := s.peering.addrs
	a.mu.Lock()
	stale := in.At.Before(time.Now().Add(-shareSkew)) || in.At.After(time.Now().Add(shareSkew)) || !in.At.After(a.heard[c.r.ID])
	if !stale {
		a.heard[c.r.ID] = in.At
	}
	a.mu.Unlock()
	if stale {
		writeLANError(w, http.StatusConflict, "that address is older than the last one, or than this machine's clock allows")
		return
	}
	urls, err := shareURLList(in.URLs)
	if err != nil {
		writeLANError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.findRemoteAt(r.Context(), c, urls, false, "it said so"); err != nil {
		writeLANError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// findRemoteAt moves c to the first of urls its owner answers at, unless it
// is at one of them already and still reaches it (failing says it doesn't).
func (s *Server) findRemoteAt(ctx context.Context, c *remoteConn, urls []string, failing bool, how string) error {
	current := strings.TrimRight(c.r.URL, "/")
	if !failing && slices.Contains(urls, current) {
		return nil
	}
	var errs []error
	for _, u := range urls {
		if u == current {
			continue
		}
		if err := s.shareHello(ctx, c, u); err != nil {
			errs = append(errs, err)
			continue
		}
		return s.moveRemote(ctx, c, u, how)
	}
	if len(errs) == 0 {
		return fmt.Errorf("%s has no other address", c.r.Name)
	}
	return fmt.Errorf("%s can't be reached at its new address: %w", c.r.Name, errors.Join(errs...))
}

// moveRemote has c reach its owner at base from now on, with the same token.
func (s *Server) moveRemote(ctx context.Context, c *remoteConn, base, how string) error {
	a := s.peering.addrs
	a.moving.Lock()
	defer a.moving.Unlock()
	if s.remoteConn(c.r.ID) != c {
		return nil // moved or unpaired meanwhile
	}
	if err := s.store.SetShareRemoteURL(ctx, c.r.ID, base); err != nil {
		return err
	}
	r := c.r
	r.URL = base
	s.logf("share: %s is at %s now, no longer %s (%s)", r.Name, base, c.r.URL, how)
	s.runRemote(c.parent, r)
	s.publishShare(ctx)
	s.events.publish(api.EventProject, struct{}{})
	return nil
}

// peerShareAddress answers another machine of this hub account looking for
// the owner of one of its pairings (servePeer): this machine's addresses, if
// the pairing is one of this machine's and it signed the request with the
// pairing's key.
func (s *Server) peerShareAddress(w http.ResponseWriter, r *http.Request) error {
	id, at := r.Header.Get(headerSharePeer), r.Header.Get(headerShareAt)
	key, err := s.store.SharePeerKey(r.Context(), id)
	if errors.Is(err, state.ErrNotFound) {
		return writeJSON(w, http.StatusNotFound, api.Error{Error: "this machine has no AgentBox paired as that"})
	} else if err != nil {
		return err
	}
	unix, _ := strconv.ParseInt(at, 10, 64)
	if d := time.Since(time.Unix(unix, 0)); d > shareSkew || d < -shareSkew || !shareMACOK(key, "lookup", r.Header.Get(headerShareProof), id, at) {
		return writeJSON(w, http.StatusUnauthorized, api.Error{Error: "that isn't signed by the pairing"})
	}
	urls, _ := s.shareURLs(r.Context())
	return writeJSON(w, http.StatusOK, api.ShareAddress{Peer: id, URLs: urls, At: time.Now()})
}

// ownerOnHub asks the other machines of this machine's hub account which of
// them is c's owner, and answers its addresses.
func (s *Server) ownerOnHub(ctx context.Context, c *remoteConn) ([]string, error) {
	cfg, peers, err := s.hubPeers(ctx, false)
	if err != nil {
		return nil, err
	}
	a := s.peering.addrs
	a.mu.Lock()
	hint := a.hint[c.r.ID]
	a.mu.Unlock()
	peers = slices.Clone(peers) // the cache's
	slices.SortStableFunc(peers, func(x, y hubapi.HubEnvironment) int {
		return boolRank(y.ID == hint) - boolRank(x.ID == hint)
	})
	id, key := sharePeerIDOf(c.token), state.HashPhoneToken(c.token)
	for _, m := range peers {
		if !m.Online {
			continue
		}
		at := strconv.FormatInt(time.Now().Unix(), 10)
		urls, err := s.askOwnerOnHub(ctx, cfg, m.ID, id, at, shareMAC(key, "lookup", id, at))
		if err != nil {
			continue
		}
		a.mu.Lock()
		a.hint[c.r.ID] = m.ID
		a.mu.Unlock()
		return urls, nil
	}
	return nil, fmt.Errorf("no machine of this hub account is %s", c.r.Name)
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Server) askOwnerOnHub(ctx context.Context, cfg *remoteConfig, machine, peer, at, proof string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remote.PeerURL(cfg.Hub, machine)+"/v1/peer/share-address", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set(headerSharePeer, peer)
	req.Header.Set(headerShareAt, at)
	req.Header.Set(headerShareProof, proof)
	resp, err := s.peering.remotes.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out api.ShareAddress
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&out) != nil || out.Peer != peer {
		return nil, fmt.Errorf("the machine answered %s", resp.Status)
	}
	return shareURLList(out.URLs)
}

// findRemoteAgain is a peer that couldn't reach its owner (err) looking for
// it: through the hub, when both are on one account, now and then. It says
// whether c moved, and a new connection reaches the owner now. Otherwise, when
// what answered says the owner's quick tunnel is gone, the remote shows that
// its address changed, for the user to paste a new link.
func (s *Server) findRemoteAgain(ctx context.Context, c *remoteConn, err error) bool {
	var st remoteStatus
	if errors.As(err, &st) && st.code == http.StatusUnauthorized {
		return false // unpaired, not moved
	}
	if errors.As(err, new(eventsStopped)) {
		return false // it was reached: connecting again says whether it still is
	}
	c.mu.Lock()
	due := !time.Now().Before(c.findAt)
	if due {
		c.findWait = min(max(2*c.findWait, shareFindFirst), shareFindMax)
		c.findAt = time.Now().Add(c.findWait)
	}
	c.mu.Unlock()
	if due && s.readRemoteConfig() != nil {
		if urls, err := s.ownerOnHub(ctx, c); err == nil {
			if s.findRemoteAt(ctx, c, urls, true, "its hub account said so") == nil && s.remoteConn(c.r.ID) != c {
				return true
			}
		}
	}
	gone := ""
	if s.quickTunnelGone(c, err) {
		gone = fmt.Sprintf("%s's address changed: paste a new link from %s here, and this pairing carries on as it was", c.r.Name, c.r.Name)
	}
	c.mu.Lock()
	changed := c.gone != gone
	c.gone = gone
	c.mu.Unlock()
	if changed {
		s.publishShare(ctx)
	}
	return false
}

// quickTunnelGone says err is what an owner's quick tunnel answers once it is
// gone: Cloudflare's 530, its name still resolving, or its name no longer
// known.
func (s *Server) quickTunnelGone(c *remoteConn, err error) bool {
	u, perr := url.Parse(c.r.URL)
	if perr != nil {
		return false
	}
	quick := s.peering.remotes.quick
	if quick == nil {
		quick = func(host string) bool { return strings.HasSuffix(host, ".trycloudflare.com") }
	}
	if !quick(u.Hostname()) {
		return false
	}
	var st remoteStatus
	var dns *net.DNSError
	return (errors.As(err, &st) && st.code == 530) || (errors.As(err, &dns) && dns.IsNotFound)
}

// shareAgain proves each pairing this machine has to the owner of a link it
// pairs with (SharePairRequest.Again), with the link's secret.
func (s *Server) shareAgain(secret string) []api.ShareAgain {
	var out []api.ShareAgain
	for _, c := range s.remoteConns() {
		if id := sharePeerIDOf(c.token); id != "" {
			out = append(out, api.ShareAgain{Peer: id, Proof: shareMAC(state.HashPhoneToken(c.token), "again", secret)})
		}
	}
	return out
}

// sharePairedAgain is the pairing a peer pairing again proves it has here,
// and the proof that this is its owner; ok is false for none.
func (s *Server) sharePairedAgain(ctx context.Context, req api.SharePairRequest) (p state.SharePeer, proof string, ok bool) {
	for _, again := range req.Again[:min(len(req.Again), 64)] {
		key, err := s.store.SharePeerKey(ctx, again.Peer)
		if err != nil || !shareMACOK(key, "again", again.Proof, req.Secret) {
			continue
		}
		if p, err = s.store.SharePeer(ctx, again.Peer); err != nil {
			continue
		}
		return p, shareMAC(key, "again-ok", req.Secret, p.ID), true
	}
	return state.SharePeer{}, "", false
}

// pairedAgain is the owner of a link this machine paired with again
// answering that it found one of its pairings (out.Peer): the pairing moves
// to the link's address, and keeps its token.
func (s *Server) pairedAgain(ctx context.Context, base, secret string, out api.SharePairResponse) (state.ShareRemote, error) {
	c := s.remoteConnByPeer(out.Peer)
	if c == nil || !shareMACOK(state.HashPhoneToken(c.token), "again-ok", out.Proof, secret, out.Peer) {
		return state.ShareRemote{}, errors.New("the other AgentBox answered for a pairing it can't show is its own")
	}
	r := c.r
	if strings.TrimRight(r.URL, "/") == base {
		return r, nil
	}
	if err := s.moveRemote(ctx, c, base, "a new link from it"); err != nil {
		return state.ShareRemote{}, err
	}
	r.URL = base
	return r, nil
}
