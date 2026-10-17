package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Hatch's move to usehatch.dev: connectors at the old address follow it, and
// an OAuth one loses its sign-in, which belongs to the old host.
func TestHatchMoveMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// The database as it was just before the move's two migrations, wherever
	// later ones put them.
	before := migrations[:slices.IndexFunc(migrations, func(m string) bool { return strings.Contains(m, "usehatch.dev") })]
	for i, m := range before {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(before))); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO connectors (project, name, url, auth, issuer, client_id, resource, access_token, refresh_token, expires_at, scope, connected_at, updated_at)
			VALUES ('', 'hatch', 'https://hatch.linting.dev/mcp', 'oauth', 'https://hatch.linting.dev', 'c1', 'https://hatch.linting.dev/mcp', x'01', x'02', 5, 'a', 6, 1)`,
		`INSERT INTO connectors (project, name, url, auth, issuer, client_id, access_token, updated_at)
			VALUES ('pawly', 'pages', 'https://Hatch.linting.dev/mcp/', 'oauth', 'i', 'c2', x'01', 1)`,
		`INSERT INTO connectors (project, name, url, auth, secret, updated_at)
			VALUES ('pawly', 'keyed', 'https://hatch.linting.dev/mcp', 'secret', 'K', 1)`,
		`INSERT INTO connectors (project, name, url, auth, access_token, updated_at)
			VALUES ('pawly', 'notion', 'https://mcp.notion.com/mcp', 'oauth', x'01', 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	get := func(project, name string) Connector {
		t.Helper()
		var c Connector
		var found bool
		all, err := st.ProjectConnectors(ctx, project)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range all {
			if x.Name == name {
				c, found = x, true
			}
		}
		if !found {
			t.Fatalf("connector %s/%s is gone", project, name)
		}
		return c
	}
	for _, c := range []Connector{get("pawly", "hatch"), get("pawly", "pages")} {
		if c.URL[:len("https://usehatch.dev/")] != "https://usehatch.dev/" || len(c.AccessToken) != 0 || len(c.RefreshToken) != 0 || c.ClientID != "" || c.Issuer != "" || c.Resource != "" {
			t.Errorf("%s = %+v; want moved and signed out", c.Name, c)
		}
	}
	if c := get("pawly", "pages"); c.URL != "https://usehatch.dev/mcp/" {
		t.Errorf("pages url = %q", c.URL)
	}
	if c := get("pawly", "keyed"); c.URL != "https://usehatch.dev/mcp" || c.Secret != "K" {
		t.Errorf("secret connector = %+v", c)
	}
	if c := get("pawly", "notion"); len(c.AccessToken) == 0 {
		t.Errorf("an unrelated connector lost its sign-in")
	}
}
