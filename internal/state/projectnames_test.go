package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// A project made before display names keeps the name it had as both its
// slug and what it is called.
func TestDisplayNameMigrationKeepsTheName(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	before := len(migrations) - 2
	for i, m := range migrations[:before] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", before)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO projects (name, root, created_at) VALUES ('pawly', '/src/pawly', 1)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	p, err := st.Project(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "pawly" || p.DisplayName != "pawly" {
		t.Errorf("migrated project = slug %q, called %q; want pawly for both", p.Name, p.DisplayName)
	}
}

func TestProjectDisplayNames(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	add := func(display, root string) (Project, error) {
		t.Helper()
		if err := st.AddProject(ctx, Project{DisplayName: display, Root: root, CreatedAt: time.Now()}); err != nil {
			return Project{}, err
		}
		return st.Project(ctx, display)
	}

	p, err := add("  Organic Web App ", "/src/organic")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "organic-web-app" || p.DisplayName != "Organic Web App" {
		t.Fatalf("added = slug %q, called %q", p.Name, p.DisplayName)
	}

	// Found by either name, the display name in any case.
	for _, ref := range []string{"organic-web-app", "Organic Web App", "ORGANIC web app"} {
		got, err := st.Project(ctx, ref)
		if err != nil || got.Name != "organic-web-app" {
			t.Errorf("Project(%q) = %q, %v", ref, got.Name, err)
		}
	}
	if _, err := st.Project(ctx, "Organic"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Project(Organic) = %v, want ErrNotFound", err)
	}

	// A display name another project answers to is taken, in any case and
	// whether it is that project's display name or its slug.
	for _, display := range []string{"organic web app", "ORGANIC-WEB-APP", "_home", "_HOME"} {
		if _, err := add(display, "/src/"+display); !errors.Is(err, ErrExists) {
			t.Errorf("add %q: got %v, want ErrExists", display, err)
		}
	}

	// One that only slugs the same gets a slug of its own.
	q, err := add("Organic: Web/App", "/src/organic2")
	if err != nil {
		t.Fatal(err)
	}
	if q.Name != "organic-web-app-2" {
		t.Errorf("colliding slug = %q, want organic-web-app-2", q.Name)
	}
	// Nor can a slug be another project's display name.
	if err := st.SetProjectDisplayName(ctx, "organic-web-app", "organic-web-app-3"); err != nil {
		t.Fatal(err)
	}
	r, err := add("Organic Web App", "/src/organic3")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "organic-web-app-4" {
		t.Errorf("slug next to a display name = %q, want organic-web-app-4", r.Name)
	}

	// Anything at all is a name: capitals, camelCase, any script.
	for _, display := range []string{"camelCaseApp", "日本語のアプリ", "Café ☕", "a"} {
		got, err := add(display, "/src/x-"+display)
		if err != nil {
			t.Errorf("add %q: %v", display, err)
		} else if got.DisplayName != display {
			t.Errorf("add %q: called %q", display, got.DisplayName)
		}
	}
	if _, err := add("   ", "/src/blank"); err == nil {
		t.Error("an empty name was accepted")
	}
}

func TestSetProjectDisplayName(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for _, p := range []Project{{Name: "pawly", Root: "/src/pawly"}, {Name: "other", Root: "/src/other"}} {
		if err := st.AddProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	// Renaming keeps the slug, and the old display name is free again.
	if err := st.SetProjectDisplayName(ctx, "pawly", "Pawly Mobile"); err != nil {
		t.Fatal(err)
	}
	p, err := st.Project(ctx, "pawly mobile")
	if err != nil || p.Name != "pawly" || p.DisplayName != "Pawly Mobile" {
		t.Errorf("renamed = %+v, %v", p, err)
	}
	// Changing only its case is a rename of its own.
	if err := st.SetProjectDisplayName(ctx, "pawly", "PAWLY MOBILE"); err != nil {
		t.Errorf("recasing: %v", err)
	}
	if err := st.SetProjectDisplayName(ctx, "other", "pawly mobile"); !errors.Is(err, ErrExists) {
		t.Errorf("taken name: got %v, want ErrExists", err)
	}
	if err := st.SetProjectDisplayName(ctx, "other", "Pawly"); !errors.Is(err, ErrExists) {
		t.Errorf("another's slug: got %v, want ErrExists", err)
	}
	if err := st.SetProjectDisplayName(ctx, "other", " "); err == nil {
		t.Error("an empty name was accepted")
	}
	if err := st.SetProjectDisplayName(ctx, "missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project: got %v, want ErrNotFound", err)
	}
}
