package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// A paired phone's push notifications (internal/daemon/phonepush.go): the
// Expo push token the phone app registered, which kinds it wants and when it
// wants none. One row per phone at most, deleted with the phone. Token is
// empty while the phone has chosen but has no token: a build of the app
// with no EAS project, or one Expo gave up on.

// PhonePush is what one phone gets pushed.
type PhonePush struct {
	PhoneID   string
	Token     string
	Kinds     []string
	QuietFrom int // minutes after midnight in TimeZone; equal to QuietTo is none
	QuietTo   int
	TimeZone  string
	Updated   time.Time
	// Phone is the phone it belongs to, as PhonePushes reads it.
	Phone Phone
}

// SetPhonePush registers or replaces a phone's push notifications.
func (s *Store) SetPhonePush(ctx context.Context, p PhonePush) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO phone_push (phone_id, token, kinds, quiet_from, quiet_to, time_zone, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (phone_id) DO UPDATE SET token = excluded.token, kinds = excluded.kinds, quiet_from = excluded.quiet_from,
			quiet_to = excluded.quiet_to, time_zone = excluded.time_zone, updated_at = excluded.updated_at`,
		p.PhoneID, p.Token, strings.Join(p.Kinds, ","), p.QuietFrom, p.QuietTo, p.TimeZone, time.Now().Unix())
	return err
}

// PhonePushOf is a phone's push notifications, or ErrNotFound when it has
// registered for none.
func (s *Store) PhonePushOf(ctx context.Context, phoneID string) (PhonePush, error) {
	row := s.db.QueryRowContext(ctx, `SELECT phone_id, token, kinds, quiet_from, quiet_to, time_zone, updated_at FROM phone_push WHERE phone_id = ?`, phoneID)
	var p PhonePush
	var kinds string
	var updated int64
	err := row.Scan(&p.PhoneID, &p.Token, &kinds, &p.QuietFrom, &p.QuietTo, &p.TimeZone, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return PhonePush{}, ErrNotFound
	}
	p.Kinds, p.Updated = splitKinds(kinds), time.Unix(updated, 0)
	return p, err
}

// PhonePushes is every phone registered for push notifications, with the
// phone itself, whose scopes say whether it may read what it would be sent.
func (s *Store) PhonePushes(ctx context.Context) ([]PhonePush, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pp.phone_id, pp.token, pp.kinds, pp.quiet_from, pp.quiet_to, pp.time_zone, pp.updated_at,
			p.id, p.name, p.created_at, p.last_seen_at, p.last_addr, p.scopes
		FROM phone_push pp JOIN phones p ON p.id = pp.phone_id ORDER BY p.created_at, p.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PhonePush
	for rows.Next() {
		var p PhonePush
		var kinds, scopes string
		var updated, created, seen int64
		if err := rows.Scan(&p.PhoneID, &p.Token, &kinds, &p.QuietFrom, &p.QuietTo, &p.TimeZone, &updated,
			&p.Phone.ID, &p.Phone.Name, &created, &seen, &p.Phone.LastAddr, &scopes); err != nil {
			return nil, err
		}
		p.Kinds, p.Updated = splitKinds(kinds), time.Unix(updated, 0)
		p.Phone.Scopes, p.Phone.Created = parseScopes(scopes), time.Unix(created, 0)
		if seen > 0 {
			p.Phone.LastSeen = time.Unix(seen, 0)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RemovePhonePush stops a phone's push notifications.
func (s *Store) RemovePhonePush(ctx context.Context, phoneID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM phone_push WHERE phone_id = ?`, phoneID)
	return err
}

// DropPushToken forgets a push token the push service says no longer
// reaches a phone: the app was uninstalled, or its token replaced. What the
// phone chose stays for the token it registers next.
func (s *Store) DropPushToken(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE phone_push SET token = '' WHERE token = ?`, token)
	return err
}

func splitKinds(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' })
}
