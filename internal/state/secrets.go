package state

import (
	"context"
	"fmt"
	"time"
)

// Secret is one secret you handed to a project or to a single agent: a name
// that becomes an environment variable inside the agent, and a value.
//
// Value is the *ciphertext*. This layer never sees a plaintext value: package
// secrets seals it before it gets here and opens it on its way into an agent,
// so a copied state.db is a file of sealed blobs (see
// [D52](../../docs/implementation/decisions.md#d52)).
type Secret struct {
	Project string
	Agent   string // empty for a project secret, which every agent of it gets
	Name    string
	Value   []byte
	// UpdatedAt is when the value was last written. A secret has no created_at
	// of its own: what matters is how old the value in the agents is.
	UpdatedAt time.Time
}

// Scopes a secret can have.
const (
	// ScopeProject is a secret every agent of the project gets.
	ScopeProject = "project"
	// ScopeAgent is a secret one agent gets.
	ScopeAgent = "agent"
)

// Scope is ScopeProject or ScopeAgent.
func (s Secret) Scope() string {
	if s.Agent == "" {
		return ScopeProject
	}
	return ScopeAgent
}

const secretColumns = `project, agent, name, value, updated_at`

// SetSecret stores a secret's ciphertext, replacing whatever was there. The
// primary key is (project, agent, name), so a project secret and an agent's own
// secret of the same name are two rows: the agent's wins when both are
// delivered.
func (s *Store) SetSecret(ctx context.Context, sec Secret) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO secrets (`+secretColumns+`) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project, agent, name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		sec.Project, sec.Agent, sec.Name, sec.Value, sec.UpdatedAt.Unix())
	return err
}

// Secret reads one secret, ciphertext and all. An agent is "" for a project
// secret.
func (s *Store) Secret(ctx context.Context, project, agent, name string) (Secret, error) {
	secrets, err := s.querySecrets(ctx, `WHERE project = ? AND agent = ? AND name = ?`, project, agent, name)
	if err != nil {
		return Secret{}, err
	}
	if len(secrets) == 0 {
		return Secret{}, fmt.Errorf("secret %s: %w", name, ErrNotFound)
	}
	return secrets[0], nil
}

// Secrets lists one scope's secrets by name: a project's own when agent is "",
// otherwise that agent's own. It never mixes the two, because which scope a
// secret is in is the thing the caller is deciding about.
func (s *Store) Secrets(ctx context.Context, project, agent string) ([]Secret, error) {
	return s.querySecrets(ctx, `WHERE project = ? AND agent = ? ORDER BY name`, project, agent)
}

// ProjectSecrets lists every secret of a project, both scopes, project ones
// first. It is what a view of the whole project reads.
func (s *Store) ProjectSecrets(ctx context.Context, project string) ([]Secret, error) {
	return s.querySecrets(ctx, `WHERE project = ? ORDER BY agent, name`, project)
}

func (s *Store) RemoveSecret(ctx context.Context, project, agent, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE project = ? AND agent = ? AND name = ?`, project, agent, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("secret %s: %w", name, ErrNotFound)
	}
	return nil
}

// RemoveAgentSecrets forgets the secrets that belonged to one agent. Destroying
// an agent calls it: nothing else will ever read them, and they are the one
// thing an agent held that isn't on its branch.
func (s *Store) RemoveAgentSecrets(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE project = ? AND agent = ?`, project, agent)
	return err
}

// RemoveProjectSecrets forgets every secret of a project, in both scopes.
func (s *Store) RemoveProjectSecrets(ctx context.Context, project string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE project = ?`, project)
	return err
}

func (s *Store) querySecrets(ctx context.Context, clause string, args ...any) ([]Secret, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+secretColumns+` FROM secrets `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var secrets []Secret
	for rows.Next() {
		var sec Secret
		var updated int64
		if err := rows.Scan(&sec.Project, &sec.Agent, &sec.Name, &sec.Value, &updated); err != nil {
			return nil, err
		}
		sec.UpdatedAt = time.Unix(updated, 0)
		secrets = append(secrets, sec)
	}
	return secrets, rows.Err()
}
