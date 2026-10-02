package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// A project is called anything the user likes, and the API takes that name or
// its slug wherever it takes a project; renaming it changes only the name.
func TestFreeFormProjectNames(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()

	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack"), Name: "Organic Web App"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "organic-web-app" || p.DisplayName != "Organic Web App" {
		t.Fatalf("added = slug %q, called %q", p.Name, p.DisplayName)
	}

	for _, ref := range []string{"organic-web-app", "Organic Web App", "organic WEB app"} {
		if got, err := d.client.Project(ctx, ref); err != nil || got.Name != "organic-web-app" {
			t.Errorf("Project(%q) = %q, %v", ref, got.Name, err)
		}
		if _, err := d.client.Fleet(ctx, ref); err != nil {
			t.Errorf("Fleet(%q): %v", ref, err)
		}
	}

	// Taken in any case; a name that only slugs the same is a project of its own.
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack"), Name: "ORGANIC WEB APP"}); err == nil || !strings.Contains(err.Error(), "already a project called Organic Web App") {
		t.Errorf("AddProject(taken) = %v", err)
	}
	q, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack"), Name: "organicWebApp"})
	if err != nil || q.Name != "organic-web-app-2" || q.DisplayName != "organicWebApp" {
		t.Fatalf("AddProject(camelCase) = %+v, %v", q, err)
	}

	renamed, err := d.client.RenameProject(ctx, "Organic Web App", "Orgânico ☕")
	if err != nil || renamed.Name != "organic-web-app" || renamed.DisplayName != "Orgânico ☕" {
		t.Fatalf("RenameProject = %+v, %v", renamed, err)
	}
	if _, err := d.client.Project(ctx, "orgânico ☕"); err != nil {
		t.Errorf("by its new name: %v", err)
	}
	if _, err := d.client.RenameProject(ctx, "organic-web-app", "ORGANICWEBAPP"); err == nil {
		t.Error("renamed onto another project's name")
	}
	if _, err := d.client.RenameProject(ctx, "organic-web-app", "  "); err == nil {
		t.Error("renamed to nothing")
	}
}
