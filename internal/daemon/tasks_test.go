package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/testutil"
)

// The task graph's wiring to the agent lifecycle (D77).
// An agent is made for something and finishes having done some of it; these
// are the two ends of that, and the only places the daemon writes to the plan
// on its own.

// openTaskOf is the plan's one open row against an agent's name.
func openTaskOf(t *testing.T, d testDaemon, project, agent string) (memory.Task, bool) {
	t.Helper()
	tasks, err := d.srv.memory().Tasks(context.Background(), project,
		memory.TaskFilter{Agent: agent, OpenOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 {
		return memory.Task{}, false
	}
	return tasks[0], true
}

func taskOf(t *testing.T, d testDaemon, project, id string) memory.Task {
	t.Helper()
	task, err := d.srv.memory().Task(context.Background(), project, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// An agent made with a task is a row in the plan, not only a line in the
// history: something has to be able to say "that is still open".
func TestAgentCreatedWritesItsTaskDown(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")

	d.srv.captureAgentTask(ctx, a, "Paginate the reminders list\nTwenty a page, cursor-based.")

	task, ok := openTaskOf(t, d, "hello-stack", "agent-01")
	if !ok {
		t.Fatal("creating an agent for a task left the plan empty")
	}
	if task.Goal != "Paginate the reminders list" {
		t.Errorf("task goal = %q, want the first line of what it was handed", task.Goal)
	}
	if task.Status != memory.TaskActive {
		t.Errorf("task status = %q, want %q: the agent is on it", task.Status, memory.TaskActive)
	}
	if task.Detail == task.Goal {
		t.Error("the rest of what the agent was handed wasn't kept as the task's detail")
	}

	// The graph's own history lands in events like everything else.
	events := eventsOfType(t, d, "hello-stack", "task_created")
	if len(events) != 1 {
		t.Fatalf("task_created events = %d, want 1", len(events))
	}
	if p := payloadOf(t, events[0]); p["taskId"] != task.ID {
		t.Errorf("task_created payload = %+v, want task %s", p, task.ID)
	}
}

// A project's chat that wrote the work down before handing it over should get
// one task, not two: the agent's creation links to what is already there.
func TestAgentCreatedLinksATaskTheChatAlreadyWrote(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	planned, err := d.srv.memory().AddTask(ctx, memory.Task{
		Project: "hello-stack", Agent: "agent-01", Goal: "Paginate the reminders list",
	})
	if err != nil {
		t.Fatal(err)
	}

	d.srv.captureAgentTask(ctx, a, "Paginate the reminders list")

	tasks, err := d.srv.memory().Tasks(ctx, "hello-stack", memory.TaskFilter{Agent: "agent-01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("the plan has %d tasks for agent-01, want the one the chat wrote", len(tasks))
	}
	if tasks[0].ID != planned.ID {
		t.Errorf("a second task %s was written beside %s", tasks[0].ID, planned.ID)
	}
	if tasks[0].Status != memory.TaskActive {
		t.Errorf("the planned task is %q, want %q once an agent is on it", tasks[0].Status, memory.TaskActive)
	}
	changed := eventsOfType(t, d, "hello-stack", "task_status_changed")
	if len(changed) != 1 {
		t.Fatalf("task_status_changed events = %d, want 1", len(changed))
	}
	if p := payloadOf(t, changed[0]); p["from"] != memory.TaskOpen || p["to"] != memory.TaskActive {
		t.Errorf("task_status_changed payload = %+v", p)
	}
}

// What an agent said as it finished is what its task becomes. The two that
// leave it open are the point: a worker that stopped halfway must not have
// its work marked done because its turn ended.
func TestAgentFinishedClosesItsTaskFromItsReport(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	// One agent per case, because a report that leaves a task open leaves it
	// open for whatever reads that agent's plan next.
	for i, tc := range []struct {
		report string
		want   string
		open   bool
	}{
		{memory.StatusDone, memory.TaskDone, false},
		{memory.StatusFailed, memory.TaskAbandoned, false},
		{memory.StatusPartial, memory.TaskOpen, true},
		{memory.StatusBlocked, memory.TaskBlocked, true},
		{"", memory.TaskDone, false}, // no report at all: the turn completed, so the task did
	} {
		name := fmt.Sprintf("agent-0%d", i+1)
		a := addAgent(t, d, repo, "hello-stack", name, "Reminders page")
		task, err := d.srv.memory().AddTask(ctx, memory.Task{
			Project: "hello-stack", Agent: name, Status: memory.TaskActive,
			Goal: "Paginate the reminders list",
		})
		if err != nil {
			t.Fatal(err)
		}

		report := memory.Report{Status: tc.report, RemainingIssues: []string{"The count query scans the table"}}
		if tc.report == "" {
			report = memory.Report{}
		}
		d.srv.closeAgentTask(ctx, a, report)

		after := taskOf(t, d, "hello-stack", task.ID)
		if after.Status != tc.want {
			t.Errorf("a %q report left the task %q, want %q", tc.report, after.Status, tc.want)
		}
		if after.Open() != tc.open {
			t.Errorf("a %q report left the task open=%v, want %v", tc.report, after.Open(), tc.open)
		}
		if tc.open && !strings.Contains(after.Detail, "count query scans the table") {
			t.Errorf("a %q report left the task open without what is still wrong: %q", tc.report, after.Detail)
		}
	}
}

// A worker may say how its own task is going, and may not write work down for
// anybody else or move the plan about: curating it needs every agent in view.
func TestAgentMayOnlyUpdateItsOwnTask(t *testing.T) {
	t.Parallel()
	mine := memory.Task{ID: "task_mine", Agent: "agent-01", Goal: "Paginate the reminders list"}
	yours := memory.Task{ID: "task_yours", Agent: "agent-02", Goal: "Index the count query"}

	worker := memoryScope{project: "hello-stack", agent: "agent-01"}
	chat := memoryScope{project: "hello-stack"}

	if _, err := taskPatch(worker, yours, api.UpdateTaskRequest{Status: ptr(memory.TaskDone)}); err == nil {
		t.Error("a worker closed another agent's task")
	}
	if _, err := taskPatch(worker, mine, api.UpdateTaskRequest{Goal: ptr("Something else entirely")}); err == nil {
		t.Error("a worker rewrote what its task is")
	}
	if _, err := taskPatch(worker, mine, api.UpdateTaskRequest{ParentID: ptr("task_other")}); err == nil {
		t.Error("a worker moved its task in the plan")
	}
	patch, err := taskPatch(worker, mine, api.UpdateTaskRequest{
		Status: ptr(memory.TaskBlocked), Detail: ptr("waiting on the index"),
	})
	if err != nil {
		t.Fatalf("a worker couldn't say how its own task is going: %v", err)
	}
	if patch.Status == nil || *patch.Status != memory.TaskBlocked || patch.Detail == nil {
		t.Errorf("a worker's patch = %+v, want its status and detail", patch)
	}
	// The project's chat has every agent in view, so it may do all of it.
	if _, err := taskPatch(chat, yours, api.UpdateTaskRequest{
		Goal: ptr("Index it properly"), Agent: ptr("agent-03"), ParentID: ptr("task_other"),
	}); err != nil {
		t.Errorf("the project's chat couldn't curate the plan: %v", err)
	}
}

// The whole surface, end to end: the project's chat curates the plan, the
// agent reads it and says how its own is going, and the refusals are refusals.
func TestTaskGraphOnAllThreeSurfaces(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	user := d.client.ProjectMemory(a.Project)
	lead := api.NewClient(d.srv.leadSocketPath(a.Project)).LeadMemory()
	agent := api.NewClient(d.srv.agentSocketPath(a.Instance)).SelfMemory()

	// The chat writes the plan: one task for the agent, one it is waiting on.
	index, err := lead.AddTask(ctx, api.AddTaskRequest{Goal: "Index the count query"})
	if err != nil {
		t.Fatal(err)
	}
	paginate, err := lead.AddTask(ctx, api.AddTaskRequest{
		Goal: "Paginate the reminders list", Agent: a.Name, DependsOn: []string{index.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paginate.DependsOn) != 1 || paginate.DependsOn[0] != index.ID {
		t.Errorf("a task written down with its blocker = %+v", paginate)
	}

	// The agent reads the whole graph, both edges and all.
	mine, err := agent.Tasks(ctx, api.TaskQuery{Agent: a.Name, OpenOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].ID != paginate.ID {
		t.Fatalf("the agent saw %d of its own tasks, %v", len(mine), err)
	}
	blocker, err := agent.Task(ctx, index.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocker.Blocks) != 1 || blocker.Blocks[0] != paginate.ID {
		t.Errorf("the blocker doesn't know what waits on it: %+v", blocker)
	}

	// It may say how its own is going, and nothing more than that.
	if _, err := agent.UpdateTask(ctx, paginate.ID, api.UpdateTaskRequest{
		Status: ptr(api.TaskBlocked), Detail: ptr("waiting on the index"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.AddTask(ctx, api.AddTaskRequest{Goal: "Something for somebody else"}); err == nil {
		t.Error("an agent wrote work down for the project")
	}
	if err := agent.LinkTasks(ctx, index.ID, paginate.ID); err == nil {
		t.Error("an agent restructured the plan")
	}
	if _, err := agent.UpdateTask(ctx, index.ID, api.UpdateTaskRequest{Status: ptr(api.TaskDone)}); err == nil {
		t.Error("an agent closed a task that isn't its own")
	}

	// A cycle is refused wherever it is asked for, and says which edge.
	err = lead.LinkTasks(ctx, index.ID, paginate.ID)
	if err == nil {
		t.Fatal("a cycle was written down over the lead socket")
	}
	if !strings.Contains(err.Error(), paginate.ID) || !strings.Contains(err.Error(), index.ID) {
		t.Errorf("the refusal doesn't name the edge: %v", err)
	}

	// The user's routes see the same plan, and closing the blocker frees it.
	if _, err := user.UpdateTask(ctx, index.ID, api.UpdateTaskRequest{Status: ptr(api.TaskDone)}); err != nil {
		t.Fatal(err)
	}
	open, err := user.Tasks(ctx, api.TaskQuery{OpenOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ID != paginate.ID {
		t.Errorf("the plan after closing the blocker = %+v", open)
	}
}

// The trust split is also a routing fact: the routes that restructure the plan
// are not served inside an agent at all.
func TestTaskRoutesAnAgentGets(t *testing.T) {
	t.Parallel()
	inAgent := map[string]bool{}
	for _, route := range memoryRoutes {
		inAgent[route.action] = route.inAgent
	}
	for action, want := range map[string]bool{
		"tasks": true, "task": true, "update-task": true,
		"add-task": false, "link-tasks": false, "unlink-tasks": false,
	} {
		got, ok := inAgent[action]
		if !ok {
			t.Errorf("there is no %q memory route", action)
			continue
		}
		if got != want {
			t.Errorf("%q inAgent = %v, want %v", action, got, want)
		}
	}
}
