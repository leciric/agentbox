package daemon

import (
	"context"
	"path/filepath"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/testutil"
)

// A project keeps its main up to date by default. The daemon's sync
// fast-forwards a main that is behind its remote, and nothing once the
// setting is off.
func TestSyncBase(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	root := d.fixtureRepo(t, "hello-stack")
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	if !p.SyncBase {
		t.Error("a new project doesn't keep its main up to date")
	}

	remote := filepath.Join(t.TempDir(), "remote.git")
	testutil.Git(t, root, "clone", "--quiet", "--bare", root, remote)
	testutil.Git(t, root, "remote", "add", "origin", remote)
	testutil.Git(t, root, "fetch", "--quiet", "origin")
	testutil.Git(t, root, "branch", "--quiet", "--set-upstream-to=origin/main", "main")
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, root, "clone", "--quiet", remote, other)
	push := func(msg string) string {
		testutil.Git(t, other, "commit", "--quiet", "--allow-empty", "-m", msg)
		testutil.Git(t, other, "push", "--quiet", "origin", "main")
		return testutil.Git(t, other, "rev-parse", "HEAD")
	}
	sync := func() {
		stored, err := d.srv.store.Project(ctx, "hello-stack")
		if err != nil {
			t.Fatal(err)
		}
		if !stored.BaseSyncOff {
			d.srv.syncBase(ctx, stored)
		}
	}

	merged := push("Merged (#86)")
	sync()
	if main := testutil.Git(t, root, "rev-parse", "main"); main != merged {
		t.Errorf("main = %s after a sync, want %s", main, merged)
	}

	if p, err = d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{SyncBase: ptr(false)}); err != nil || p.SyncBase {
		t.Fatalf("UpdateProject(SyncBase: false) = %+v, %v", p, err)
	}
	if p, err := d.client.Project(ctx, "hello-stack"); err != nil || p.SyncBase {
		t.Errorf("Project() after turning it off = %+v, %v", p, err)
	}
	push("Merged (#87)")
	sync()
	if main := testutil.Git(t, root, "rev-parse", "main"); main != merged {
		t.Errorf("main moved to %s with the setting off", main)
	}
}
