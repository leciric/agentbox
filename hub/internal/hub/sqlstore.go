package hub

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	_ "modernc.org/sqlite"             // registers "sqlite"
)

// SQLStore keeps the hub's data in Postgres or SQLite. The SQL is the same for
// both: text IDs, times as Unix milliseconds, and ? placeholders, rewritten to
// $1, $2… for Postgres.
type SQLStore struct {
	db       *sql.DB
	postgres bool
}

// OpenStore opens the database in dsn and brings its schema up to date:
// postgres://… (or postgresql://…) for Postgres, or sqlite:<path> for SQLite.
func OpenStore(ctx context.Context, dsn string) (*SQLStore, error) {
	s := &SQLStore{}
	var err error
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		s.postgres = true
		s.db, err = sql.Open("pgx", dsn)
	case strings.HasPrefix(dsn, "sqlite:"):
		path := strings.TrimPrefix(dsn, "sqlite:")
		s.db, err = sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
		s.db.SetMaxOpenConns(1)
	default:
		return nil, fmt.Errorf("unsupported database %q: use postgres://… or sqlite:<path>", redact(dsn))
	}
	if err != nil {
		return nil, err
	}
	if err := s.db.PingContext(ctx); err != nil {
		_ = s.db.Close()
		return nil, fmt.Errorf("connecting to %s: %w", redact(dsn), err)
	}
	if err := s.migrate(ctx); err != nil {
		_ = s.db.Close()
		return nil, err
	}
	return s, nil
}

// redact hides a password in a database URL.
func redact(dsn string) string {
	scheme, rest, ok := strings.Cut(dsn, "://")
	if !ok {
		return dsn
	}
	userinfo, host, ok := strings.Cut(rest, "@")
	if !ok {
		return dsn
	}
	user, _, _ := strings.Cut(userinfo, ":")
	return scheme + "://" + user + ":***@" + host
}

// migrations are applied in order, once each. Append; never edit one that has shipped.
var migrations = []string{
	`CREATE TABLE users (
		id TEXT PRIMARY KEY,
		email TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		created_at BIGINT NOT NULL
	);
	CREATE TABLE sessions (
		token_hash TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		label TEXT NOT NULL,
		created_at BIGINT NOT NULL,
		expires_at BIGINT NOT NULL
	);
	CREATE INDEX sessions_user ON sessions(user_id);
	CREATE TABLE environments (
		id TEXT PRIMARY KEY,
		owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		created_at BIGINT NOT NULL,
		last_seen_at BIGINT NOT NULL,
		version TEXT NOT NULL,
		hostname TEXT NOT NULL,
		UNIQUE (owner_id, name)
	);`,
	// No foreign key: a deleted environment's tunnel stays until it's removed
	// from Cloudflare.
	`CREATE TABLE environment_tunnels (
		environment_id TEXT PRIMARY KEY,
		hostname TEXT NOT NULL UNIQUE,
		tunnel_id TEXT NOT NULL,
		dns_record_id TEXT NOT NULL,
		created_at BIGINT NOT NULL,
		requested_at BIGINT NOT NULL
	);`,
	// Pairing: sessions become devices, with an ID to revoke them by and when
	// they were last used; setup links claim the hub, or sign its owner in.
	`ALTER TABLE sessions ADD COLUMN id TEXT NOT NULL DEFAULT '';
	ALTER TABLE sessions ADD COLUMN last_seen_at BIGINT NOT NULL DEFAULT 0;
	UPDATE sessions SET id = 'dev_' || substr(token_hash, 1, 24);
	CREATE UNIQUE INDEX sessions_id ON sessions(id);
	CREATE TABLE setup_links (
		token_hash TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		created_at BIGINT NOT NULL,
		expires_at BIGINT NOT NULL
	);`,
	// Tailscale, the second transport: '' is Cloudflare.
	`ALTER TABLE environment_tunnels ADD COLUMN transport TEXT NOT NULL DEFAULT '';
	ALTER TABLE environment_tunnels ADD COLUMN address TEXT NOT NULL DEFAULT '';`,
}

