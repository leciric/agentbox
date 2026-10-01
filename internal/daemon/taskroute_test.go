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

// leadTaskProject is a project whose lead can be sent a message, with the
// agent queue on and no slots, so nothing queued ever starts on its own and
// the test looks at the queue itself.
func leadTaskProject(t *testing.T) (testDaemon, state.Agent) {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{queue: func(s *Server) {
		s.queueEvery = 0
		if err := s.store.SetFlag(context.Background(), state.SettingAgentQueue, true); err != nil {
			t.Fatal(err)
		}
		s.slotBudget = func(context.Context) (int64, error) { return 0, nil }
		s.queueStart = func(context.Context, state.QueuedAgent) error { return nil }
	}})
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

// Queued, a task for the lead waits behind the agents queued before it, takes
// no slot, and goes as soon as they've started; taken back out of the queue,
// it goes nowhere.
func TestALeadTaskWaitsItsTurnInTheQueue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, lead := leadTaskProject(t)
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{TaskTarget: new(api.TaskRouteLead)}); err != nil {
		t.Fatal(err)
	}
	ahead := state.Agent{Project: "hello-stack", Name: "agent-01", AI: "none", Branch: "agentbox/one",
		Status: state.AgentQueued, CreatedAt: time.Now().Add(-time.Minute)}
	if err := d.srv.store.Enqueue(ctx, ahead, []byte(`{}`)); err != nil {
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
	if out.Target != api.TaskRouteLead || out.Task.LeadQueuedAt.IsZero() || out.Task.Agent != "" {
		t.Fatalf("the queued start = %+v, want it waiting for the lead", out)
	}
	d.srv.admitQueued(ctx)
	if sentToLead(t, d, lead, "Plan the release") != "" {
		t.Fatal("the task went to the lead ahead of the agent queued before it")
	}
	if status, err := d.client.Queue(ctx, "hello-stack"); err != nil || len(status.Queued) != 1 {
		t.Errorf("the project's queue = %+v, %v; want only the one agent in it", status.Queued, err)
	}

	// Back to the backlog and into the queue again: it is behind the agent
	// still, and unqueueing really takes it out.
	if got, err := user.UnqueueTask(ctx, task.ID); err != nil || !got.LeadQueuedAt.IsZero() {
		t.Fatalf("unqueueing = %+v, %v", got, err)
	}
	if queue, _ := d.srv.memory().LeadQueue(ctx, ""); len(queue) != 0 {
		t.Fatalf("the lead's queue after unqueueing = %+v", queue)
	}
	if _, err := user.StartTask(ctx, task.ID, api.StartTaskRequest{Queue: true}); err != nil {
		t.Fatal(err)
	}

	// The agent ahead leaves the queue (it started, or was taken out): the
	// task is at the front, and goes.
	if err := d.srv.store.RemoveAgent(ctx, "hello-stack", "agent-01"); err != nil {
		t.Fatal(err)
	}
	d.srv.admitQueued(ctx)
	if sentToLead(t, d, lead, "Plan the release") == "" {
		t.Fatal("the task at the front of the queue didn't go to the lead")
	}
	if got := taskOf(t, d, "hello-stack", task.ID); got.Agent != state.LeadName || !got.LeadQueuedAt.IsZero() {
		t.Errorf("the task once it went = %+v, want it the lead's and out of the queue", got)
	}
}

func TestAgentsAhead(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	queue := []state.QueuedAgent{
		{Project: "p", Name: "a", QueuedAt: at.Add(-time.Minute)},
		{Project: "p", Name: "b", QueuedAt: at},
		{Project: "p", Name: "c", QueuedAt: at.Add(time.Minute)},
		{Project: "q", Name: "d", QueuedAt: at.Add(-time.Hour)},
	}
	task := memory.Task{Project: "p", LeadQueuedAt: at.Add(500 * time.Millisecond)}
	if n := agentsAhead(queue, task); n != 1 {
		t.Errorf("agents ahead = %d, want only the one queued a minute before (same-second ties go to the task)", n)
	}
}
