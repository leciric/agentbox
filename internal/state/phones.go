package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
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

// Phone is a paired phone.
type Phone struct {
	ID       string
	Name     string
	Created  time.Time
	LastSeen time.Time // zero until it first connects after pairing
	LastAddr string
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO phones (id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		p.ID, p.Name, HashPhoneToken(token), p.Created.Unix())
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
	row := s.db.QueryRowContext(ctx, `SELECT id, name, created_at, last_seen_at, last_addr FROM phones WHERE token_hash = ?`, HashPhoneToken(token))
	p, err := scanPhone(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Phone{}, ErrNotFound
	}
	return p, err
}

// Phones lists the paired phones, the most recently paired first.
func (s *Store) Phones(ctx context.Context) ([]Phone, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at, last_seen_at, last_addr FROM phones ORDER BY created_at DESC, id`)
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
	if err := row.Scan(&p.ID, &p.Name, &created, &seen, &p.LastAddr); err != nil {
		return Phone{}, err
	}
	p.Created = time.Unix(created, 0)
	if seen > 0 {
		p.LastSeen = time.Unix(seen, 0)
	}
	return p, nil
}
