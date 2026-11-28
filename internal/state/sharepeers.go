package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Sharing projects with another AgentBox (internal/daemon/share.go). The
// owner pairs a peer with a one-time link, as a phone is paired; the peer
// gets a token of its own, of which only the hash is kept here. Each peer
// reaches the projects it was given, all of them or a list, and may have so
// many agents running on this machine (its slots).
//
// The other side of it, an AgentBox that paired with an owner, keeps the
// owner's address and the token it got (ShareRemote), sealed, to proxy the
// shared projects to its own app.

// SettingShareSlots is how many agents a peer may have running on this
// machine when it has no limit of its own; "" is DefaultShareSlots.
const SettingShareSlots = "share_slots"

// SettingShareName is what this AgentBox is called on the others; "" is
// the computer's name.
const SettingShareName = "share_name"

// DefaultShareSlots is SettingShareSlots' default.
const DefaultShareSlots = 2

// MaxShareSlots bounds a slot limit.
const MaxShareSlots = 64

// SharePeer is another AgentBox paired to reach some of this machine's
// projects.
type SharePeer struct {
	ID       string
	Name     string
	Created  time.Time
	LastSeen time.Time
	LastAddr string
	// AllProjects shares every project of this machine, those added later
	// included, but never one another AgentBox shares with it; otherwise
	// Projects, by slug.
	AllProjects bool
	Projects    []string
	// Slots is how many agents it may have running here; -1 follows
	// SettingShareSlots.
	Slots int
	// Access is what it may do in the projects it reaches: ShareAccessView,
	// ShareAccessChat or ShareAccessFull.
	Access string
	// OwnMachine lets it run the agents a project's chat makes for it on its
	// own computer, with its own AI accounts and GitHub access, instead of
	// here (internal/daemon/sharemachines.go).
	OwnMachine bool
	// URLs are where it said it can be reached, its likeliest first: where
	// this machine tells it its own new address (internal/daemon/shareaddress.go).
	// None when it has no address of its own, or paired before it could say.
	URLs []string
	// Sync lets it sync with this machine (internal/daemon/sync.go): copy
	// the data of the projects it reaches, merge its own into them, and add
	// its own projects here.
	Sync bool
}

// What a peer may do in the projects it reaches, each level all the one
// before it may and more (api.ShareAccessView and the rest).
const (
	ShareAccessView = "view"
	ShareAccessChat = "chat"
	ShareAccessFull = "full"
)

// ShareAccessRank orders the levels, "" (unknown) lowest.
func ShareAccessRank(access string) int {
	switch access {
	case ShareAccessView:
		return 1
	case ShareAccessChat:
		return 2
	case ShareAccessFull:
		return 3
	}
	return 0
}

// May says p's access reaches need.
func (p SharePeer) May(need string) bool {
	return ShareAccessRank(p.Access) >= ShareAccessRank(need)
}

// RemoteProjectSep is in the name of a project another AgentBox shares with
// this one, between that AgentBox and its project ("r1a2b3~web",
// internal/daemon/shareremote.go). No project's own slug has it.
const RemoteProjectSep = "~"

// Shares says p reaches the project called slug: one of this machine's own.
// A project another AgentBox shares with this one is never passed on, which
// with two AgentBoxes sharing everything with each other would send each
// one's events back to it, renamed, without end.
func (p SharePeer) Shares(slug string) bool {
	if slug == "" || slug == HomeProject || strings.Contains(slug, RemoteProjectSep) {
		return false
	}
	return p.AllProjects || slices.Contains(p.Projects, slug)
}

const sharePeerSelect = `SELECT id, name, created_at, last_seen_at, last_addr, all_projects, projects, slots, own_machine, access, urls, sync FROM share_peers`

func scanSharePeer(row interface{ Scan(...any) error }) (SharePeer, error) {
	var p SharePeer
	var created, seen int64
	var all, own, sync int
	var projects, urls string
	if err := row.Scan(&p.ID, &p.Name, &created, &seen, &p.LastAddr, &all, &projects, &p.Slots, &own, &p.Access, &urls, &sync); err != nil {
		return SharePeer{}, err
	}
	p.Created = time.Unix(created, 0)
	if seen > 0 {
		p.LastSeen = time.Unix(seen, 0)
	}
	p.AllProjects = all != 0
	p.OwnMachine = own != 0
	p.Sync = sync != 0
	p.Projects = []string{}
	for _, s := range strings.Split(projects, ",") {
		if s = strings.TrimSpace(s); s != "" {
			p.Projects = append(p.Projects, s)
		}
	}
	p.URLs = strings.Fields(urls)
	return p, nil
}

