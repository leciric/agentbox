package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// AgentTokenHash is the hex SHA-256 of a token, which is what the database
// keeps: the token itself exists only in the pod's Secret.
func AgentTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SetAgentToken makes token the one agent's pod authenticates with, replacing
// any earlier one. expiresAt zero means as long as the pod.
//
// The replacement is the unique index's conflict target, so the row keeps one
// hash and one deadline, and the token it held stops working with this write
// rather than lingering until somebody remembers to delete it.
func (s *Store) SetAgentToken(ctx context.Context, project, agent, token string, expiresAt time.Time) error {
	var expires int64
	if !expiresAt.IsZero() {
		expires = expiresAt.Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_tokens (hash, project, agent, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (project, agent) DO UPDATE SET hash = excluded.hash, expires_at = excluded.expires_at,
		created_at = excluded.created_at`,
		AgentTokenHash(token), project, agent, expires, time.Now().Unix())
	return err
}

// AgentForToken is the agent a token belongs to, ErrNotFound when the token is
// not one this daemon issued or it has expired.
//
// The lookup is by hash, so comparing a request's token never reads the
// plaintext back out of the database. An expired row is refused here as well
// as swept by PruneAgentTokens, because the sweep runs on a timer and the
// deadline has to hold between sweeps.
func (s *Store) AgentForToken(ctx context.Context, token string) (project, agent string, err error) {
	if token == "" {
		return "", "", ErrNotFound
	}
	now := time.Now().Unix()
	err = s.db.QueryRowContext(ctx, `SELECT project, agent FROM agent_tokens
		WHERE hash = ? AND (expires_at = 0 OR expires_at > ?)`, AgentTokenHash(token), now).Scan(&project, &agent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	return project, agent, nil
}

// RemoveAgentToken forgets the agent's token, so a request carrying it is
// refused from the next lookup on. It is ErrNotFound when the agent had none,
// which tells a caller revoking one whether there was something to revoke.
func (s *Store) RemoveAgentToken(ctx context.Context, project, agent string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agent_tokens WHERE project = ? AND agent = ?`, project, agent)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("agent %s/%s: %w", project, agent, ErrNotFound)
	}
	return nil
}

// PruneAgentTokens removes the tokens whose deadline has passed and returns
// how many. A zero deadline belongs to a pod and never expires: the
// reconciler removes that row when it deletes the pod, not this sweep.
func (s *Store) PruneAgentTokens(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agent_tokens WHERE expires_at != 0 AND expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
