package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// enqueueSized queues an agent of a size, as if it had joined the queue at
// joined.
func (q *queueTest) enqueueSized(t *testing.T, project, name, size string, joined time.Time) {
	t.Helper()
	a := state.Agent{Project: project, Name: name, Instance: agent.InstanceName(project, name), AI: "none",
		Branch: "agentbox/" + name, Status: state.AgentQueued, CreatedAt: joined, Size: size}
	req, _ := json.Marshal(queuedRequest{Request: api.CreateAgentRequest{Project: project, AI: "none", Size: size}})
	if err := q.srv.store.Enqueue(context.Background(), a, req); err != nil {
		t.Fatal(err)
	}
}

// queueOff turns the agent queue's switch off: no slots, memory only.
func (q *queueTest) queueOff(t *testing.T) {
	t.Helper()
	if err := q.srv.store.SetFlag(context.Background(), state.SettingAgentQueue, false); err != nil {
		t.Fatal(err)
	}
}

func (q *queueTest) waiting(t *testing.T, ref string) string {
	t.Helper()
	status, err := q.client.Queue(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range status.Queued {
		if a.Ref == ref {
			return a.Waiting
		}
	}
	t.Fatalf("%s isn't queued", ref)
	return ""
}

// Two projects share the VM's memory: an agent of the project with the big
// baseline doesn't fit, waits, says why in numbers, and one of the other
// project, with a small baseline, starts into what is free meanwhile,
// whatever the queue switch says.
func TestAdmissionAcrossProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// 32 GiB, less the 4 the VM keeps and the 7 left for bursts: 21 to share.
	q := newQueueTest(t, 32*gib, map[string]int64{"p": 8 * gib, "r": 2 * gib},
		runningInstances(agent.InstanceName("p", "a1"), agent.InstanceName("p", "a2"), agent.InstanceName("r", "b1")))
	q.queueOff(t)
	q.addProject(t, "p")
	q.addProject(t, "r")
	q.addRunning(t, "p", "a1")
	q.addRunning(t, "p", "a2")
	q.addRunning(t, "r", "b1")
	now := time.Now()
	q.enqueueSized(t, "p", "big", agent.SizeHeavy, now.Add(-time.Minute))
	q.enqueueSized(t, "r", "small", agent.SizeLight, now)
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); !sameList(got, "r/small") {
		t.Fatalf("started %v, want only r's agent: p's needs 8 of the 3 GiB free", got)
	}
	want := "queued: 4 agents in 2 projects reserve 20 of 21 GB; starts when ~8 GB is free"
	if got := q.waiting(t, "p/big"); got != want {
		t.Errorf("p's agent waits because %q, want %q", got, want)
	}
	if ag, err := q.client.Agent(ctx, "p/big"); err != nil || ag.Waiting != want || ag.Size != agent.SizeHeavy {
		t.Errorf("the agent = waiting %q, size %q, %v", ag.Waiting, ag.Size, err)
	}
}

// A running agent counts as what it uses once that's more than its baseline,
// less what it holds of the burst pool: a heavy phase under a lease doesn't
// keep agents from starting.
func TestAdmissionCountsWhatAnAgentGrewTo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := newQueueTest(t, 32*gib, map[string]int64{"p": 2 * gib}, runningInstances(agent.InstanceName("p", "a1")))
	q.queueOff(t)
	q.addProject(t, "p")
	q.addRunning(t, "p", "a1")
	q.setUsage("p/a1", 20*gib)
	q.enqueueSized(t, "p", "q1", agent.SizeLight, time.Now())
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); len(got) != 0 {
		t.Fatalf("started %v with 20 of 21 GiB in use", got)
	}
	q.srv.burst.leases["p/a1"] = &burstLease{project: "p", bytes: 16 * gib, keys: map[string]time.Time{"test": time.Now().Add(time.Hour)}}
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); !sameList(got, "p/q1") {
		t.Errorf("started %v once a1's growth was under a lease, want p/q1", got)
	}
}

// Once the agent at the front has waited StarveAfter, smaller ones behind it
// no longer start ahead of it, though they'd fit.
func TestAdmissionDoesntStarveAHeavyAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := newQueueTest(t, 32*gib, map[string]int64{"p": 8 * gib, "r": 2 * gib},
		runningInstances(agent.InstanceName("p", "a1"), agent.InstanceName("p", "a2"), agent.InstanceName("r", "b1")))
	q.queueOff(t)
	q.addProject(t, "p")
	q.addProject(t, "r")
	q.addRunning(t, "p", "a1")
	q.addRunning(t, "p", "a2")
	q.addRunning(t, "r", "b1")
	q.enqueueSized(t, "p", "big", "", time.Now().Add(-agent.StarveAfter-time.Minute))
	q.enqueueSized(t, "r", "small", "", time.Now())
	q.srv.admitQueued(ctx)
	if got := q.startedSoFar(); len(got) != 0 {
		t.Fatalf("started %v ahead of the big agent that has waited long enough", got)
	}
	if got := q.waiting(t, "r/small"); !strings.Contains(got, "p/big has waited longest") {
		t.Errorf("the small agent waits because %q, want behind p/big", got)
	}
}

