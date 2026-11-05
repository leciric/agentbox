package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The lead's chat starts its tools from GET /v1/project, so it doesn't wait on
// what projectInfo reads from the repository, which takes seconds on a large
// one shared from a Mac. The app's view of the project still has it.
func TestTheLeadsProjectSkipsTheRepository(t *testing.T) {
	t.Parallel()
	d, lead := oneProject(t)
	ctx := context.Background()
	app, err := d.client.Project(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.Root, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.Root, ".env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if app, err = d.client.Project(ctx, "web"); err != nil {
		t.Fatal(err)
	}
	if len(app.EnvFiles) != 1 || app.Branch == "" {
		t.Fatalf("the app's project = env files %v, branch %q; want .env and a branch", app.EnvFiles, app.Branch)
	}
	got, err := lead.ProjectSelf(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "web" || got.Root != app.Root {
		t.Errorf("the lead's project = %q at %q, want web at %q", got.Name, got.Root, app.Root)
	}
	if len(got.EnvFiles) != 0 || got.Branch != "" {
		t.Errorf("the lead's project read the repository: env files %v, branch %q", got.EnvFiles, got.Branch)
	}
}
