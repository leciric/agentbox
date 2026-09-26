package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// NewAgentID is an agent's identity, distinct from its name: set once when
// it's added, and never reused, the way its name now is too.
func NewAgentID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("reading random bytes: %v", err))
	}
	return "agt_" + hex.EncodeToString(b)
}

// NextAgentName reserves this project's next agent-NN and records it in the
// same transaction, so two concurrent reservations can never come back with
// the same name: the second blocks on the first's write lock (the store's
// connection opens with _txlock=immediate) rather than racing it.
//
// floor is the caller's own idea of the lowest number that's safe to hand
// out — from the agents a project has now, and everything else that might
// still name one it doesn't: a worktree directory, a branch, an old event or
// report. The store's own counter is never moved backwards by it, only
// forwards, so a floor that's stale by the time this runs changes nothing.
func (s *Store) NextAgentName(ctx context.Context, project string, floor int) (string, error) {
	if floor < 1 {
		floor = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	var next int
	err = tx.QueryRowContext(ctx, `SELECT next FROM agent_seq WHERE project = ?`, project).Scan(&next)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		next = floor
	case err != nil:
		return "", err
	case floor > next:
		next = floor
	}

	name := fmt.Sprintf("agent-%02d", next)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agent_seq (project, next) VALUES (?, ?)
			ON CONFLICT (project) DO UPDATE SET next = excluded.next`,
		project, next+1); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return name, nil
}

// PastAgentNames is every agent name a project's memory still mentions —
// its events and its reports — including ones for agents that no longer
// exist. Seeding NextAgentName's floor from this, alongside the agents a
// project has now, is what keeps a freed number from being handed out
// again after, say, an import that skipped agent_seq.
func (s *Store) PastAgentNames(ctx context.Context, project string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent FROM events WHERE project = ? AND agent != ''
		UNION
		SELECT agent FROM agent_reports WHERE project = ? AND agent != ''
	`, project, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