// A create that didn't ask to queue, with the queue off, still waits in it
// when the VM has no memory for it, and is told why.
func TestACreateThatDoesntFitQueues(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), recordingIncus, testConfig{instances: runningAgent01, queue: func(s *Server) {
		s.queueEvery = 0
		s.slotBudget = func(context.Context) (int64, error) { return 32 * gib, nil }
		s.projectPeak = func(context.Context, string) (int64, bool, error) { return 4 * gib, true, nil }
		s.projectShape = func(context.Context, string) (agent.Shape, error) {
			return agent.Shape{Baseline: 4 * gib, Burst: 2 * gib}, nil
		}
		s.queueStart = func(context.Context, state.QueuedAgent) error { return nil }
		// Another project's create, still making its machine, holds 18 GiB.
		s.pendingCreates[-1] = pendingCreate{project: "elsewhere", reserved: 18 * gib}
	}})
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	job, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", AI: "none", Queue: ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind == "create" {
		t.Fatal("a create the VM has no memory for started")
	}
	job = doneJob(t, d, job.ID)
	var ag api.Agent
	if err := json.Unmarshal(job.Result, &ag); err != nil {
		t.Fatal(err)
	}
	want := "queued: 1 agent in 1 project reserve 18 of 21 GB; starts when ~4 GB is free"
	if ag.State != state.AgentQueued || ag.Waiting != want {
		t.Errorf("the agent = %s, waiting %q; want queued, %q", ag.State, ag.Waiting, want)
	}

	// Once the other create needs less, the next fits behind the queued one,
	// and starts now.
	d.srv.mu.Lock()
	d.srv.pendingCreates[-1] = pendingCreate{project: "elsewhere", reserved: 2 * gib}
	d.srv.mu.Unlock()
	job, err = d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", AI: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != "create" {
		t.Errorf("a create that fits started a %q job", job.Kind)
	}
	pending := func() int {
		d.srv.mu.Lock()
		defer d.srv.mu.Unlock()
		return len(d.srv.pendingCreates)
	}
	if n := pending(); n != 2 {
		t.Errorf("%d creates hold memory while the second one's job runs, want 2", n)
	}
	// Its machine never comes up here: cancelled, its job gives its memory back.
	if _, err := d.client.CancelJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	doneJob(t, d, job.ID)
	waitFor(t, "the second create's memory back", func() bool { return pending() == 1 })
}

// The project's size for its chat's agents is kept, checked, and wins over
// the chat's own.
func TestProjectAgentSize(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{AgentSize: ptr("huge")}); err == nil {
		t.Error("a size that doesn't exist was kept")
	}
	p, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{AgentSize: ptr(agent.SizeHeavy)})
	if err != nil || p.AgentSize != agent.SizeHeavy {
		t.Fatalf("the project's size = %q, %v", p.AgentSize, err)
	}
	if p, err = d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{AgentSize: ptr(agent.SizeAuto)}); err != nil || p.AgentSize != "" {
		t.Errorf("auto is stored as %q, %v; want empty", p.AgentSize, err)
	}
}

func TestJoinOrder(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	got := joinOrder([]state.QueuedAgent{
		{Project: "p", Name: "p2", Position: 2, QueuedAt: at.Add(-time.Hour)}, // moved behind p1
		{Project: "p", Name: "p1", Position: 1, QueuedAt: at},
		{Project: "r", Name: "r1", Position: 1, QueuedAt: at.Add(-time.Minute)},
	})
	var names []string
	for _, q := range got {
		names = append(names, q.Name)
	}
	if !sameList(names, "r1", "p1", "p2") {
		t.Errorf("join order = %v, want each project's own order kept", names)
	}
}

// tightenMemory writes memory.high only on the agents it limits, and lifts it
// when there's room again.
func TestTightenMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := newQueueTest(t, 16*gib, map[string]int64{"p": 4 * gib},
		runningInstances(agent.InstanceName("p", "a1"), agent.InstanceName("p", "a2")))
	q.addProject(t, "p")
	q.addRunning(t, "p", "a1")
	q.addRunning(t, "p", "a2")
	highs := map[string]int64{}
	q.srv.setMemoryHigh = func(instance string, high int64) error { highs[instance] = high; return nil }
	q.setUsage("p/a1", 9*gib)
	q.srv.tightenMemory(ctx)
	a1, a2 := agent.InstanceName("p", "a1"), agent.InstanceName("p", "a2")
	// a1 gives up what's short; a2, at its reservation, keeps room above it.
	if highs[a1] == 0 || highs[a1] >= 9*gib || highs[a2] < 4*gib+agent.HighHeadroom(4*gib) {
		t.Errorf("with 1 of 14 GiB free, memory.high = %d on a1 (using 9), %d on a2; want a1 held below what it uses, a2 above its reservation", highs[a1], highs[a2])
	}
	q.setUsage("p/a1", 2*gib)
	q.srv.tightenMemory(ctx)
	if highs[a1] != 0 {
		t.Errorf("with room again, a1's memory.high = %d, want none", highs[a1])
	}
}

func doneJob(t *testing.T, d testDaemon, id string) api.Job {
	t.Helper()
	var job api.Job
	waitFor(t, "the job", func() bool {
		j, err := d.client.Job(context.Background(), id)
		job = j
		return err == nil && j.Done()
	})
	return job
}

// setUsage says ref was last seen using memory bytes.
func (q *queueTest) setUsage(ref string, memory int64) {
	q.srv.mu.Lock()
	defer q.srv.mu.Unlock()
	if q.srv.usageNow == nil {
		q.srv.usageNow = map[string]agent.AgentUsage{}
	}
	q.srv.usageNow[ref] = agent.AgentUsage{Memory: memory}
}