func (s *SQLStore) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}
	var applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		return err
	}
	for i := applied; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, stmt := range strings.Split(migrations[i], ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO schema_migrations (version) VALUES (?)`), i+1); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// rebind turns ? placeholders into Postgres's $1, $2…
func (s *SQLStore) rebind(query string) string {
	if !s.postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *SQLStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := s.db.ExecContext(ctx, s.rebind(query), args...)
	if err != nil && isUniqueViolation(err) {
		return nil, ErrExists
	}
	return res, err
}

func (s *SQLStore) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.rebind(query), args...)
}

func isUniqueViolation(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "SQLSTATE 23505")
}

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *SQLStore) Close() error { return s.db.Close() }

func (s *SQLStore) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *SQLStore) CountPasswords(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, `SELECT COUNT(*) FROM users WHERE password_hash <> ''`).Scan(&n)
	return n, err
}

func (s *SQLStore) Owner(ctx context.Context) (User, error) {
	return s.scanUser(s.queryRow(ctx, `SELECT id, email, name, password_hash, created_at FROM users ORDER BY created_at, id LIMIT 1`))
}

// An account without an email keeps its ID in the column, which is unique and
// can't be mistaken for an email: there's no @ in it.
func (s *SQLStore) CreateUser(ctx context.Context, u User) error {
	email := strings.ToLower(u.Email)
	if email == "" {
		email = u.ID
	}
	_, err := s.exec(ctx, `INSERT INTO users (id, email, name, password_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.ID, email, u.Name, u.PasswordHash, millis(u.CreatedAt))
	return err
}

func (s *SQLStore) scanUser(row *sql.Row) (User, error) {
	var u User
	var created int64
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &created); err != nil {
		return User{}, notFound(err)
	}
	if u.Email == u.ID {
		u.Email = ""
	}
	u.CreatedAt = fromMillis(created)
	return u, nil
}

func (s *SQLStore) UserByEmail(ctx context.Context, email string) (User, error) {
	if !strings.Contains(email, "@") {
		return User{}, ErrNotFound
	}
	return s.scanUser(s.queryRow(ctx, `SELECT id, email, name, password_hash, created_at FROM users WHERE email = ?`, strings.ToLower(email)))
}

func (s *SQLStore) UserByID(ctx context.Context, id string) (User, error) {
	return s.scanUser(s.queryRow(ctx, `SELECT id, email, name, password_hash, created_at FROM users WHERE id = ?`, id))
}

func (s *SQLStore) CreateSession(ctx context.Context, x Session) error {
	_, err := s.exec(ctx, `INSERT INTO sessions (id, token_hash, user_id, label, created_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		x.ID, x.TokenHash, x.UserID, x.Label, millis(x.CreatedAt), millis(x.ExpiresAt), millis(x.LastSeenAt))
	return err
}

const sessionColumns = `id, token_hash, user_id, label, created_at, expires_at, last_seen_at`

func scanSession(row scanner) (Session, error) {
	var x Session
	var created, expires, seen int64
	if err := row.Scan(&x.ID, &x.TokenHash, &x.UserID, &x.Label, &created, &expires, &seen); err != nil {
		return Session{}, notFound(err)
	}
	x.CreatedAt, x.ExpiresAt, x.LastSeenAt = fromMillis(created), fromMillis(expires), fromMillis(seen)
	return x, nil
}

func (s *SQLStore) Session(ctx context.Context, tokenHash string, now time.Time) (Session, error) {
	return scanSession(s.queryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE token_hash = ? AND expires_at > ?`, tokenHash, millis(now)))
}

func (s *SQLStore) Sessions(ctx context.Context, userID string, now time.Time) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`SELECT `+sessionColumns+` FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen_at DESC, created_at DESC`), userID, millis(now))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Session{}
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *SQLStore) DeleteSessionByID(ctx context.Context, userID, id string) error {
	res, err := s.exec(ctx, `DELETE FROM sessions WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) TouchSession(ctx context.Context, tokenHash string, seen, expires time.Time) error {
	_, err := s.exec(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?`, millis(seen), millis(expires), tokenHash)
	return err
}

func (s *SQLStore) ReplaceSetupLink(ctx context.Context, l SetupLink) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM setup_links WHERE user_id = ?`), l.UserID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO setup_links (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`),
		l.TokenHash, l.UserID, millis(l.CreatedAt), millis(l.ExpiresAt)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) TakeSetupLink(ctx context.Context, tokenHash string, now time.Time) (SetupLink, error) {
	var l SetupLink
	var created, expires int64
	err := s.queryRow(ctx, `DELETE FROM setup_links WHERE token_hash = ? RETURNING token_hash, user_id, created_at, expires_at`, tokenHash).
		Scan(&l.TokenHash, &l.UserID, &created, &expires)
	if err != nil {
		return SetupLink{}, notFound(err)
	}
	l.CreatedAt, l.ExpiresAt = fromMillis(created), fromMillis(expires)
	if !l.ExpiresAt.IsZero() && !now.Before(l.ExpiresAt) {
		return SetupLink{}, ErrNotFound
	}
	return l, nil
}

