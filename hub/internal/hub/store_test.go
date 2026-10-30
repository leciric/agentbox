package hub

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// eachStore runs fn against SQLite, and against Postgres when
// AGENTBOX_TEST_POSTGRES names a database that the test may empty.
func eachStore(t *testing.T, fn func(t *testing.T, s Store)) {
	ctx := context.Background()
	t.Run("sqlite", func(t *testing.T) {
		s, err := OpenStore(ctx, "sqlite:"+filepath.Join(t.TempDir(), "hub.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		fn(t, s)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("AGENTBOX_TEST_POSTGRES")
		if dsn == "" {
			t.Skip("set AGENTBOX_TEST_POSTGRES to a Postgres database the test may empty")
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS environments, sessions, users, schema_migrations`); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		s, err := OpenStore(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		fn(t, s)
	})
}

func TestStoreUsersAndSessions(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		now := time.Now().UTC().Truncate(time.Millisecond)
		if n, err := s.CountUsers(ctx); err != nil || n != 0 {
			t.Fatalf("CountUsers = %d, %v", n, err)
		}
		u := User{ID: newID("usr_"), Email: "Owner@Example.com", Name: "Owner", PasswordHash: "x", CreatedAt: now}
		if err := s.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateUser(ctx, User{ID: newID("usr_"), Email: "owner@example.com", PasswordHash: "y", CreatedAt: now}); !errors.Is(err, ErrExists) {
			t.Errorf("the same email in another case: err = %v, want ErrExists", err)
		}
		got, err := s.UserByEmail(ctx, "OWNER@example.com")
		if err != nil || got.ID != u.ID || got.Email != "owner@example.com" || !got.CreatedAt.Equal(now) {
			t.Errorf("UserByEmail = %+v, %v", got, err)
		}
		if _, err := s.UserByID(ctx, "usr_missing"); !errors.Is(err, ErrNotFound) {
			t.Errorf("UserByID(missing): err = %v", err)
		}

		sess := Session{TokenHash: hashToken(newToken(sessionPrefix)), UserID: u.ID, Label: "cli", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		if err := s.CreateSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Session(ctx, sess.TokenHash, now); err != nil || got.UserID != u.ID || got.Label != "cli" {
			t.Errorf("Session = %+v, %v", got, err)
		}
		if _, err := s.Session(ctx, sess.TokenHash, now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
			t.Errorf("an expired session: err = %v, want ErrNotFound", err)
		}
		if err := s.DeleteSession(ctx, sess.TokenHash); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Session(ctx, sess.TokenHash, now); !errors.Is(err, ErrNotFound) {
			t.Errorf("a deleted session: err = %v", err)
		}
	})
}

func TestStoreEnvironments(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		now := time.Now().UTC().Truncate(time.Millisecond)
		owner := User{ID: newID("usr_"), Email: "owner@example.com", PasswordHash: "x", CreatedAt: now}
		other := User{ID: newID("usr_"), Email: "other@example.com", PasswordHash: "x", CreatedAt: now}
		for _, u := range []User{owner, other} {
			if err := s.CreateUser(ctx, u); err != nil {
				t.Fatal(err)
			}
		}
		vps := Environment{ID: newID("env_"), OwnerID: owner.ID, Name: "vps", TokenHash: hashToken(newToken(environmentPrefix)), CreatedAt: now}
		pc := Environment{ID: newID("env_"), OwnerID: owner.ID, Name: "pc", TokenHash: hashToken(newToken(environmentPrefix)), CreatedAt: now}
		for _, e := range []Environment{vps, pc} {
			if err := s.CreateEnvironment(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.CreateEnvironment(ctx, Environment{ID: newID("env_"), OwnerID: owner.ID, Name: "vps", TokenHash: "other", CreatedAt: now}); !errors.Is(err, ErrExists) {
			t.Errorf("a second environment named vps: err = %v, want ErrExists", err)
		}
		if err := s.CreateEnvironment(ctx, Environment{ID: newID("env_"), OwnerID: other.ID, Name: "vps", TokenHash: "theirs", CreatedAt: now}); err != nil {
			t.Errorf("another account's vps: %v", err)
		}

		envs, err := s.Environments(ctx, owner.ID)
		if err != nil || len(envs) != 2 || envs[0].Name != "pc" || envs[1].Name != "vps" || !envs[1].LastSeenAt.IsZero() {
			t.Fatalf("Environments = %+v, %v", envs, err)
		}
		if got, err := s.EnvironmentByToken(ctx, vps.TokenHash); err != nil || got.ID != vps.ID {
			t.Errorf("EnvironmentByToken = %+v, %v", got, err)
		}
		seen := now.Add(time.Minute)
		if err := s.SeenEnvironment(ctx, vps.ID, seen, "0.2.0", "vps-1"); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Environment(ctx, vps.ID); err != nil || !got.LastSeenAt.Equal(seen) || got.Version != "0.2.0" || got.Hostname != "vps-1" {
			t.Errorf("after SeenEnvironment: %+v, %v", got, err)
		}
		if err := s.DeleteEnvironment(ctx, pc.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteEnvironment(ctx, pc.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("deleting it again: err = %v, want ErrNotFound", err)
		}
		if _, err := s.EnvironmentByToken(ctx, pc.TokenHash); !errors.Is(err, ErrNotFound) {
			t.Errorf("a deleted environment's token: err = %v", err)
		}
	})
}

func TestStoreReopens(t *testing.T) {
	ctx := context.Background()
	path := "sqlite:" + filepath.Join(t.TempDir(), "hub.db")
	s, err := OpenStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(ctx, User{ID: "usr_1", Email: "a@example.com", PasswordHash: "x", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = OpenStore(ctx, path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = s.Close() }()
	if n, _ := s.CountUsers(ctx); n != 1 {
		t.Errorf("after reopening, CountUsers = %d", n)
	}
	if _, err := OpenStore(ctx, "mysql://nope"); err == nil {
		t.Error("an unsupported database opened")
	}
	if got := redact("postgres://agentbox:s3cret@db:5432/agentbox"); got != "postgres://agentbox:***@db:5432/agentbox" {
		t.Errorf("redact = %q", got)
	}
}

func TestPasswords(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Error("a 5-character password was accepted")
	}
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "correct horse battery") || CheckPassword(hash, "correct horse battery!") || CheckPassword("not a hash", "x") {
		t.Error("CheckPassword gave the wrong answer")
	}
}
