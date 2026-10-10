package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// leadTaskProject is a project whose lead can be sent a message.
func leadTaskProject(t *testing.T) (testDaemon, state.Agent) {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(context.Background(), api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	return d, leadReadyToChat(t, d, "hello-stack")
}

// sentToLead is the user message in the lead's chat that carries goal, if any.
func sentToLead(t *testing.T, d testDaemon, lead state.Agent, goal string) string {
	t.Helper()
	th, err := d.srv.chat.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range th.Items {
		if it.Kind == "user" && strings.Contains(it.Text, goal) {
			return it.Text
		}
	}
	return ""
}

// "Tasks go to" is a new agent until somebody says otherwise, and takes only
// the two places a task can go.
func TestTaskTargetSetting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if s, err := d.client.Settings(ctx); err != nil || s.TaskTarget != api.TaskRouteAgent {
		t.Fatalf("a new installation's tasks go to %q, %v; want a new agent", s.TaskTarget, err)
	}
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{TaskTarget: new("robot")}); err == nil {
		t.Error("tasks were sent somewhere that isn't an agent or the lead")
	}
	if s, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{TaskTarget: new(api.TaskRouteLead)}); err != nil || s.TaskTarget != api.TaskRouteLead {
		t.Errorf("tasks go to %q, %v after choosing the lead", s.TaskTarget, err)
	}
}

// With the setting on the lead, starting a task sends it to the project's
// chat as a message and makes it the lead's, with no agent made for it.
func TestStartingATaskSendsItToTheLead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, lead := leadTaskProject(t)
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{TaskTarget: new(api.TaskRouteLead)}); err != nil {
		t.Fatal(err)
	}
	user := d.client.ProjectMemory("hello-stack")
	task, err := user.AddTask(ctx, api.AddTaskRequest{Goal: "Rework the settings page", Detail: "Split it into tabs."})
	if err != nil {
		t.Fatal(err)
	}

	out, err := user.StartTask(ctx, task.ID, api.StartTaskRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Target != api.TaskRouteLead || out.Job != nil {
		t.Errorf("the start = %+v, want it gone to the lead with no job", out)
	}
	if out.Task.Agent != state.LeadName || out.Task.Status != api.TaskActive {
		t.Errorf("the task after it went = %+v, want it the lead's and active", out.Task)
	}
	msg := sentToLead(t, d, lead, "Rework the settings page")
	if msg == "" || !strings.Contains(msg, "Split it into tabs.") || !strings.Contains(msg, task.ID) {
		t.Errorf("the lead was sent %q, want the task's goal, detail and id", msg)
	}
	if agents, _ := d.srv.store.Agents(ctx, "hello-stack"); len(agents) != 1 {
		t.Errorf("the project has %d agents, want only its lead", len(agents))
	}
	if _, err := user.StartTask(ctx, task.ID, api.StartTaskRequest{}); err == nil {
		t.Error("a task the lead already has was started again")
	}
}

// A task's own choice beats the setting, in both directions.
func TestATasksRouteOverridesTheSetting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, lead := leadTaskProject(t)
	user := d.client.ProjectMemory("hello-stack")
	task, err := user.AddTask(ctx, api.AddTaskRequest{Goal: "Audit the queue"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Route != "" {
		t.Fatalf("a new task's route = %q, want it following the setting", task.Route)
	}
	if _, err := user.UpdateTask(ctx, task.ID, api.UpdateTaskRequest{Route: new(api.TaskRouteLead)}); err != nil {
		t.Fatal(err)
	}
	// The setting says a new agent; the task says the lead.
	if out, err := user.StartTask(ctx, task.ID, api.StartTaskRequest{}); err != nil || out.Target != api.TaskRouteLead {
		t.Fatalf("the start = %+v, %v; want the lead", out, err)
	}
	if sentToLead(t, d, lead, "Audit the queue") == "" {
		t.Error("the lead wasn't sent the task")
	}

	// The setting says the lead; a task that chose an agent asks for one,
	// which is refused here for want of an image rather than sent to the lead.
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{TaskTarget: new(api.TaskRouteLead)}); err != nil {
		t.Fatal(err)
	}
	other, err := user.AddTask(ctx, api.AddTaskRequest{Goal: "Fix the login redirect"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := user.UpdateTask(ctx, other.ID, api.UpdateTaskRequest{Route: new(api.TaskRouteAgent)}); err != nil {
		t.Fatal(err)
	}
	_, _ = user.StartTask(ctx, other.ID, api.StartTaskRequest{Queue: true})
	if sentToLead(t, d, lead, "Fix the login redirect") != "" {
		t.Error("a task that chose an agent went to the lead")
	}
	if got := taskOf(t, d, "hello-stack", other.ID); got.Agent == state.LeadName || !got.LeadQueuedAt.IsZero() {
		t.Errorf("the agent's task = %+v, want it nowhere near the lead", got)
	}
}

// Nothing queues: a task for the lead started with Queue goes at once, and
// one an earlier release left waiting for the lead goes as the daemon starts.
func TestALeadTaskGoesAtOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, lead := leadTaskProject(t)
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{TaskTarget: new(api.TaskRouteLead)}); err != nil {
		t.Fatal(err)
	}
	user := d.client.ProjectMemory("hello-stack")
	task, err := user.AddTask(ctx, api.AddTaskRequest{Goal: "Plan the release"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := user.StartTask(ctx, task.ID, api.StartTaskRequest{Queue: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Target != api.TaskRouteLead || !out.Task.LeadQueuedAt.IsZero() || out.Task.Agent != state.LeadName {
		t.Fatalf("the start = %+v, want it the lead's at once", out)
	}
	if sentToLead(t, d, lead, "Plan the release") == "" {
		t.Fatal("the task didn't go to the lead")
	}
	if _, err := user.UnqueueTask(ctx, task.ID); err == nil {
		t.Error("unqueued a task that was never queued")
	}

	left, err := user.AddTask(ctx, api.AddTaskRequest{Goal: "Left behind"})
	if err != nil {
		t.Fatal(err)
	}
	queuedAt := time.Now().Add(-time.Hour)
	if _, err := d.srv.memory().UpdateTask(ctx, "hello-stack", left.ID, memory.TaskPatch{LeadQueuedAt: &queuedAt}); err != nil {
		t.Fatal(err)
	}
	d.srv.startLeftoverQueue(ctx)
	if sentToLead(t, d, lead, "Left behind") == "" {
		t.Fatal("the task left queued for the lead didn't go to it")
	}
	if got := taskOf(t, d, "hello-stack", left.ID); got.Agent != state.LeadName || !got.LeadQueuedAt.IsZero() {
		t.Errorf("the task left queued, once it went = %+v", got)
	}
}