func (s *SQLStore) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.exec(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *SQLStore) DeleteSessions(ctx context.Context, userID string) error {
	_, err := s.exec(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *SQLStore) SetPassword(ctx context.Context, userID, passwordHash string) error {
	res, err := s.exec(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) CreateEnvironment(ctx context.Context, e Environment) error {
	_, err := s.exec(ctx, `INSERT INTO environments (id, owner_id, name, token_hash, created_at, last_seen_at, version, hostname) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.OwnerID, e.Name, e.TokenHash, millis(e.CreatedAt), millis(e.LastSeenAt), e.Version, e.Hostname)
	return err
}

const environmentColumns = `id, owner_id, name, token_hash, created_at, last_seen_at, version, hostname`

type scanner interface{ Scan(dest ...any) error }

func scanEnvironment(row scanner) (Environment, error) {
	var e Environment
	var created, seen int64
	if err := row.Scan(&e.ID, &e.OwnerID, &e.Name, &e.TokenHash, &created, &seen, &e.Version, &e.Hostname); err != nil {
		return Environment{}, notFound(err)
	}
	e.CreatedAt, e.LastSeenAt = fromMillis(created), fromMillis(seen)
	return e, nil
}

func (s *SQLStore) Environments(ctx context.Context, ownerID string) ([]Environment, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`SELECT `+environmentColumns+` FROM environments WHERE owner_id = ? ORDER BY name`), ownerID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	envs := []Environment{}
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		envs = append(envs, e)
	}
	return envs, rows.Err()
}

func (s *SQLStore) Environment(ctx context.Context, id string) (Environment, error) {
	return scanEnvironment(s.queryRow(ctx, `SELECT `+environmentColumns+` FROM environments WHERE id = ?`, id))
}

func (s *SQLStore) EnvironmentByToken(ctx context.Context, tokenHash string) (Environment, error) {
	return scanEnvironment(s.queryRow(ctx, `SELECT `+environmentColumns+` FROM environments WHERE token_hash = ?`, tokenHash))
}

func (s *SQLStore) SeenEnvironment(ctx context.Context, id string, seen time.Time, version, hostname string) error {
	_, err := s.exec(ctx, `UPDATE environments SET last_seen_at = ?, version = ?, hostname = ? WHERE id = ?`, millis(seen), version, hostname, id)
	return err
}

func (s *SQLStore) DeleteEnvironment(ctx context.Context, id string) error {
	res, err := s.exec(ctx, `DELETE FROM environments WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) SaveEnvironmentTunnel(ctx context.Context, t EnvironmentTunnel) error {
	_, err := s.exec(ctx, `INSERT INTO environment_tunnels (environment_id, transport, hostname, tunnel_id, dns_record_id, address, created_at, requested_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (environment_id) DO UPDATE SET transport = excluded.transport, hostname = excluded.hostname, tunnel_id = excluded.tunnel_id,
			dns_record_id = excluded.dns_record_id, address = excluded.address, created_at = excluded.created_at, requested_at = excluded.requested_at`,
		t.EnvironmentID, t.Transport, t.Hostname, t.TunnelID, t.DNSRecordID, t.Address, millis(t.CreatedAt), millis(t.RequestedAt))
	return err
}

const environmentTunnelColumns = `environment_id, transport, hostname, tunnel_id, dns_record_id, address, created_at, requested_at`

func scanEnvironmentTunnel(row scanner) (EnvironmentTunnel, error) {
	var t EnvironmentTunnel
	var created, requested int64
	if err := row.Scan(&t.EnvironmentID, &t.Transport, &t.Hostname, &t.TunnelID, &t.DNSRecordID, &t.Address, &created, &requested); err != nil {
		return EnvironmentTunnel{}, notFound(err)
	}
	t.CreatedAt, t.RequestedAt = fromMillis(created), fromMillis(requested)
	return t, nil
}

func (s *SQLStore) EnvironmentTunnel(ctx context.Context, environmentID string) (EnvironmentTunnel, error) {
	return scanEnvironmentTunnel(s.queryRow(ctx, `SELECT `+environmentTunnelColumns+` FROM environment_tunnels WHERE environment_id = ?`, environmentID))
}

func (s *SQLStore) EnvironmentTunnels(ctx context.Context) ([]EnvironmentTunnel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+environmentTunnelColumns+` FROM environment_tunnels ORDER BY environment_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []EnvironmentTunnel{}
	for rows.Next() {
		t, err := scanEnvironmentTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *SQLStore) TunnelRequested(ctx context.Context, environmentID string, at time.Time) error {
	_, err := s.exec(ctx, `UPDATE environment_tunnels SET requested_at = ? WHERE environment_id = ?`, millis(at), environmentID)
	return err
}

func (s *SQLStore) DeleteEnvironmentTunnel(ctx context.Context, environmentID string) error {
	_, err := s.exec(ctx, `DELETE FROM environment_tunnels WHERE environment_id = ?`, environmentID)
	return err
}
