package daemon

import (
	"context"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/memory"
)

// The project's tasks are a list only the user manages. Nothing in the
// daemon writes one on its own — not an agent's creation, not its finish —
// and neither an agent nor the project's chat can write one over its socket.

func taskOf(t *testing.T, d testDaemon, project, id string) memory.Task {
	t.Helper()
	task, err := d.srv.memory().Task(context.Background(), project, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// An agent's finish used to close its task from its report. The user's task
// is the user's to close: whatever the agent reported, it stays as it was.
func TestAgentFinishedLeavesItsTaskAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	task, err := d.srv.memory().AddTask(ctx, memory.Task{Project: "hello-stack", Agent: a.Name, Goal: "Paginate the reminders list"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.memory().AddReport(ctx, memory.Report{Project: "hello-stack", Agent: a.Name,
		Status: memory.StatusDone, Summary: "Paginated."}); err != nil {
		t.Fatal(err)
	}

	d.srv.captureAgentFinished(ctx, a, api.AgentChanges{}, nil, "Paginated.")

	after := taskOf(t, d, "hello-stack", task.ID)
	if after.Status != memory.TaskOpen || after.Detail != task.Detail || !after.UpdatedAt.Equal(task.UpdatedAt) {
		t.Errorf("the task after its agent finished = %+v, want it untouched", after)
	}
	tasks, err := d.srv.memory().Tasks(ctx, "hello-stack", memory.TaskFilter{})
	if err != nil || len(tasks) != 1 {
		t.Errorf("the task list after a finish = %d tasks, %v; want the user's one", len(tasks), err)
	}
}

// The user's routes write the list; a project's chat and an agent only read it.
func TestOnlyTheUserWritesTasks(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.serveLeadAPI(a.Project); err != nil {
		t.Fatal(err)
	}
	user := d.client.ProjectMemory(a.Project)
	lead := api.NewClient(d.srv.leadSocketPath(a.Project)).LeadMemory()
	agent := api.NewClient(d.srv.agentSocketPath(a.Instance)).SelfMemory()

	index, err := user.AddTask(ctx, api.AddTaskRequest{Goal: "Index the count query"})
	if err != nil {
		t.Fatal(err)
	}
	paginate, err := user.AddTask(ctx, api.AddTaskRequest{Goal: "Paginate the reminders list", Agent: a.Name})
	if err != nil {
		t.Fatal(err)
	}
	goal := "Paginate the reminders list, cursor-based"
	if got, err := user.UpdateTask(ctx, paginate.ID, api.UpdateTaskRequest{Goal: &goal}); err != nil || got.Goal != goal {
		t.Errorf("the user editing a task = %+v, %v", got, err)
	}

	// Both of the others read it.
	for who, m := range map[string]*api.MemoryClient{"the lead": lead, "an agent": agent} {
		if tasks, err := m.Tasks(ctx, api.TaskQuery{}); err != nil || len(tasks) != 2 {
			t.Errorf("%s read %d tasks, %v; want both", who, len(tasks), err)
		}
	}
	// And neither writes it, not even an agent its own task.
	for who, m := range map[string]*api.MemoryClient{"the lead": lead, "an agent": agent} {
		if _, err := m.AddTask(ctx, api.AddTaskRequest{Goal: "Something else"}); err == nil {
			t.Errorf("%s wrote a task down", who)
		}
		if _, err := m.UpdateTask(ctx, paginate.ID, api.UpdateTaskRequest{Status: ptr(api.TaskDone)}); err == nil {
			t.Errorf("%s closed a task", who)
		}
		if err := m.LinkTasks(ctx, paginate.ID, index.ID); err == nil {
			t.Errorf("%s linked two tasks", who)
		}
		if err := m.DeleteTask(ctx, index.ID); err == nil {
			t.Errorf("%s deleted a task", who)
		}
	}
	if got := taskOf(t, d, a.Project, paginate.ID); got.Status != memory.TaskOpen || len(got.DependsOn) != 0 {
		t.Errorf("the task after the refusals = %+v", got)
	}

	if err := user.DeleteTask(ctx, index.ID); err != nil {
		t.Fatal(err)
	}
	if tasks, err := user.Tasks(ctx, api.TaskQuery{}); err != nil || len(tasks) != 1 || tasks[0].ID != paginate.ID {
		t.Errorf("the list after the user deleted one = %+v, %v", tasks, err)
	}
}

// The routes that write the list are on neither an agent's socket nor a
// project chat's.
func TestTaskRoutesOnlyTheUserGets(t *testing.T) {
	t.Parallel()
	routes := map[string]struct{ inAgent, userOnly bool }{}
	for _, route := range memoryRoutes {
		routes[route.action] = struct{ inAgent, userOnly bool }{route.inAgent, route.userOnly}
	}
	for action, want := range map[string]struct{ inAgent, userOnly bool }{
		"tasks": {true, false}, "task": {true, false},
		"add-task": {false, true}, "update-task": {false, true}, "delete-task": {false, true},
		"link-tasks": {false, true}, "unlink-tasks": {false, true},
		"start-task": {false, true}, "unqueue-task": {false, true},
	} {
		got, ok := routes[action]
		if !ok {
			t.Errorf("there is no %q memory route", action)
			continue
		}
		if got != want {
			t.Errorf("%q = %+v, want %+v", action, got, want)
		}
	}
}

// Deleting a queued task takes its queued agent out of the queue: the work is
// gone, so nothing should start for it.
func TestDeletingAQueuedTaskUnqueuesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := newQueueTest(t, 4*gib, map[string]int64{"p": 4 * gib}, runningInstances())
	q.addProject(t, "p")
	q.enqueue(t, "p", "q1")
	task, err := q.srv.memory().AddTask(ctx, memory.Task{Project: "p", Agent: "q1", Goal: "Fix the login redirect"})
	if err != nil {
		t.Fatal(err)
	}

	if err := q.client.ProjectMemory("p").DeleteTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.srv.store.Agent(ctx, "p", "q1"); err == nil {
		t.Error("the queued agent for a deleted task is still there")
	}
	if queued, _ := q.srv.store.Queue(ctx, "p"); len(queued) != 0 {
		t.Errorf("still queued: %v", queued)
	}
	if _, err := q.srv.memory().Task(ctx, "p", task.ID); err == nil {
		t.Error("the task is still on the list")
	}
}
