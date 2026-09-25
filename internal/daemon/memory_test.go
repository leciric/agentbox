package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// One project memory, reached from three places: the user's routes, the
// project chat's socket, and an agent's own socket inside its machine. What
// each of the three may do to it is the whole point of the test.
func TestProjectMemoryOnAllThreeSurfaces(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	project := a.Project

	user := d.client.ProjectMemory(project)
	lead := api.NewClient(d.srv.leadSocketPath(project)).LeadMemory()
	agent := api.NewClient(d.srv.agentSocketPath(a.Instance)).SelfMemory()

	// The agent records what it ran into. It never says which agent it is:
	// the socket does.
	event, err := agent.AppendEvent(ctx, api.AddMemoryEventRequest{
		Type:    "test_failed",
		Agent:   "agent-99", // ignored: an agent can't write in another's name
		Payload: json.RawMessage(`{"error":"connection refused on 127.0.0.1:7777"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Agent != a.Name || event.Project != project {
		t.Errorf("the agent's event = %+v, want it attributed to %s/%s", event, project, a.Name)
	}

	// The lead reads it and writes down what it means. A memory is the lead's
	// to write, not the agent's.
	events, err := lead.Events(ctx, api.EventQuery{})
	if err != nil || len(events) != 1 || events[0].ID != event.ID {
		t.Fatalf("the lead saw %d events, %v", len(events), err)
	}
	mem, err := lead.AddMemory(ctx, api.AddMemoryRequest{
		Kind: api.MemoryKindProject, Importance: 5,
		Title: "The API listens on port 7777", Content: "internal/daemon/server.go binds :7777.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if mem.ID == "" || mem.Kind != api.MemoryKindProject {
		t.Fatalf("the memory = %+v", mem)
	}
	if _, err := agent.AddMemory(ctx, api.AddMemoryRequest{Title: "I know best"}); err == nil {
		t.Error("an agent wrote a project memory")
	}

	// The agent searches what the project knows, and finds both the memory
	// somebody wrote and the raw event it came from.
	found, err := agent.Search(ctx, "7777", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Memories) != 1 || found.Memories[0].ID != mem.ID {
		t.Errorf("the agent's search found %d memories", len(found.Memories))
	}
	if len(found.Events) != 1 {
		t.Errorf("the agent's search found %d events", len(found.Events))
	}

	// Working memory is the lead's and the user's to keep current; an agent
	// reads it and leaves it alone.
	task := "Move the API off 7777"
	working, err := lead.SetWorkingMemory(ctx, api.WorkingMemoryPatch{CurrentTask: &task})
	if err != nil {
		t.Fatal(err)
	}
	if working.CurrentTask != task || working.UpdatedAt.IsZero() {
		t.Fatalf("working memory = %+v", working)
	}
	goal := "Ship 0.12"
	if working, err = user.SetWorkingMemory(ctx, api.WorkingMemoryPatch{Goal: &goal}); err != nil {
		t.Fatal(err)
	}
	if working.Goal != goal || working.CurrentTask != task {
		t.Errorf("a one-field patch changed the rest: %+v", working)
	}
	if seen, err := agent.WorkingMemory(ctx); err != nil || seen.CurrentTask != task {
		t.Errorf("the agent read %+v, %v", seen, err)
	}
	if _, err := agent.SetWorkingMemory(ctx, api.WorkingMemoryPatch{Goal: &goal}); err == nil {
		t.Error("an agent set the project's working memory")
	}

	// It records what it produced as a reference, and files a report.
	artifact, err := agent.AddArtifact(ctx, api.AddArtifactRequest{
		Type: "file", Path: "internal/daemon/server.go", Metadata: json.RawMessage(`{"lines":393}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Agent != a.Name {
		t.Errorf("the artifact = %+v, want it attributed to the agent behind the socket", artifact)
	}
	report, err := agent.AddReport(ctx, api.AddReportRequest{
		Task: "Move the API off 7777", Status: api.ReportPartial,
		Summary:         "The port is a constant now, but nothing reads it from configuration yet.",
		Discoveries:     []string{"The preview proxy assumes 7777 too"},
		RemainingIssues: []string{"The preview proxy"},
		Artifacts:       []string{artifact.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Agent != a.Name || report.Status != api.ReportPartial {
		t.Fatalf("the report = %+v", report)
	}

	// The user sees all of it, and the lead sees the same rows.
	if reports, err := user.Reports(ctx, ""); err != nil || len(reports) != 1 {
		t.Errorf("the user saw %d reports, %v", len(reports), err)
	}
	if reports, err := lead.Reports(ctx, a.Name); err != nil || len(reports) != 1 {
		t.Errorf("the lead saw %d of the agent's reports, %v", len(reports), err)
	}
	if artifacts, err := user.Artifacts(ctx); err != nil || len(artifacts) != 1 {
		t.Errorf("the user saw %d artifacts, %v", len(artifacts), err)
	}
	memories, err := user.Memories(ctx, api.MemoryKindProject)
	if err != nil || len(memories) != 1 {
		t.Fatalf("the user saw %d memories of the kind asked for, %v", len(memories), err)
	}

	// A correction replaces the fact, and the replaced one stops coming back.
	if _, err := lead.AddMemory(ctx, api.AddMemoryRequest{
		Kind: api.MemoryKindProject, Title: "The API listens on port 8080",
		Content: "It moved off 7777.", SupersedesID: mem.ID,
	}); err != nil {
		t.Fatal(err)
	}
	memories, err = user.Memories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 || memories[0].ID == mem.ID {
		t.Errorf("after superseding, the project remembers %d memories including the old one", len(memories))
	}

	// A project that doesn't exist is a 404, not an empty answer: memory is
	// scoped to a project, and a typo shouldn't look like a project with
	// nothing in it.
	if _, err := d.client.ProjectMemory("nope").Memories(ctx); !api.IsNotFound(err) {
		t.Errorf("an unknown project's memory: %v, want not found", err)
	}
}

// An agent's memory routes are its project's, and the ones it shouldn't have
// are not there at all rather than quietly ignored.
func TestInAgentMemoryIsScopedToItsProject(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	agent := api.NewClient(d.srv.agentSocketPath(a.Instance)).SelfMemory()

	if _, err := agent.AppendEvent(ctx, api.AddMemoryEventRequest{Type: "branch_pushed"}); err != nil {
		t.Fatal(err)
	}
	// The same event is the project's, read from outside.
	events, err := d.client.ProjectMemory(a.Project).Events(ctx, api.EventQuery{Agent: a.Name})
	if err != nil || len(events) != 1 {
		t.Fatalf("the project saw %d of the agent's events, %v", len(events), err)
	}
	if events[0].Type != "branch_pushed" {
		t.Errorf("event = %+v", events[0])
	}

	// An event with no type is refused, with something the agent can act on.
	if _, err := agent.AppendEvent(ctx, api.AddMemoryEventRequest{}); err == nil || !strings.Contains(err.Error(), "type") {
		t.Errorf("an event with no type: %v", err)
	}
}

// POST /context is the stable way to ask what a project knows, on all three
// surfaces: the app, a project's chat and an agent all get one slice built by
// one builder rather than three approximations of it (D75). An agent's is
// smaller by default, and the accounting says what each build cost.
func TestContextRouteOnAllThreeSurfaces(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	project := a.Project
	user := d.client.ProjectMemory(project)
	lead := api.NewClient(d.srv.leadSocketPath(project)).LeadMemory()
	agent := api.NewClient(d.srv.agentSocketPath(a.Instance)).SelfMemory()

	if _, err := lead.SetWorkingMemory(ctx, api.WorkingMemoryPatch{
		Goal: ptr("Ship the reminders page"), CurrentTask: ptr("Logging in with Google"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := lead.AddMemory(ctx, api.AddMemoryRequest{
		Kind: api.MemoryKindDiscovery, Importance: 5,
		Title:   "The OAuth callback needs the exact port",
		Content: "Google rejects a redirect URI whose port isn't registered.",
	}); err != nil {
		t.Fatal(err)
	}

	built, err := user.Context(ctx, api.ContextRequest{Query: "OAuth callback port"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(built.Text, "OAuth callback needs the exact port") {
		t.Errorf("the context doesn't answer the query:\n%s", built.Text)
	}
	if !strings.Contains(built.Text, "Ship the reminders page") {
		t.Errorf("the context leaves out what the project is doing:\n%s", built.Text)
	}
	if built.Stats.Budget == 0 || built.Stats.Tokens == 0 || built.Stats.CorpusTokens == 0 {
		t.Errorf("the build isn't accounted for: %+v", built.Stats)
	}
	if len(built.Sections) == 0 || built.Sections[0].Kind != "working" {
		t.Errorf("the sections are %+v", built.Sections)
	}

	// A project's chat gets the whole budget; an agent gets a share of it,
	// and is recorded as an agent without having to say so.
	fromLead, err := lead.Context(ctx, api.ContextRequest{Query: "OAuth"})
	if err != nil {
		t.Fatal(err)
	}
	fromAgent, err := agent.Context(ctx, api.ContextRequest{Query: "OAuth"})
	if err != nil {
		t.Fatal(err)
	}
	if fromAgent.Stats.Budget >= fromLead.Stats.Budget {
		t.Errorf("an agent's budget is %d of the project's %d", fromAgent.Stats.Budget, fromLead.Stats.Budget)
	}
	if fromAgent.Stats.For != api.ContextForAgent {
		t.Errorf("an agent's build was recorded as %q", fromAgent.Stats.For)
	}
	// A budget it asks for itself is honoured, so a tool that knows what it
	// can afford isn't held to the project's number.
	asked, err := agent.Context(ctx, api.ContextRequest{Query: "OAuth", Budget: 600})
	if err != nil {
		t.Fatal(err)
	}
	if asked.Stats.Budget != 600 {
		t.Errorf("a build asked for 600 tokens got a budget of %d", asked.Stats.Budget)
	}

	// And what the builds cost is there to be shown.
	account, err := user.ContextStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if account.Builds < 4 || account.Tokens == 0 {
		t.Errorf("the accounting after four builds is %+v", account)
	}
	if len(account.Recent) == 0 || account.Recent[0].Query != "OAuth" {
		t.Errorf("the accounting doesn't have the newest build first: %+v", account.Recent)
	}

	// The project budget is a setting, like the rollover threshold.
	p, err := d.client.SetContextBudget(ctx, project, 1200)
	if err != nil {
		t.Fatal(err)
	}
	if p.ContextBudget != 1200 {
		t.Errorf("the project's context budget is %d", p.ContextBudget)
	}
	if _, err := d.client.SetContextBudget(ctx, project, 10); err == nil {
		t.Error("a budget below the minimum was accepted")
	}
	after, err := lead.Context(ctx, api.ContextRequest{Query: "OAuth"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Stats.Budget != 1200 {
		t.Errorf("a build after the setting changed used a budget of %d", after.Stats.Budget)
	}
}
