package hub

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"agentbox/hubapi"
)

// Pairing is how anything gets onto the hub without a password: a device (the
// app, the CLI, a browser, a phone) or an environment asks with POST
// /v1/pair, and gets a secret, which it keeps, and a short code, which it
// shows. A device already signed in approves the code: in a browser on
// /hub/approve, by scanning its QR code, or by typing it into the app. The
// asker polls with its secret until it gets its token.
//
// It is OAuth's device authorization grant (RFC 8628) in all but name, and
// for the same reason: what asks may have no browser, or a browser that
// isn't signed in. Pending pairings live in memory, as the tunnels do: a hub
// that restarts forgets them, and whoever asked asks again.

const (
	pairingLifetime = 10 * time.Minute
	pairingInterval = 2 * time.Second
	// Unauthenticated callers can't fill the hub's memory with pairings.
	maxPairingsPerAddress = 5
	maxPairings           = 500
)

type pairing struct {
	secretHash string
	code       string
	req        hubapi.HubPairRequest
	address    string
	created    time.Time
	expires    time.Time
	status     string
	userID     string // who approved it
}

// codeAlphabet has no vowels, so no code spells a word, and no digits, so
// none reads as another (RFC 8628, section 6.1).
const codeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

func newCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:])
}

// normalizeCode reads a code however it was typed: in lower case, without
// its dash, with spaces.
func normalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if strings.ContainsRune(codeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) != 8 {
		return ""
	}
	return s[:4] + "-" + s[4:]
}

var labelClean = regexp.MustCompile(`[\x00-\x1f\x7f]+`)

