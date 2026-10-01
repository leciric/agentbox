package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

const gib = int64(1) << 30

// queueTest is a daemon whose queue starts nothing for real: starting a
// queued agent is recorded, and marked as starting the way the real create
// job marks it, and the budget and every project's peak are the test's.
type queueTest struct {
	testDaemon
	mu      sync.Mutex
	started []string
}

func (q *queueTest) startedSoFar() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.started...)
}

// runningInstances is `incus list` for these instances, all running.
func runningInstances(names ...string) string {
	var rows []string
	for _, n := range names {
		rows = append(rows, fmt.Sprintf(`{"name":%q,"status":"Running","config":{},"expanded_config":{}}`, n))
	}
	return "[" + strings.Join(rows, ",") + "]"
}

func newQueueTest(t *testing.T, budget int64, peaks map[string]int64, instances string) *queueTest {
	t.Helper()
	q := &queueTest{}
	q.testDaemon = startTestDaemon(t, t.TempDir(), cpuBudgetIncus, testConfig{instances: instances, queue: func(s *Server) {
		s.queueEvery = 0 // the test looks at the queue itself
		if err := s.store.SetFlag(context.Background(), state.SettingAgentQueue, true); err != nil {
			t.Fatal(err)
		}
		s.slotBudget = func(context.Context) (int64, error) { return budget, nil }
		s.projectPeak = func(_ context.Context, project string) (int64, bool, error) { return peaks[project], true, nil }
		s.queueStart = func(_ context.Context, a state.QueuedAgent) error {
			q.mu.Lock()
			q.started = append(q.started, a.Ref())
			q.mu.Unlock()
			s.setQueueStarting(a.Ref(), true)
			return nil
		}
	}})
	return q
}

func (q *queueTest) addProject(t *testing.T, name string) {
	t.Helper()
	if err := q.srv.store.AddProject(context.Background(), state.Project{Name: name, Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// addRunning adds a ready agent whose machine is agent.InstanceName's.
func (q *queueTest) addRunning(t *testing.T, project, name string) {
	t.Helper()
	a := state.Agent{Project: project, Name: name, Instance: agent.InstanceName(project, name), AI: "none",
		Branch: "agentbox/" + name, Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now()}
	if err := q.srv.store.AddAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func (q *queueTest) enqueue(t *testing.T, project, name string) {
	t.Helper()
	a := state.Agent{Project: project, Name: name, Instance: agent.InstanceName(project, name), AI: "none",
		Branch: "agentbox/" + name, Status: state.AgentQueued, CreatedAt: time.Now()}
	req, _ := json.Marshal(queuedRequest{Request: api.CreateAgentRequest{Project: project, AI: "none", Task: "do " + name}})
	if err := q.srv.store.Enqueue(context.Background(), a, req); err != nil {
		t.Fatal(err)
	}
}

func (q *queueTest) slots(t *testing.T) map[string]api.ProjectSlots {
	t.Helper()
	status, err := q.client.Queue(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]api.ProjectSlots{}
	for _, p := range status.Projects {
		out[p.Project] = p
	}
	return out
}

func sameList(got []string, want ...string) bool {
	return strings.Join(got, ",") == strings.Join(want, ",")
}

// The example the queue was asked for, driven through the admission loop:
// organic's agents peak at 6 GiB and agentbox's at 2 GiB, on an 18 GiB
// budget. With both busy, organic runs 1 and agentbox 4; a queued agent
// starts only as a slot frees, in its project's order, and only once.
func TestAdmitQueuedStartsIntoFreeSlots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	instances := []string{
		agent.InstanceName("agentbox", "a1"), agent.InstanceName("agentbox", "a2"),
		agent.InstanceName("agentbox", "a3"), agent.InstanceName("agentbox", "a4"),
		agent.InstanceName("organic", "o1"),
	}
	q := newQueueTest(t, 18*gib, map[string]int64{"organic": 6 * gib, "agentbox": 2 * gib}, runningInstances(instances...))
	q.addProject(t, "agentbox")
	q.addProject(t, "organic")
	for _, n := range []string{"a1", "a2", "a3", "a4"} {
		q.addRunning(t, "agentbox", n)
	}
	q.addRunning(t, "organic", "o1")
	for _, n := range []string{"a5", "a6", "a7"} {
		q.enqueue(t, "agentbox", n)
	}
	q.enqueue(t, "organic", "o2")

	slots := q.slots(t)
	if slots["organic"].Slots != 1 || slots["agentbox"].Slots != 4 {
		t.Fatalf("slots: organic %d, agentbox %d; want 1 and 4", slots["organic"].Slots, slots["agentbox"].Slots)
	}
	if slots["agentbox"].Running != 4 || slots["agentbox"].Queued != 3 {
		t.Errorf("agentbox: %+v, want 4 running and 3 queued", slots["agentbox"])
	}

	// Every slot is taken: nothing starts, and nothing running is touched.
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); len(got) != 0 {
		t.Fatalf("started %v with every slot taken", got)
	}

	// One of agentbox's agents stops: its next in line starts, and only it.
	q.setInstances(t, runningInstances(instances[1:]...))
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); !sameList(got, "agentbox/a5") {
		t.Fatalf("after a stop, started %v; want agentbox/a5", got)
	}
	// While it's being made it holds the slot, so looking again starts
	// nothing more, and it isn't started twice.
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); !sameList(got, "agentbox/a5") {
		t.Fatalf("looking again started %v; want still only agentbox/a5", got)
	}

	// organic's only running agent stops: it has work queued, so it gets its
	// slot back and its queued agent starts.
	q.setInstances(t, runningInstances(instances[1:4]...))
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); !sameList(got, "agentbox/a5", "organic/o2") {
		t.Fatalf("after organic's stop, started %v; want agentbox/a5 then organic/o2", got)
	}
}

