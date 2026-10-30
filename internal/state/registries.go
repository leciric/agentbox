package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/naming"
)

// Registry is an OCI registry AgentBox pushes its agent image to and a cluster
// pulls it from. Which one is configuration, not a product choice: a Forgejo, a
// Docker Hub account, a GHCR namespace and a local registry:2 are all a url and,
// when they are private, a login.
type Registry struct {
	Name string
	// URL is the repository prefix images are pushed under, with no scheme and
	// no trailing slash: hub.example.com/h4ad, docker.io/someone.
	URL string
	// SealedUsername and SealedSecret are the login, sealed by package secrets.
	// Both are empty when the registry needs no login to pull what AgentBox
	// pushes. This layer never sees either.
	SealedUsername []byte
	SealedSecret   []byte
	// Public says anyone may pull what is pushed here. It is false unless the
	// user said otherwise, because the agent image carries Claude Code and the
	// ACP adapters, which may not be redistributed.
	Public bool
	// CA is the registry's PEM certificate, when a private CA signed it.
	CA []byte
	// Insecure allows http, or a certificate no CA can verify. For a local
	// cluster only.
	Insecure bool
	// CreatedAt is when the user added it.
	CreatedAt time.Time
	// LastSeen is when `agentbox registry test` last reached it, and LastError
	// what that said when it couldn't.
	LastSeen  time.Time
	LastError string
}

// HasCredential reports whether a login is stored for this registry.
func (r Registry) HasCredential() bool { return len(r.SealedSecret) > 0 }

const registryColumns = `name, url, sealed_username, sealed_secret, public, ca, insecure, created_at, last_seen, last_error`

// emptyBlob is how an absent credential or CA is stored: the column's empty
// blob, not NULL. sql turns a nil []byte into NULL, and the schema refuses
// that, so a registry with no login could not be added at all.
func emptyBlob(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

// AddRegistry records a registry. Running it twice is an error rather than a
// silent overwrite: the credential of the one that is there may be the only copy
// of a token somebody no longer has.
func (s *Store) AddRegistry(ctx context.Context, reg Registry) error {
	if err := naming.Validate("registry", reg.Name, 63); err != nil {
		return err
	}
	url, err := registryURL(reg.Name, reg.URL)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO registries (`+registryColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		reg.Name, url, emptyBlob(reg.SealedUsername), emptyBlob(reg.SealedSecret), reg.Public, emptyBlob(reg.CA), reg.Insecure,
		reg.CreatedAt.Unix(), unixOrZero(reg.LastSeen), reg.LastError)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("registry %s: %w", reg.Name, ErrExists)
	}
	return err
}

// registryURL checks the repository prefix, which is not an address: a scheme
// here would end up inside an image reference, where nothing understands it.
func registryURL(name, url string) (string, error) {
	url = strings.TrimSuffix(strings.TrimSpace(url), "/")
	switch {
	case url == "":
		return "", fmt.Errorf("registry %s needs a url, like hub.example.com/you", name)
	case strings.Contains(url, "://"):
		return "", fmt.Errorf("registry %s's url %q is a repository prefix, not an address: leave the scheme off, and use --insecure if it really is http", name, url)
	}
	return url, nil
}

// Registry is one registry by name.
func (s *Store) Registry(ctx context.Context, name string) (Registry, error) {
	registries, err := s.queryRegistries(ctx, `WHERE name = ?`, name)
	if err != nil {
		return Registry{}, err
	}
	if len(registries) == 0 {
		return Registry{}, fmt.Errorf("registry %s: %w", name, ErrNotFound)
	}
	return registries[0], nil
}

// Registries is every registry, by name.
func (s *Store) Registries(ctx context.Context) ([]Registry, error) {
	return s.queryRegistries(ctx, `ORDER BY name`)
}

func (s *Store) queryRegistries(ctx context.Context, clause string, args ...any) ([]Registry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+registryColumns+` FROM registries `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var registries []Registry
	for rows.Next() {
		var reg Registry
		var created, lastSeen int64
		if err := rows.Scan(&reg.Name, &reg.URL, &reg.SealedUsername, &reg.SealedSecret, &reg.Public,
			&reg.CA, &reg.Insecure, &created, &lastSeen, &reg.LastError); err != nil {
			return nil, err
		}
		reg.CreatedAt = time.Unix(created, 0)
		reg.LastSeen = timeOrZero(lastSeen)
		registries = append(registries, reg)
	}
	return registries, rows.Err()
}

// SetRegistryHealth records what the last test of a registry found. An empty
// errText is a test that passed.
func (s *Store) SetRegistryHealth(ctx context.Context, name string, at time.Time, errText string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE registries SET last_seen = ?, last_error = ? WHERE name = ?`,
		unixOrZero(at), errText, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("registry %s: %w", name, ErrNotFound)
	}
	return nil
}

// SetRegistryCredential replaces a registry's sealed login, which is what
// `agentbox registry rotate` does. Both empty makes it one that needs no login.
func (s *Store) SetRegistryCredential(ctx context.Context, name string, sealedUsername, sealedSecret []byte) error {
	res, err := s.db.ExecContext(ctx, `UPDATE registries SET sealed_username = ?, sealed_secret = ? WHERE name = ?`,
		emptyBlob(sealedUsername), emptyBlob(sealedSecret), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("registry %s: %w", name, ErrNotFound)
	}
	return nil
}

// SetRegistrySettings replaces where a registry is and how it is reached: its
// url (checked like add's), whether it is public, its CA and whether it is
// insecure. The login is not touched.
func (s *Store) SetRegistrySettings(ctx context.Context, reg Registry) error {
	url, err := registryURL(reg.Name, reg.URL)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE registries SET url = ?, public = ?, ca = ?, insecure = ? WHERE name = ?`,
		url, reg.Public, emptyBlob(reg.CA), reg.Insecure, reg.Name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("registry %s: %w", reg.Name, ErrNotFound)
	}
	return nil
}

// RemoveRegistry forgets one. A registry a cluster pulls from stays: that
// cluster's pods would stop starting, and the registry's name is the only thing
// telling the user where its images come from.
func (s *Store) RemoveRegistry(ctx context.Context, name string) error {
	clusters, err := s.queryClusters(ctx, `WHERE registry = ? ORDER BY name`, name)
	if err != nil {
		return err
	}
	if len(clusters) > 0 {
		used := make([]string, 0, len(clusters))
		for _, cluster := range clusters {
			used = append(used, cluster.Name)
		}
		return fmt.Errorf("registry %s is where cluster %s pulls its images: point the cluster elsewhere (agentbox cluster add) or remove it first",
			name, strings.Join(used, ", "))
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM registries WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("registry %s: %w", name, ErrNotFound)
	}
	return nil
}