// AddSharePeer pairs another AgentBox called name, sharing nothing until the
// owner chooses, and returns it with its token, which isn't kept.
func (s *Store) AddSharePeer(ctx context.Context, name string) (SharePeer, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "AgentBox"
	}
	if r := []rune(name); len(r) > MaxPhoneNameLen {
		name = string(r[:MaxPhoneNameLen])
	}
	id := make([]byte, 6)
	secret := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return SharePeer{}, "", err
	}
	if _, err := rand.Read(secret); err != nil {
		return SharePeer{}, "", err
	}
	p := SharePeer{ID: "peer_" + hex.EncodeToString(id), Name: name, Created: time.Now(), Projects: []string{}, Slots: -1, OwnMachine: true, Access: ShareAccessFull}
	token := "share_" + p.ID + "." + base64.RawURLEncoding.EncodeToString(secret)
	_, err := s.db.ExecContext(ctx, `INSERT INTO share_peers (id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		p.ID, p.Name, HashPhoneToken(token), p.Created.Unix())
	if err != nil {
		return SharePeer{}, "", err
	}
	return p, token, nil
}

// SharePeerByToken is the peer a token belongs to, or ErrNotFound.
func (s *Store) SharePeerByToken(ctx context.Context, token string) (SharePeer, error) {
	if token == "" {
		return SharePeer{}, ErrNotFound
	}
	p, err := scanSharePeer(s.db.QueryRowContext(ctx, sharePeerSelect+` WHERE token_hash = ?`, HashPhoneToken(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return SharePeer{}, ErrNotFound
	}
	return p, err
}

// SharePeer is one peer, or ErrNotFound.
func (s *Store) SharePeer(ctx context.Context, id string) (SharePeer, error) {
	p, err := scanSharePeer(s.db.QueryRowContext(ctx, sharePeerSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SharePeer{}, ErrNotFound
	}
	return p, err
}

// SharePeers lists the paired peers, the most recently paired first.
func (s *Store) SharePeers(ctx context.Context) ([]SharePeer, error) {
	rows, err := s.db.QueryContext(ctx, sharePeerSelect+` ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SharePeer
	for rows.Next() {
		p, err := scanSharePeer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetSharePeer replaces what a peer reaches and its slots (-1 for the
// default), and returns it as it now is.
func (s *Store) SetSharePeer(ctx context.Context, id string, all bool, projects []string, slots int) (SharePeer, error) {
	if slots < -1 || slots > MaxShareSlots {
		return SharePeer{}, errors.New("a peer's slots must be between 0 and " + strconv.Itoa(MaxShareSlots))
	}
	var clean []string
	for _, p := range projects {
		if p = strings.TrimSpace(p); p != "" && !strings.Contains(p, ",") && !slices.Contains(clean, p) {
			clean = append(clean, p)
		}
	}
	res, err := s.db.ExecContext(ctx, `UPDATE share_peers SET all_projects = ?, projects = ?, slots = ? WHERE id = ?`,
		boolInt(all), strings.Join(clean, ","), slots, id)
	if err != nil {
		return SharePeer{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return SharePeer{}, ErrNotFound
	}
	return s.SharePeer(ctx, id)
}

// SetSharePeerAccess sets what a peer may do in the projects it reaches.
func (s *Store) SetSharePeerAccess(ctx context.Context, id, access string) error {
	if ShareAccessRank(access) == 0 {
		return errors.New("access must be view, chat or full, not " + strconv.Quote(access))
	}
	res, err := s.db.ExecContext(ctx, `UPDATE share_peers SET access = ? WHERE id = ?`, access, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSharePeerSync says whether a peer may sync with this machine, and
// returns it as it now is.
func (s *Store) SetSharePeerSync(ctx context.Context, id string, on bool) (SharePeer, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE share_peers SET sync = ? WHERE id = ?`, boolInt(on), id)
	if err != nil {
		return SharePeer{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return SharePeer{}, ErrNotFound
	}
	return s.SharePeer(ctx, id)
}

// SetSharePeerOwnMachine says whether a peer may run agents on its own
// computer, and returns it as it now is.
func (s *Store) SetSharePeerOwnMachine(ctx context.Context, id string, on bool) (SharePeer, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE share_peers SET own_machine = ? WHERE id = ?`, boolInt(on), id)
	if err != nil {
		return SharePeer{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return SharePeer{}, ErrNotFound
	}
	return s.SharePeer(ctx, id)
}

// SawSharePeer records when and from where a peer last connected.
func (s *Store) SawSharePeer(ctx context.Context, id, addr string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE share_peers SET last_seen_at = ?, last_addr = ? WHERE id = ?`, at.Unix(), addr, id)
	return err
}

// SetSharePeerURLs keeps where a peer said it can be reached.
func (s *Store) SetSharePeerURLs(ctx context.Context, id string, urls []string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE share_peers SET urls = ? WHERE id = ?`, strings.Join(urls, " "), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SharePeerKey is the hash of a peer's token (HashPhoneToken), or
// ErrNotFound: what a message between the owner and the peer is signed with,
// which the peer works out from the token it keeps.
func (s *Store) SharePeerKey(ctx context.Context, id string) (string, error) {
	var key string
	err := s.db.QueryRowContext(ctx, `SELECT token_hash FROM share_peers WHERE id = ?`, id).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return key, err
}

// RemoveSharePeer revokes a peer: its token stops working at once.
func (s *Store) RemoveSharePeer(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM share_peer_agents WHERE peer_id = ?`, id); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM share_activity WHERE peer_id = ?`, id); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM share_peers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddSharePeerAgent records that a peer made one of this machine's agents.
func (s *Store) AddSharePeerAgent(ctx context.Context, peer, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO share_peer_agents (peer_id, project, agent, created_at) VALUES (?, ?, ?, ?)`,
		peer, project, agent, time.Now().Unix())
	return err
}

// SharePeerOfAgent is the paired AgentBox that asked for one of this
// machine's agents (AddSharePeerAgent): ErrNotFound when none did.
func (s *Store) SharePeerOfAgent(ctx context.Context, project, agent string) (string, error) {
	var peer string
	err := s.db.QueryRowContext(ctx, `SELECT peer_id FROM share_peer_agents WHERE project = ? AND agent = ? ORDER BY created_at DESC LIMIT 1`,
		project, agent).Scan(&peer)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return peer, err
}

// SharePeerAgents are the refs ("project/agent") of the agents a peer made
// that this machine still has.
func (s *Store) SharePeerAgents(ctx context.Context, peer string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pa.project, pa.agent FROM share_peer_agents pa
		JOIN agents a ON a.project = pa.project AND a.name = pa.agent
		WHERE pa.peer_id = ? ORDER BY pa.created_at, pa.agent`, peer)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var project, agent string
		if err := rows.Scan(&project, &agent); err != nil {
			return nil, err
		}
		out = append(out, project+"/"+agent)
	}
	return out, rows.Err()
}

// ShareRemote is another AgentBox this one paired with, which shares some
// of its projects with it.
type ShareRemote struct {
	ID string
	// Name is what the owner calls its AgentBox.
	Name string
	// URL is where its share gateway is: its phone page's address.
	URL     string
	Token   []byte // sealed by internal/secrets
	Created time.Time
}

// AddShareRemote keeps a pairing with an owner.
func (s *Store) AddShareRemote(ctx context.Context, r ShareRemote) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO share_remotes (id, name, url, token, created_at) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.URL, r.Token, r.Created.Unix())
	return err
}

// ShareRemotes lists the pairings with owners, the oldest first.
func (s *Store) ShareRemotes(ctx context.Context) ([]ShareRemote, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, url, token, created_at FROM share_remotes ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ShareRemote
	for rows.Next() {
		var r ShareRemote
		var created int64
		if err := rows.Scan(&r.ID, &r.Name, &r.URL, &r.Token, &created); err != nil {
			return nil, err
		}
		r.Created = time.Unix(created, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetShareRemoteURL moves a pairing with an owner to the owner's new
// address, keeping its token.
func (s *Store) SetShareRemoteURL(ctx context.Context, id, url string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE share_remotes SET url = ? WHERE id = ?`, url, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveShareRemote forgets a pairing with an owner.
func (s *Store) RemoveShareRemote(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM share_remotes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
