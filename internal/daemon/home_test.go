package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// The Home chat's key is the same on both sides of the API, and no project
// can ever be called it.
func TestHomeProjectIsReserved(t *testing.T) {
	t.Parallel()
	if api.HomeProject != state.HomeProject {
		t.Fatalf("api.HomeProject = %q, state.HomeProject = %q", api.HomeProject, state.HomeProject)
	}
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	_, err := d.client.AddProject(context.Background(), api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack"), Name: state.HomeProject})
	if err == nil || !strings.Contains(err.Error(), "invalid project name") {
		t.Errorf("AddProject(%q) = %v, want it refused", state.HomeProject, err)
	}
}

// The Home chat is there with no project at all, makes nothing until it is
// used, and is kept under its own key like a project's chat.
func TestHomeChatNeedsNoProject(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()

	info, err := d.client.ProjectChat(ctx, api.HomeProject)
	if err != nil {
		t.Fatal(err)
	}
	if info.Started || info.Ref != "_home/lead" || info.Chat != api.ChatOff {
		t.Errorf("ProjectChat(_home) = %+v, want _home/lead, not started, off", info)
	}
	if thread, err := d.client.Chat(ctx, api.HomeProject); err != nil || len(thread.Items) != 0 {
		t.Errorf("Chat(_home) = %+v, %v; want it empty", thread, err)
	}
	if files, err := d.client.Files(ctx, api.HomeProject); err != nil || len(files.Files) != 0 {
		t.Errorf("Files(_home) = %+v, %v; want none", files, err)
	}
	if _, err := os.Stat(d.paths.LeadHome(state.HomeProject)); !os.IsNotExist(err) {
		t.Errorf("a private HOME was made just by looking (stat: %v)", err)
	}

	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	a, err := d.srv.manager(nil).EnsureHome(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !a.IsHome() || a.Worktree != d.paths.HomeChat() {
		t.Errorf("EnsureHome() = %+v, want the Home chat in %s", a, d.paths.HomeChat())
	}
	if st, err := os.Stat(a.Worktree); err != nil || !st.IsDir() {
		t.Errorf("its working directory wasn't made: %v", err)
	}
	brief, err := os.ReadFile(filepath.Join(d.paths.LeadHome(state.HomeProject), ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), "the user's main chat") || !strings.Contains(string(brief), "**hello-stack**") {
		t.Errorf("the brief doesn't say what the chat is, or which projects there are:\n%s", brief)
	}
	if info, err := d.client.ProjectChat(ctx, api.HomeProject); err != nil || !info.Started {
		t.Errorf("ProjectChat(_home) = %+v, %v; want it started", info, err)
	}
	// It's no agent of any project.
	if agents, err := d.client.Agents(ctx, ""); err != nil || len(agents) != 0 {
		t.Errorf("Agents() = %+v, %v; want none", agents, err)
	}

	// Resetting it takes its HOME away, and leaves its folder.
	if err := d.client.ResetProjectChat(ctx, api.HomeProject); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.paths.LeadHome(state.HomeProject)); !os.IsNotExist(err) {
		t.Errorf("the reset left its private HOME (stat: %v)", err)
	}
	if _, err := os.Stat(a.Worktree); err != nil {
		t.Errorf("the reset took its working directory: %v", err)
	}
}

// The Home chat's socket reaches every project, and only through the routes
// its tools need.
func TestHomeSocket(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	home := api.NewClient(d.srv.leadSocketPath(state.HomeProject))

	projects, err := home.Projects(ctx)
	if err != nil || len(projects) != 1 || projects[0].Name != "hello-stack" {
		t.Fatalf("Projects() = %+v, %v; want hello-stack", projects, err)
	}
	if _, err := home.Fleet(ctx, "hello-stack"); err != nil {
		t.Errorf("Fleet(hello-stack) = %v", err)
	}
	if _, err := home.ProjectMemory("hello-stack").Search(ctx, "anything", 5); err != nil {
		t.Errorf("Search(hello-stack) = %v", err)
	}
	if _, err := home.HomeAgentChat(ctx, "hello-stack", state.LeadName); err == nil {
		t.Error("HomeAgentChat(lead) read a project's chat as if it were an agent")
	}
	if err := home.HomeTellLead(ctx, "nope", "hello"); err == nil {
		t.Error("HomeTellLead(nope) told a project that doesn't exist")
	}

	// A project from a URL is cloned first, then added like a folder.
	dest := filepath.Join(t.TempDir(), "clone")
	p, err := home.HomeAddProject(ctx, api.HomeAddProjectRequest{URL: d.fixtureRepo(t, "hello-stack"), Path: dest, Name: "cloned"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "cloned" || p.Root != dest {
		t.Errorf("HomeAddProject() = %+v, want cloned at %s", p, dest)
	}
	// A name that's taken is refused before anything is cloned.
	again := filepath.Join(t.TempDir(), "again")
	if _, err := home.HomeAddProject(ctx, api.HomeAddProjectRequest{URL: d.fixtureRepo(t, "hello-stack"), Path: again, Name: "cloned"}); err == nil {
		t.Error("HomeAddProject() added a second project called cloned")
	}
	if _, err := os.Stat(again); !os.IsNotExist(err) {
		t.Errorf("a refused add left a clone behind (stat: %v)", err)
	}

	// Nothing else: not a project's settings, nor removing one.
	if err := home.RemoveProject(ctx, "hello-stack"); err == nil || !strings.Contains(err.Error(), "Home chat can't") {
		t.Errorf("RemoveProject() over the Home socket = %v, want it refused", err)
	}
	// And a project's own socket doesn't reach the others.
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	if _, err := lead.Projects(ctx); err == nil {
		t.Error("a project's chat listed every project")
	}
}
