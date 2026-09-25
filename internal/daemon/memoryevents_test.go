package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/testutil"
)

// eventsOfType waits for at least one event of a type to reach a project's
// memory, and returns them all, newest first.
func eventsOfType(t *testing.T, d testDaemon, project, eventType string) []memory.Event {
	t.Helper()
	var events []memory.Event
	waitFor(t, "a "+eventType+" event", func() bool {
		var err error
		events, err = d.srv.memory().Events(context.Background(), project, memory.EventFilter{Types: []string{eventType}})
		return err == nil && len(events) > 0
	})
	return events
}

func payloadOf(t *testing.T, e memory.Event) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(e.Payload, &out); err != nil {
		t.Fatalf("event %s's payload doesn't parse: %v", e.ID, err)
	}
	return out
}

// Creating an agent is one of the daemon's own chokepoints: nothing an agent
// says is needed for the project to remember it exists.
func TestMemoryCapturesAgentCreated(t *testing.T) {
	ctx := context.Background()
	t.Setenv("INCUS_INSTANCES", `[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]`)
	d := startTestDaemon(t, t.TempDir(), recordingIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}

	job, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{
		Project: "hello-stack", Name: "agent-01", Title: "Reminders page", AI: "none", Task: "Fix the bug",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "agent-01 to be created", func() bool {
		j, err := d.client.Job(ctx, job.ID)
		return err == nil && j.Done()
	})
	if j, _ := d.client.Job(ctx, job.ID); j.Status != api.JobSucceeded {
		t.Fatalf("creating agent-01 = %s: %s", j.Status, j.Error)
	}

	events := eventsOfType(t, d, "hello-stack", "agent_created")
	if len(events) != 1 {
		t.Fatalf("agent_created events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Agent != "agent-01" {
		t.Errorf("event agent = %q, want agent-01", e.Agent)
	}
	p := payloadOf(t, e)
	if p["title"] != "Reminders page" || p["task"] != "Fix the bug" || p["model"] != "" {
		t.Errorf("agent_created payload = %+v", p)
	}
	if branch, _ := p["branch"].(string); branch == "" {
		t.Errorf("agent_created payload has no branch: %+v", p)
	}

	working, err := d.srv.memory().WorkingMemory(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if !slicesContain(working.ActiveAgents, "agent-01") {
		t.Errorf("working memory's activeAgents = %v, want agent-01 in it", working.ActiveAgents)
	}
}

func slicesContain(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Retiring an agent, however it happens, is the other half of the lifecycle:
// the project should stop counting it among who is active.
func TestMemoryCapturesAgentRetired(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	d.srv.addActiveAgent(ctx, "hello-stack", a.Name)

	if err := d.srv.retireOne(ctx, d.srv.manager(nil), a, api.RetireStop); err != nil {
		t.Fatal(err)
	}

	events := eventsOfType(t, d, "hello-stack", "agent_retired")
	if len(events) != 1 {
		t.Fatalf("agent_retired events = %d, want 1", len(events))
	}
	p := payloadOf(t, events[0])
	if p["how"] != api.RetireStop || p["branch"] != a.Branch {
		t.Errorf("agent_retired payload = %+v", p)
	}

	working, err := d.srv.memory().WorkingMemory(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if slicesContain(working.ActiveAgents, "agent-01") {
		t.Errorf("working memory's activeAgents still has agent-01 after it was retired: %v", working.ActiveAgents)
	}
}

// A genuine finish is a finish notice's payload, kept: the same diff stat, and
// a link to the report the agent filed, if it filed one.
func TestMemoryCapturesAgentFinishedWithItsReport(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	d.srv.addActiveAgent(ctx, "hello-stack", a.Name)
	summary := "Added pagination to the reminders list, twenty a page."
	saidLast(t, d, a, summary)
	if err := os.WriteFile(filepath.Join(a.Worktree, "server.mjs"), []byte("// paginated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := d.srv.memory().AddReport(ctx, memory.Report{
		Project: "hello-stack", Agent: "agent-01", Task: "Reminders page",
		Status: memory.StatusDone, Summary: "Paginated the reminders list.",
	})
	if err != nil {
		t.Fatal(err)
	}

	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})

	events := eventsOfType(t, d, "hello-stack", "agent_finished")
	if len(events) != 1 {
		t.Fatalf("agent_finished events = %d, want 1", len(events))
	}
	p := payloadOf(t, events[0])
	if p["reportId"] != report.ID {
		t.Errorf("agent_finished payload's reportId = %v, want %q", p["reportId"], report.ID)
	}
	if p["summary"] != summary {
		t.Errorf("agent_finished payload's summary = %v, want %q", p["summary"], summary)
	}
	if files, _ := p["files"].(float64); files != 1 {
		t.Errorf("agent_finished payload's files = %v, want 1", p["files"])
	}
	if dirty, _ := p["dirty"].(bool); !dirty {
		t.Errorf("agent_finished payload's dirty = %v, want true", p["dirty"])
	}

	working, err := d.srv.memory().WorkingMemory(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if slicesContain(working.ActiveAgents, "agent-01") {
		t.Errorf("working memory's activeAgents still has agent-01 after it finished: %v", working.ActiveAgents)
	}
}

// The chain a question travels — asked, answered or escalated — is worth its
// own trail, independent of whatever the agent that asked it goes on to do.
func TestMemoryCapturesQuestionEvents(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")

	asked := make(chan api.Question, 1)
	go func() {
		q, err := d.srv.askForTest(ctx, a, "Should the page paginate?", "building it")
		if err != nil {
			t.Errorf("ask: %v", err)
		}
		asked <- q
	}()
	id := waitForQuestion(t, d, "hello-stack")
	if _, err := d.srv.answerQuestion(ctx, id, "Yes, 20 per page.", "lead"); err != nil {
		t.Fatal(err)
	}
	<-asked

	askedEvents := eventsOfType(t, d, "hello-stack", "question_asked")
	if len(askedEvents) != 1 || payloadOf(t, askedEvents[0])["question"] != "Should the page paginate?" {
		t.Errorf("question_asked events = %+v", askedEvents)
	}
	answeredEvents := eventsOfType(t, d, "hello-stack", "question_answered")
	if len(answeredEvents) != 1 {
		t.Fatalf("question_answered events = %d, want 1", len(answeredEvents))
	}
	p := payloadOf(t, answeredEvents[0])
	if p["answer"] != "Yes, 20 per page." || p["answeredBy"] != "lead" {
		t.Errorf("question_answered payload = %+v", p)
	}

	go func() {
		q, err := d.srv.askForTest(ctx, a, "Which payment provider?", "")
		if err != nil {
			t.Errorf("ask: %v", err)
		}
		asked <- q
	}()
	id = waitForQuestion(t, d, "hello-stack")
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	if _, err := lead.LeadEscalateQuestion(ctx, id, "this is a product decision"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.AnswerQuestion(ctx, "hello-stack", id, "Stripe."); err != nil {
		t.Fatal(err)
	}
	<-asked

	escalatedEvents := eventsOfType(t, d, "hello-stack", "question_escalated")
	if len(escalatedEvents) != 1 {
		t.Fatalf("question_escalated events = %d, want 1", len(escalatedEvents))
	}
	if p := payloadOf(t, escalatedEvents[0]); p["why"] != "this is a product decision" {
		t.Errorf("question_escalated payload = %+v", p)
	}
}

// Notes are the one thing every agent's brief carries, so a change to them is
// worth a row of its own.
func TestMemoryCapturesNotesChanged(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetNotes(ctx, "hello-stack", "Squash before merging.\n"); err != nil {
		t.Fatal(err)
	}

	events := eventsOfType(t, d, "hello-stack", "notes_changed")
	if len(events) != 1 {
		t.Fatalf("notes_changed events = %d, want 1", len(events))
	}
	p := payloadOf(t, events[0])
	if !strings.Contains(p["preview"].(string), "Squash before merging.") {
		t.Errorf("notes_changed payload = %+v", p)
	}
}

// A screenshot or recording already lands in media; the project's memory
// should carry a reference to it, not a copy.
func TestMemoryCapturesArtifactForNewMedia(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	a := addTestAgent(t, d)
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	inAgent := api.NewClient(d.srv.agentSocketPath(a.Instance))

	item, err := inAgent.AddNote(ctx, "", api.NoteRequest{Text: "Implemented the list."})
	if err != nil {
		t.Fatal(err)
	}

	artifacts, err := d.srv.memory().Artifacts(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].Type != "media" || !strings.Contains(artifacts[0].Path, item.ID) {
		t.Fatalf("Artifacts() = %+v", artifacts)
	}

	events := eventsOfType(t, d, "hello-stack", "artifact_created")
	if len(events) != 1 {
		t.Fatalf("artifact_created events = %d, want 1", len(events))
	}
	if events[0].ArtifactID != artifacts[0].ID {
		t.Errorf("artifact_created event's artifactId = %q, want %q", events[0].ArtifactID, artifacts[0].ID)
	}
}

// Merging a pull request is a chokepoint the daemon drives itself, unlike one
// opened on GitHub directly, which is why it — and not "opened" — is captured.
func TestMemoryCapturesPRMerged(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	// Pushed under a name of its own: the agent is found by its commit.
	head := commitOn(t, a, "reminders.txt")

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9" && r.Method == http.MethodGet:
			fmt.Fprintf(w, `{"number":9,"title":"Reminders page","state":"open","draft":false,"html_url":"https://github.com/acme/hello-stack/pull/9","base":{"ref":"main"},"head":{"ref":"feat/reminders","sha":%q}}`, head)
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			w.Write([]byte(`{"total_count":0}`))
		case r.URL.Path == "/repos/acme/hello-stack/pulls/9/merge" && r.Method == http.MethodPut:
			w.Write([]byte(`{"sha":"def","merged":true,"message":"Pull Request successfully merged"}`))
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer stub.Close()
	t.Setenv("AGENTBOX_GITHUB_API", stub.URL)

	if _, err := d.client.MergePullRequest(ctx, "hello-stack", 9, "squash"); err != nil {
		t.Fatal(err)
	}

	events := eventsOfType(t, d, "hello-stack", "pr_merged")
	if len(events) != 1 {
		t.Fatalf("pr_merged events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Agent != "agent-01" {
		t.Errorf("pr_merged event's agent = %q, want agent-01", e.Agent)
	}
	p := payloadOf(t, e)
	if p["number"] != float64(9) || p["branch"] != "feat/reminders" {
		t.Errorf("pr_merged payload = %+v", p)
	}
}

// A lead's own turns are the one thing chat.Manager.Finished never reports
// (it skips the lead deliberately), so this is captured off the same Publish
// stream every chat item already goes through.
func TestMemoryCapturesLeadTurn(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")

	go d.srv.askForTest(ctx, a, "Should the page paginate?", "building it")
	waitForQuestion(t, d, "hello-stack")

	events := eventsOfType(t, d, "hello-stack", "lead_turn")
	if len(events) != 1 {
		t.Fatalf("lead_turn events = %d, want 1", len(events))
	}
	p := payloadOf(t, events[0])
	if !strings.Contains(p["userMessage"].(string), "Should the page paginate?") {
		t.Errorf("lead_turn payload = %+v", p)
	}
	if events[0].Agent != "" {
		t.Errorf("lead_turn event's agent = %q, want empty: it is about the project, not an agent", events[0].Agent)
	}
}