// The queue goes in its order, and moving an agent to the front changes who
// starts next.
func TestAdmitQueuedFollowsTheOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := newQueueTest(t, 64*gib, map[string]int64{"p": 2 * gib}, "[]")
	q.addProject(t, "p")
	if err := q.srv.store.SetProjectSlots(ctx, "p", 1); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"q1", "q2", "q3"} {
		q.enqueue(t, "p", n)
	}
	if _, err := q.client.MoveQueued(ctx, "p/q3", 1); err != nil {
		t.Fatal(err)
	}
	agents, err := q.client.Agents(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]int{}
	for _, a := range agents {
		if a.State != "queued" {
			t.Errorf("%s is %q, want queued", a.Name, a.State)
		}
		positions[a.Name] = a.QueuePosition
	}
	if positions["q3"] != 1 || positions["q1"] != 2 || positions["q2"] != 3 {
		t.Errorf("positions after moving q3 first: %v", positions)
	}

	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); !sameList(got, "p/q3") {
		t.Fatalf("started %v; want only p/q3, pinned to one slot", got)
	}

	// Removing one takes it out of line, and the rest close up.
	if err := q.client.RemoveQueued(ctx, "p/q1"); err != nil {
		t.Fatal(err)
	}
	status, err := q.client.Queue(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range status.Queued {
		names = append(names, fmt.Sprintf("%s#%d", a.Name, a.Position))
	}
	// q3 is still queued, as far as the store goes, until its job takes it.
	if !sameList(names, "q3#1", "q2#2") {
		t.Errorf("queue after removing q1: %v", names)
	}
	// One being started can't be removed from under its job.
	if err := q.client.RemoveQueued(ctx, "p/q3"); err == nil {
		t.Error("removed an agent that was starting")
	}
}