func cleanLabel(s string, max int) string {
	s = strings.TrimSpace(labelClean.ReplaceAllString(s, " "))
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// baseURL is the hub's address, for links: the one it was given, or the one
// the request came to.
func (s *Server) baseURL(r *http.Request) string {
	if s.URL != "" {
		return strings.TrimRight(s.URL, "/")
	}
	scheme := "http"
	if secure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// expirePairings drops the pairings that ran out. s.mu is held.
func (s *Server) expirePairings(now time.Time) {
	for code, p := range s.pairings {
		if !now.Before(p.expires) {
			delete(s.pairings, code)
		}
	}
}

func (s *Server) startPairing(w http.ResponseWriter, r *http.Request) error {
	var req hubapi.HubPairRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	req.Label = cleanLabel(req.Label, 100)
	req.Hostname = cleanLabel(req.Hostname, 100)
	req.Version = cleanLabel(req.Version, 40)
	switch req.Kind {
	case hubapi.PairDevice:
		req.Name = ""
	case hubapi.PairEnvironment:
		req.Name = environmentName(req.Name, req.Hostname)
	default:
		return fmt.Errorf("%q isn't a kind of pairing: use %s or %s", req.Kind, hubapi.PairDevice, hubapi.PairEnvironment)
	}
	if req.Label == "" {
		req.Label = "unnamed " + req.Kind
	}
	if !s.claimed(r.Context()) {
		return httpError{http.StatusConflict, "this hub has no owner yet: open the setup link it printed when it started (or run agentbox-hub link on its machine)"}
	}
	now := time.Now()
	secret := newToken(hubapi.PairingSecretPrefix)
	p := &pairing{secretHash: hashToken(secret), req: req, address: clientIP(r), created: now, expires: now.Add(pairingLifetime), status: hubapi.PairPending}
	s.mu.Lock()
	if s.pairings == nil {
		s.pairings = map[string]*pairing{}
	}
	s.expirePairings(now)
	n := 0
	for _, other := range s.pairings {
		if other.address == p.address {
			n++
		}
	}
	if n >= maxPairingsPerAddress || len(s.pairings) >= maxPairings {
		s.mu.Unlock()
		return httpError{http.StatusTooManyRequests, "too many pairings are waiting for approval: approve or let them expire, then try again"}
	}
	for {
		p.code = newCode()
		if s.pairings[p.code] == nil {
			break
		}
	}
	s.pairings[p.code] = p
	s.mu.Unlock()
	approve := s.baseURL(r) + hubapi.PathApprovePage + "?code=" + url.QueryEscape(p.code)
	qr, _ := QR(approve)
	s.logf("%s %q asks to pair from %s: code %s", req.Kind, req.Label, p.address, p.code)
	return writeJSON(w, http.StatusOK, hubapi.HubPairing{
		Secret: secret, Code: p.code, ApproveURL: approve, ExpiresAt: p.expires,
		Interval: int(pairingInterval / time.Second), QR: qr,
	})
}

var nameUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// environmentName makes a valid name from what an environment asked for, or
// from its hostname.
func environmentName(name, hostname string) string {
	name = strings.ToLower(cmpOr(name, hostname))
	if i := strings.IndexByte(name, '.'); i > 0 && name == strings.ToLower(hostname) {
		name = name[:i] // lint-pc.local
	}
	name = strings.Trim(nameUnsafe.ReplaceAllString(name, "-"), "-")
	if len(name) > 28 {
		name = strings.TrimRight(name[:28], "-")
	}
	if name == "" {
		return "environment"
	}
	return name
}

// pairingBySecret finds the pairing a secret belongs to. s.mu is held.
func (s *Server) pairingBySecret(secret string) *pairing {
	if !strings.HasPrefix(secret, hubapi.PairingSecretPrefix) {
		return nil
	}
	hash := hashToken(secret)
	for _, p := range s.pairings {
		if p.secretHash == hash {
			return p
		}
	}
	return nil
}

// pairToken answers whoever asked for a pairing: pending until a device
// approves it, then its token, once.
func (s *Server) pairToken(w http.ResponseWriter, r *http.Request) error {
	var req hubapi.HubPairTokenRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	now := time.Now()
	s.mu.Lock()
	p := s.pairingBySecret(req.Secret)
	if p == nil {
		s.mu.Unlock()
		return httpError{http.StatusNotFound, "no such pairing: it expired, or was already used"}
	}
	status := p.status
	if status == hubapi.PairPending && !now.Before(p.expires) {
		status = hubapi.PairExpired
	}
	if status != hubapi.PairPending {
		delete(s.pairings, p.code) // answered once, with its token or why not
	}
	s.mu.Unlock()
	if status != hubapi.PairApproved {
		return writeJSON(w, http.StatusOK, hubapi.HubPairResult{Status: status})
	}
	u, err := s.Store.UserByID(r.Context(), p.userID)
	if err != nil {
		return err
	}
	out := hubapi.HubPairResult{Status: hubapi.PairApproved}
	switch p.req.Kind {
	case hubapi.PairDevice:
		sess, token, err := s.newSession(r.Context(), u, p.req.Label)
		if err != nil {
			return err
		}
		out.Session = &hubapi.HubSession{Token: token, User: toAPIUser(u), ExpiresAt: sess.ExpiresAt}
		s.logf("device %q (code %s) signed in", p.req.Label, p.code)
	case hubapi.PairEnvironment:
		e, token, err := s.addEnvironment(r.Context(), u, p.req.Name, true)
		if err != nil {
			return err
		}
		e.Hostname, e.Version = p.req.Hostname, p.req.Version
		out.Environment = &hubapi.HubEnvironmentToken{Environment: s.toAPIEnvironment(e), Token: token}
		s.logf("environment %s (code %s) added", e.Name, p.code)
	}
	return writeJSON(w, http.StatusOK, out)
}

// pendingPairing is the pairing a code in the path names, while it can still
// be approved.
func (s *Server) pendingPairing(r *http.Request) (*pairing, error) {
	code := normalizeCode(r.PathValue("code"))
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pairings[code]
	if p == nil || !time.Now().Before(p.expires) {
		return nil, httpError{http.StatusNotFound, "no pairing has that code: it expired, or was mistyped"}
	}
	if p.status != hubapi.PairPending {
		return nil, httpError{http.StatusConflict, "that pairing was already " + p.status}
	}
	return p, nil
}

func pairingInfo(p *pairing) hubapi.HubPairingInfo {
	return hubapi.HubPairingInfo{
		Code: p.code, Kind: p.req.Kind, Label: p.req.Label, Name: p.req.Name, Hostname: p.req.Hostname,
		Version: p.req.Version, Address: p.address, CreatedAt: p.created, ExpiresAt: p.expires,
	}
}

func (s *Server) getPairing(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := s.authenticate(r); err != nil {
		return err
	}
	p, err := s.pendingPairing(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	info := pairingInfo(p)
	s.mu.Unlock()
	return writeJSON(w, http.StatusOK, info)
}

func (s *Server) decidePairing(approve bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		u, sess, err := s.authenticate(r)
		if err != nil {
			return err
		}
		p, err := s.pendingPairing(r)
		if err != nil {
			return err
		}
		s.mu.Lock()
		if p.status != hubapi.PairPending {
			s.mu.Unlock()
			return httpError{http.StatusConflict, "that pairing was already " + p.status}
		}
		p.status, p.userID = hubapi.PairDenied, u.ID
		if approve {
			p.status = hubapi.PairApproved
		}
		info := pairingInfo(p)
		s.mu.Unlock()
		verb := "denied"
		if approve {
			verb = "approved"
		}
		s.logf("%s %s the %s %q (code %s)", cmpOr(sess.Label, "a device"), verb, info.Kind, info.Label, info.Code)
		return writeJSON(w, http.StatusOK, info)
	}
}

// claimed reports whether the hub has an owner. A hub from before pairing has
// one: its first account.
func (s *Server) claimed(ctx context.Context) bool {
	n, err := s.Store.CountUsers(ctx)
	return err != nil || n > 0
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) error {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		return err
	}
	passwords, err := s.Store.CountPasswords(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, hubapi.HubInfo{Version: s.Version, Claimed: n > 0, Passwords: passwords > 0})
}

// NewSetupLink makes a one-time link, run on the hub's machine: on a hub
// with no owner, whoever opens it first becomes the owner; on one with an
// owner, it signs the owner in once, within 15 minutes, for when all their
// devices are gone. Making one invalidates the one before. base is the
// hub's address.
func NewSetupLink(ctx context.Context, store Store, base string) (link string, claim bool, err error) {
	now := time.Now()
	l := SetupLink{CreatedAt: now}
	owner, err := store.Owner(ctx)
	switch {
	case errors.Is(err, ErrNotFound):
		claim = true
	case err != nil:
		return "", false, err
	default:
		l.UserID, l.ExpiresAt = owner.ID, now.Add(15*time.Minute)
	}
	secret := newToken(hubapi.SetupSecretPrefix)
	l.TokenHash = hashToken(secret)
	if err := store.ReplaceSetupLink(ctx, l); err != nil {
		return "", false, err
	}
	return strings.TrimRight(base, "/") + hubapi.PathSetupPage + "#" + secret, claim, nil
}

// setup takes a setup link's secret: the first to send the claim link becomes
// the owner, and a sign-in link signs its user in.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) error {
	if !sameOrigin(r) {
		return errOrigin
	}
	var req hubapi.HubSetupRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if !strings.HasPrefix(req.Secret, hubapi.SetupSecretPrefix) {
		return httpError{http.StatusUnauthorized, "that isn't a setup link"}
	}
	ip := clientIP(r)
	if s.tooManyFailures(ip, false) {
		return httpError{http.StatusTooManyRequests, "too many wrong links from this address: try again in a few minutes"}
	}
	label := cmpOr(cleanLabel(req.Label, 100), "browser")
	// One claim at a time, so that two people opening the link at once
	// can't both become owners.
	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	l, err := s.Store.TakeSetupLink(r.Context(), hashToken(req.Secret), time.Now())
	if errors.Is(err, ErrNotFound) {
		s.tooManyFailures(ip, true)
		return httpError{http.StatusUnauthorized, "this setup link was already used, or replaced by a newer one: run agentbox-hub link on the hub's machine for another"}
	} else if err != nil {
		return err
	}
	var u User
	if l.UserID == "" {
		if s.claimed(r.Context()) {
			return httpError{http.StatusConflict, "this hub already has an owner"}
		}
		u = User{ID: newID("usr_"), Name: "Owner", CreatedAt: time.Now()}
		if err := s.Store.CreateUser(r.Context(), u); err != nil {
			return err
		}
		s.logf("%s from %s claimed the hub, and is its owner", label, ip)
	} else {
		if u, err = s.Store.UserByID(r.Context(), l.UserID); err != nil {
			return err
		}
		s.logf("%s from %s signed in with a sign-in link", label, ip)
	}
	return s.startSession(w, r, u, label)
}

