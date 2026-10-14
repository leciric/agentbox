package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// The phones paired to read and send chat messages from the local network
// (internal/daemon/lan.go). A phone is paired by scanning a QR code carrying
// a one-time secret; what it gets back is a token of its own, kept in a
// cookie. Only the token's hash is stored, so a copy of state.db pairs
// nothing, and revoking a phone is deleting its row.

// SettingLAN turns chatting from a phone on the local network on ("1"). Off
// by default: nothing listens on the network until the user asks.
const SettingLAN = "lan"

// SettingLANPort is the TCP port the phone connects to; "" is DefaultLANPort.
const SettingLANPort = "lan_port"

// DefaultLANPort is where the phone connects when nobody chose a port.
const DefaultLANPort = 7780

// SettingLANTunnel turns reaching the phone page from anywhere, through a
// Cloudflare Tunnel, on ("1"). Off by default; it only runs while SettingLAN
// is on too.
const SettingLANTunnel = "lan_tunnel"

// SettingLANTunnelToken is a named tunnel's token, sealed by internal/secrets;
// "" is a quick tunnel. SettingLANTunnelHostname is the named tunnel's public
// hostname.
const (
	SettingLANTunnelToken    = "lan_tunnel_token"
	SettingLANTunnelHostname = "lan_tunnel_hostname"
)

// DefaultLANTunnelPort is the port, on the loopback address of AgentBox's own
// machine or VM, a named tunnel reaches the daemon at: its public hostname
// points at http://localhost:7781 in Cloudflare's dashboard. A quick tunnel,
// which AgentBox points itself, takes any free port.
const DefaultLANTunnelPort = 7781

// MaxPhoneNameLen bounds a phone's name, which the phone suggests itself.
const MaxPhoneNameLen = 60

// What a phone may do through the daemon's API (internal/daemon/lan.go).
// Some things no scope allows: the daemon refuses them to every phone.
const (
	// PhoneScopeRead reads: projects, agents, chats, pull requests.
	PhoneScopeRead = "read"
	// PhoneScopeChat sends messages, cancels a turn, answers a chat's
	// permission requests and starts an agent's chat.
	PhoneScopeChat = "chat"
	// PhoneScopeAnswer answers the questions agents ask.
	PhoneScopeAnswer = "answer"
	// PhoneScopeMerge merges pull requests. Never given at pairing: the user
	// turns it on for a phone in the app on the computer.
	PhoneScopeMerge = "merge"
)

// PhoneScopes are every scope, in the order the app lists them.
var PhoneScopes = []string{PhoneScopeRead, PhoneScopeChat, PhoneScopeAnswer, PhoneScopeMerge}

// DefaultPhoneScopes are what a phone gets when it pairs.
var DefaultPhoneScopes = []string{PhoneScopeRead, PhoneScopeChat, PhoneScopeAnswer}

// Phone is a paired phone.
type Phone struct {
	ID       string
	Name     string
	Created  time.Time
	LastSeen time.Time // zero until it first connects after pairing
	LastAddr string
	Scopes   []string // in PhoneScopes' order
}

// Can says the phone has scope.
func (p Phone) Can(scope string) bool { return slices.Contains(p.Scopes, scope) }

// normalScopes is scopes in PhoneScopes' order, each once, or an error
// naming one that isn't a scope.
func normalScopes(scopes []string) ([]string, error) {
	for _, sc := range scopes {
		if !slices.Contains(PhoneScopes, sc) {
			return nil, fmt.Errorf("%q isn't a phone scope: use %s", sc, strings.Join(PhoneScopes, ", "))
		}
	}
	out := []string{}
	for _, sc := range PhoneScopes {
		if slices.Contains(scopes, sc) {
			out = append(out, sc)
		}
	}
	return out, nil
}

func parseScopes(s string) []string {
	out, _ := normalScopes(strings.FieldsFunc(s, func(r rune) bool { return r == ',' }))
	return out
}