// A project that queues nothing sees nothing new: agents made without the
// queue go past its slots, and the loop leaves them all alone.
func TestAdmitQueuedLeavesUnqueuedProjectsAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	names := []string{agent.InstanceName("p", "a1"), agent.InstanceName("p", "a2"), agent.InstanceName("p", "a3")}
	q := newQueueTest(t, 4*gib, map[string]int64{"p": 4 * gib}, runningInstances(names...))
	q.addProject(t, "p")
	for _, n := range []string{"a1", "a2", "a3"} {
		q.addRunning(t, "p", n)
	}
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); len(got) != 0 {
		t.Fatalf("started %v", got)
	}
	slots := q.slots(t)
	if slots["p"].Slots != 1 || slots["p"].Running != 3 {
		t.Errorf("p: %+v, want 1 slot with 3 running", slots["p"])
	}
	if out := instanceStatus(t, q.testDaemon); out != "Running" {
		t.Errorf("an agent is %s, want every one still running", out)
	}
}

// Pinning a project's slots and turning on "always queue" go through the
// project's settings, and back.
func TestProjectQueueSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := newQueueTest(t, 16*gib, map[string]int64{"p": 2 * gib}, "[]")
	q.addProject(t, "p")
	p, err := q.client.UpdateProject(ctx, "p", api.UpdateProjectRequest{Slots: ptr(3), AlwaysQueue: ptr(true)})
	if err != nil || p.Slots != 3 || !p.AlwaysQueue {
		t.Fatalf("UpdateProject = %+v, %v", p, err)
	}
	if s := q.slots(t)["p"]; s.Slots != 3 || s.Pinned != 3 {
		t.Errorf("pinned: %+v, want 3", s)
	}
	if _, err := q.client.UpdateProject(ctx, "p", api.UpdateProjectRequest{Slots: ptr(-1)}); err == nil {
		t.Error("slots -1 was taken")
	}
	if p, err = q.client.UpdateProject(ctx, "p", api.UpdateProjectRequest{Slots: ptr(0)}); err != nil || p.Slots != 0 {
		t.Fatalf("back to auto = %+v, %v", p, err)
	}
	// Auto, alone, on 16 GiB less its 2 GiB reserve: seven of 2 GiB.
	if s := q.slots(t)["p"]; s.Slots != 7 || s.Pinned != 0 {
		t.Errorf("auto: %+v, want 7", s)
	}
}