// newSession makes a device for a user, and its token.
func (s *Server) newSession(ctx context.Context, u User, label string) (Session, string, error) {
	token := newToken(sessionPrefix)
	now := time.Now()
	sess := Session{ID: newID("dev_"), TokenHash: hashToken(token), UserID: u.ID, Label: label, CreatedAt: now, ExpiresAt: now.Add(sessionLifetime), LastSeenAt: now}
	return sess, token, s.Store.CreateSession(ctx, sess)
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) error {
	u, current, err := s.authenticate(r)
	if err != nil {
		return err
	}
	sessions, err := s.Store.Sessions(r.Context(), u.ID, time.Now())
	if err != nil {
		return err
	}
	out := make([]hubapi.HubDevice, 0, len(sessions))
	for _, x := range sessions {
		out = append(out, hubapi.HubDevice{ID: x.ID, Label: cmpOr(x.Label, "unnamed device"), CreatedAt: x.CreatedAt, LastSeenAt: x.LastSeenAt, Current: x.TokenHash == current.TokenHash})
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) error {
	u, sess, err := s.authenticate(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteSessionByID(r.Context(), u.ID, r.PathValue("id")); errors.Is(err, ErrNotFound) {
		return httpError{http.StatusNotFound, "no such device"}
	} else if err != nil {
		return err
	}
	s.logf("%s revoked the device %s", cmpOr(sess.Label, "a device"), r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
	return nil
}
