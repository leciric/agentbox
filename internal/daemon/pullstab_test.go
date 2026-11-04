package daemon

import (
	"context"
	"testing"

	"agentbox/internal/api"
)

// A project shows its Pull requests tab by default, and turning it off is
// kept: the app reads PullRequests to leave the tab, and its polling, out.
func TestProjectPullRequestsTab(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")})
	if err != nil {
		t.Fatal(err)
	}
	if !p.PullRequests {
		t.Error("a new project has its Pull requests tab off")
	}
	if p, err = d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{PullRequests: new(false)}); err != nil || p.PullRequests {
		t.Fatalf("UpdateProject(PullRequests: false) = %+v, %v", p, err)
	}
	if p, err := d.client.Project(ctx, "hello-stack"); err != nil || p.PullRequests {
		t.Errorf("Project() after turning it off = %+v, %v", p, err)
	}
	if p, err = d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{PullRequests: new(true)}); err != nil || !p.PullRequests {
		t.Fatalf("UpdateProject(PullRequests: true) = %+v, %v", p, err)
	}
}
