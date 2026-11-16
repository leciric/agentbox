package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
)

func todoTexts(list []api.Todo) string {
	var out []string
	for _, t := range list {
		mark := " "
		if t.Done {
			mark = "x"
		}
		out = append(out, "["+mark+"] "+t.Text)
	}
	return strings.Join(out, ", ")
}

// The user and the lead keep the same list: either adds, checks off,
// reorders and deletes, and the app hears each change.
func TestTodosAreOneListForTheUserAndTheLead(t *testing.T) {
	t.Parallel()
	d, lead := oneProject(t)
	ctx := context.Background()
	events, stop := d.srv.events.subscribe()
	defer stop()

	a, err := d.client.AddTodo(ctx, "web", "  Fix the login redirect  ")
	if err != nil || a.Text != "Fix the login redirect" || a.CreatedBy != "user" || a.Position != 1 {
		t.Fatalf("added %+v, %v", a, err)
	}
	for ev := range events {
		if ev.Type == api.EventTodo {
			break
		}
	}
	b, err := lead.LeadAddTodo(ctx, "Write the release notes")
	if err != nil || b.CreatedBy != "lead" || b.Position != 2 {
		t.Fatalf("lead added %+v, %v", b, err)
	}
	c, _ := d.client.AddTodo(ctx, "web", "Bump the Go version")
	done, err := lead.LeadUpdateTodo(ctx, a.ID, api.TodoRequest{Done: new(true)})
	if err != nil || !done.Done || done.DoneAt == nil {
		t.Fatalf("checked off %+v, %v", done, err)
	}
	list, err := d.client.ReorderTodos(ctx, "web", []string{c.ID, a.ID, b.ID})
	if got := todoTexts(list); err != nil || got != "[ ] Bump the Go version, [x] Fix the login redirect, [ ] Write the release notes" {
		t.Fatalf("reordered: %s, %v", got, err)
	}
	// An order made from a list that has changed since is refused.
	if _, err := d.client.ReorderTodos(ctx, "web", []string{c.ID, a.ID}); err == nil || !strings.Contains(err.Error(), "read it again") {
		t.Fatalf("a stale order: %v", err)
	}
	if err := lead.LeadRemoveTodo(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err := d.client.UpdateTodo(ctx, "web", a.ID, api.TodoRequest{Done: new(false), Text: str("Fix the login redirect on Safari")})
	if err != nil || reopened.Done || reopened.DoneAt != nil || reopened.Text != "Fix the login redirect on Safari" {
		t.Fatalf("reopened %+v, %v", reopened, err)
	}
	list, _ = lead.LeadTodos(ctx)
	if got := todoTexts(list); got != "[ ] Fix the login redirect on Safari, [ ] Write the release notes" {
		t.Fatalf("left: %s", got)
	}
	if list[0].Position != 1 || list[1].Position != 2 {
		t.Fatalf("positions: %+v", list)
	}
	counts, err := d.client.OpenTodoCounts(ctx)
	if err != nil || counts.Open["web"] != 2 {
		t.Fatalf("counts %+v, %v", counts, err)
	}
}

func TestTodoRequestsAreChecked(t *testing.T) {
	t.Parallel()
	d, _ := oneProject(t)
	ctx := context.Background()
	if _, err := d.client.AddTodo(ctx, "web", "   "); err == nil {
		t.Error("added an empty todo")
	}
	if _, err := d.client.AddTodo(ctx, "web", strings.Repeat("x", maxTodo+1)); err == nil {
		t.Error("added one too long")
	}
	if _, err := d.client.AddTodo(ctx, "nope", "x"); err == nil {
		t.Error("added to a project that doesn't exist")
	}
	if _, err := d.client.UpdateTodo(ctx, "web", "nope", api.TodoRequest{Done: new(true)}); err == nil {
		t.Error("checked off one that doesn't exist")
	}
	a, _ := d.client.AddTodo(ctx, "web", "x")
	if _, err := d.client.UpdateTodo(ctx, "web", a.ID, api.TodoRequest{Text: str("")}); err == nil {
		t.Error("emptied one")
	}
}

// Starting a todo creates an agent whose task is its text, records which
// agent it went to, and won't start a second one for it.
func TestATodoStartsAnAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), recordingIncus, testConfig{instances: runningAgents01And02})
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	todo, err := d.client.AddTodo(ctx, "hello-stack", "Add a dark mode toggle\n\nIn Settings, beside the language.")
	if err != nil {
		t.Fatal(err)
	}
	started, err := d.client.StartTodo(ctx, "hello-stack", todo.ID, api.TodoStartRequest{AI: "none"})
	if err != nil || started.Job.ID == "" || started.Todo.Job != started.Job.ID {
		t.Fatalf("started %+v, %v", started, err)
	}
	if _, err := d.client.StartTodo(ctx, "hello-stack", todo.ID, api.TodoStartRequest{}); err == nil {
		t.Fatal("started twice while its agent was being made")
	}
	job := waitJobDone(t, d, started.Job.ID)
	if job.Status != api.JobSucceeded {
		t.Fatalf("job %s: %s", job.Status, job.Error)
	}
	var agent string
	waitFor(t, "the todo to name its agent", func() bool {
		list, _ := d.client.Todos(ctx, "hello-stack")
		agent = list[0].Agent
		return agent != ""
	})
	a, err := d.client.Agent(ctx, "hello-stack/"+agent)
	if err != nil || a.Title != "Add a dark mode toggle" {
		t.Fatalf("agent %+v, %v", a, err)
	}
	if _, err := d.client.StartTodo(ctx, "hello-stack", todo.ID, api.TodoStartRequest{}); err == nil || !strings.Contains(err.Error(), agent) {
		t.Fatalf("started again while %s has it: %v", agent, err)
	}
}