// A queued create, end to end through the API: the agent has its name,
// title, branch and task at once and no machine, and the queue loop then
// makes it with the branch it was queued with. A task of the plan queued the
// same way is made for that task.
func TestQueuedCreateStartsWhenASlotIsFree(t *testing.T) {
	t.Parallel()
	// Both machines answer as running as soon as they're copied, which is
	// what a create waits for.
	two := `[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}},` +
		`{"name":"ab-hello-stack-agent-02","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}}}}]`
	d := startTestDaemon(t, t.TempDir(), recordingIncus, testConfig{instances: two})
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{AgentQueue: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	// Two slots, whatever this machine's memory says.
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{Slots: ptr(2)}); err != nil {
		t.Fatal(err)
	}
	job, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{
		Project: "hello-stack", AI: "none", Title: "Reminders page", Task: "Build the reminders page.", Queue: ptr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	var queuedAgent api.Agent
	if job.Kind != "queue" || job.Status != api.JobSucceeded || json.Unmarshal(job.Result, &queuedAgent) != nil {
		t.Fatalf("queueing answered %+v", job)
	}
	if queuedAgent.State != "queued" || queuedAgent.QueuePosition != 1 || queuedAgent.Branch != "agentbox/reminders-page" {
		t.Errorf("queued agent = %s #%d on %s", queuedAgent.State, queuedAgent.QueuePosition, queuedAgent.Branch)
	}

	task, err := d.srv.memory().AddTask(ctx, memoryTask("hello-stack", "Fix the login redirect"))
	if err != nil {
		t.Fatal(err)
	}
	job, err = d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", AI: "none", TaskID: task.ID, Queue: ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	var forTask api.Agent
	_ = json.Unmarshal(job.Result, &forTask)
	if forTask.Title != "Fix the login redirect" {
		t.Errorf("an agent for a task is titled %q", forTask.Title)
	}
	if got, _ := d.srv.memory().Task(ctx, "hello-stack", task.ID); got.Agent != forTask.Name {
		t.Errorf("the task is %q's, want the queued %s's", got.Agent, forTask.Name)
	}
	// Given to one agent, a task can't be queued again.
	if _, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", AI: "none", TaskID: task.ID, Queue: ptr(true)}); err == nil {
		t.Error("queued the same task twice")
	}

	// Nothing holds either slot, so the loop makes both.
	for _, a := range []api.Agent{queuedAgent, forTask} {
		waitFor(t, a.Name+" to be made", func() bool {
			got, err := d.srv.store.Agent(ctx, "hello-stack", a.Name)
			return err == nil && got.Status == state.AgentReady
		})
	}
	made, err := d.srv.store.Agent(ctx, "hello-stack", queuedAgent.Name)
	if err != nil {
		t.Fatal(err)
	}
	if made.Branch != queuedAgent.Branch {
		t.Errorf("made on %s, queued on %s", made.Branch, queuedAgent.Branch)
	}
	if _, err := os.Stat(made.Worktree); err != nil {
		t.Errorf("its worktree: %v", err)
	}
	// The task stays the user's: it's linked to its agent and otherwise left
	// as it was, and the agent made with a task of its own wrote none.
	if got, err := d.srv.memory().Task(ctx, "hello-stack", task.ID); err != nil || got.Status != memory.TaskOpen || got.Agent != forTask.Name {
		t.Errorf("the task once its agent started = %+v, %v; want it open and %s's", got, err, forTask.Name)
	}
	if tasks, err := d.srv.memory().Tasks(ctx, "hello-stack", memory.TaskFilter{}); err != nil || len(tasks) != 1 {
		t.Errorf("the task list after two creates = %d tasks, %v; want only the user's one", len(tasks), err)
	}
	if q, _ := d.srv.store.Queue(ctx, ""); len(q) != 0 {
		t.Errorf("still queued: %v", q)
	}
}

func memoryTask(project, goal string) memory.Task {
	return memory.Task{Project: project, Goal: goal, Status: memory.TaskOpen}
}

// With "agent queue" off, which it is until turned on, everything is as it
// was: a create asked to queue makes its agent now, and an agent left queued
// from when it was on starts whatever the slots say.
func TestQueueOffQueuesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := newQueueTest(t, 4*gib, map[string]int64{"p": 4 * gib}, runningInstances(agent.InstanceName("p", "a1")))
	q.addProject(t, "p")
	q.addRunning(t, "p", "a1")
	q.enqueue(t, "p", "q1")
	q.enqueue(t, "p", "q2")
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); len(got) != 0 {
		t.Fatalf("queue on, one slot taken: started %v", got)
	}
	settings, err := q.client.UpdateSettings(ctx, api.UpdateSettingsRequest{AgentQueue: ptr(false)})
	if err != nil || settings.AgentQueue {
		t.Fatalf("turning the queue off = %v, %v", settings.AgentQueue, err)
	}
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); !sameList(got, "p/q1", "p/q2") {
		t.Errorf("queue off: started %v, want both", got)
	}
	status, err := q.client.Queue(ctx, "")
	if err != nil || status.Enabled {
		t.Errorf("queue status enabled = %v, %v", status.Enabled, err)
	}
}

func TestQueueOffCreatesNow(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), recordingIncus, testConfig{instances: runningAgent01})
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{AlwaysQueue: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	job, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", AI: "none", Queue: ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != "create" {
		t.Errorf("with the queue off, a queued create started a %q job", job.Kind)
	}
	waitFor(t, "the job", func() bool { j, err := d.client.Job(ctx, job.ID); return err == nil && j.Done() })
}