// HashPhoneToken is what's stored of a phone's token.
func HashPhoneToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// AddPhone pairs a phone called name and returns it with its token, which
// isn't kept and can't be read again.
func (s *Store) AddPhone(ctx context.Context, name string) (Phone, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Phone"
	}
	if r := []rune(name); len(r) > MaxPhoneNameLen {
		name = string(r[:MaxPhoneNameLen])
	}
	id := make([]byte, 6)
	secret := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return Phone{}, "", err
	}
	if _, err := rand.Read(secret); err != nil {
		return Phone{}, "", err
	}
	p := Phone{ID: "phone_" + hex.EncodeToString(id), Name: name, Created: time.Now()}
	token := p.ID + "." + base64.RawURLEncoding.EncodeToString(secret)
	p.Scopes = slices.Clone(DefaultPhoneScopes)
	_, err := s.db.ExecContext(ctx, `INSERT INTO phones (id, name, token_hash, created_at, scopes) VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.Name, HashPhoneToken(token), p.Created.Unix(), strings.Join(p.Scopes, ","))
	if err != nil {
		return Phone{}, "", err
	}
	return p, token, nil
}

// PhoneByToken is the phone a token belongs to, or ErrNotFound.
func (s *Store) PhoneByToken(ctx context.Context, token string) (Phone, error) {
	if token == "" {
		return Phone{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, name, created_at, last_seen_at, last_addr, scopes FROM phones WHERE token_hash = ?`, HashPhoneToken(token))
	p, err := scanPhone(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Phone{}, ErrNotFound
	}
	return p, err
}

// Phones lists the paired phones, the most recently paired first.
func (s *Store) Phones(ctx context.Context) ([]Phone, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at, last_seen_at, last_addr, scopes FROM phones ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Phone
	for rows.Next() {
		p, err := scanPhone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SawPhone records when and from where a phone last connected.
func (s *Store) SawPhone(ctx context.Context, id, addr string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE phones SET last_seen_at = ?, last_addr = ? WHERE id = ?`, at.Unix(), addr, id)
	return err
}

// SetPhoneScopes replaces what a phone may do, and returns it as it now is.
func (s *Store) SetPhoneScopes(ctx context.Context, id string, scopes []string) (Phone, error) {
	scopes, err := normalScopes(scopes)
	if err != nil {
		return Phone{}, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE phones SET scopes = ? WHERE id = ?`, strings.Join(scopes, ","), id)
	if err != nil {
		return Phone{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Phone{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, name, created_at, last_seen_at, last_addr, scopes FROM phones WHERE id = ?`, id)
	return scanPhone(row)
}

// PhoneAuditEntry is one write a phone made, or tried to.
type PhoneAuditEntry struct {
	ID        int64
	PhoneID   string
	PhoneName string
	// Scope is the one the route needs, or "" for one no phone may use.
	Scope string
	Route string // "POST /v1/projects/p/chat/messages"
	// Status is the HTTP status it got, and Result a word for it: "ok",
	// "refused" (the phone may not), or "failed".
	Status int
	Result string
	Addr   string
	At     time.Time
}

// AddPhoneAudit appends an entry, at e.At or, when that is zero, now.
func (s *Store) AddPhoneAudit(ctx context.Context, e PhoneAuditEntry) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO phone_audit (phone_id, phone_name, scope, route, status, result, addr, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.PhoneID, e.PhoneName, e.Scope, e.Route, e.Status, e.Result, e.Addr, e.At.Unix())
	return err
}

// PhoneAudit is a phone's audit, newest first, at most limit entries, from
// before the entry before (0: from the newest).
func (s *Store) PhoneAudit(ctx context.Context, phoneID string, before int64, limit int) ([]PhoneAuditEntry, error) {
	if before <= 0 {
		before = math.MaxInt64
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, phone_id, phone_name, scope, route, status, result, addr, at FROM phone_audit
		WHERE phone_id = ? AND id < ? ORDER BY id DESC LIMIT ?`, phoneID, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []PhoneAuditEntry{}
	for rows.Next() {
		var e PhoneAuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &e.PhoneID, &e.PhoneName, &e.Scope, &e.Route, &e.Status, &e.Result, &e.Addr, &at); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// RemovePhone unpairs a phone: its token stops working at once.
func (s *Store) RemovePhone(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM phones WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanPhone(row interface{ Scan(...any) error }) (Phone, error) {
	var p Phone
	var created, seen int64
	var scopes string
	if err := row.Scan(&p.ID, &p.Name, &created, &seen, &p.LastAddr, &scopes); err != nil {
		return Phone{}, err
	}
	p.Scopes = parseScopes(scopes)
	p.Created = time.Unix(created, 0)
	if seen > 0 {
		p.LastSeen = time.Unix(seen, 0)
	}
	return p, nil
}
