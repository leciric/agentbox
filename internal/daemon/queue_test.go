package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// queueTestServer is the placement setup with the job runner a create needs:
// the tests here start real create jobs, which only Serve normally gives a
// daemon.
func queueTestServer(t *testing.T, projectCluster string, clusters map[string]api.ClusterCapacity) (*Server, state.Project) {
	t.Helper()
	srv, p := placementSetup(t, projectCluster, clusters)
	srv.jobs = newJobs(context.Background(), srv.store, srv.events)
	t.Cleanup(srv.jobs.wait)
	return srv, p
}

// queueTestAgent queues one agent of the setup's project, the way a create
// that asked to wait does, and returns its place in line.
func queueTestAgent(t *testing.T, srv *Server, name, reason string) state.QueuedAgent {
	t.Helper()
	ctx := context.Background()
	queue := true
	req, err := srv.checkCreate(ctx, api.CreateAgentRequest{
		Project: "pawly", Name: name, AI: "none", Queue: &queue, Title: "Reminders",
	}, false)
	if err != nil {
		t.Fatalf("checkCreate() = %v", err)
	}
	j, err := srv.queueCreate(ctx, req, false, reason)
	if err != nil {
		t.Fatalf("queueCreate() = %v", err)
	}
	srv.jobs.wait()
	job, err := srv.store.Job(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != api.JobSucceeded {
		t.Fatalf("the queueing job = %s (%s), want it to have answered with the agent", job.Status, job.Error)
	}
	var queued api.Agent
	if err := json.Unmarshal([]byte(job.Result), &queued); err != nil {
		t.Fatal(err)
	}
	if queued.State != state.AgentQueued || queued.QueuePosition != 1 || queued.QueueReason != reason {
		t.Fatalf("the job's agent = %+v, want it queued first with the reason", queued)
	}
	line, err := srv.store.LeftoverQueue(ctx, "pawly")
	if err != nil || len(line) != 1 {
		t.Fatalf("queue = %+v, %v", line, err)
	}
	return line[0]
}

// TestCreateQueuesWhenNoClusterHasRoom: an agent nothing can take is accepted,
// listed as queued with the reason, and started once a cluster has room, which
// is the one case a create that asked to wait doesn't fail.
func TestCreateQueuesWhenNoClusterHasRoom(t *testing.T) {
	srv, _ := queueTestServer(t, "k3d", map[string]api.ClusterCapacity{"k3d": {Agents: 0, Running: 2}})
	ctx := context.Background()
	q := queueTestAgent(t, srv, "agent-09", "all 2 of its agents are running")

	// The queue is what the app and the CLI read: the agent's state, its
	// place and the reason are on the agent itself.
	agents, err := srv.manager(nil).List(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].State != state.AgentQueued {
		t.Fatalf("List() = %+v, want the queued agent", agents)
	}
	info := srv.agentInfo(agents[0])
	if info.QueuePosition != 1 || info.QueueReason != "all 2 of its agents are running" {
		t.Errorf("listed agent = %+v, want its place and the reason", info)
	}

	// A full cluster is still full: the pass leaves it queued and starts
	// nothing.
	if err := srv.startQueuedAgent(ctx, q); err == nil {
		t.Error("a queued agent started while its only cluster was full")
	} else {
		var noRoom *NoRoomError
		if !errors.As(err, &noRoom) {
			t.Errorf("startQueuedAgent() = %v, want a NoRoomError", err)
		}
	}
	if a, err := srv.store.Agent(ctx, "pawly", "agent-09"); err != nil || a.Status != state.AgentQueued {
		t.Errorf("agent after a full pass = %+v, %v, want it still queued", a, err)
	}

	// Room: it starts, and the slot is taken out of the daemon's count before
	// the pod exists, so the next queued agent doesn't take it too.
	srv.setClusterCapacity("k3d", api.ClusterCapacity{Agents: 1, Running: 2, At: time.Now()})
	if err := srv.startQueuedAgent(ctx, q); err != nil {
		t.Fatalf("startQueuedAgent() = %v", err)
	}
	if room, _ := srv.clusterCapacity("k3d"); room.Agents != 0 {
		t.Errorf("capacity after starting one = %+v, want the slot reserved", room)
	}
	jobs, err := srv.store.Jobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var create *state.Job
	for i := range jobs {
		if jobs[i].Kind == "create" {
			create = &jobs[i]
		}
	}
	if create == nil || create.Target != "pawly" {
		t.Fatalf("jobs = %+v, want a create of pawly", jobs)
	}
	// Nothing here can make the machine (there is no repository), so the job
	// is stopped rather than left running behind the test.
	srv.jobs.mu.Lock()
	for _, j := range srv.jobs.byID {
		j.cancel()
	}
	srv.jobs.mu.Unlock()
	srv.jobs.wait()
}

// TestCreateWithoutQueueSaysWhatIsFull: the same create without asking to wait
// fails with the reason and the way to wait, and nothing is queued.
func TestCreateWithoutQueueSaysWhatIsFull(t *testing.T) {
	srv, _ := queueTestServer(t, "k3d", map[string]api.ClusterCapacity{"k3d": {Agents: 0, Running: 2}})
	ctx := context.Background()
	_, err := srv.createAgentJob(ctx, api.CreateAgentRequest{Project: "pawly", Name: "agent-09", AI: "none"}, false)
	var noRoom *NoRoomError
	if !errors.As(err, &noRoom) || err.Error() != "all 4 of its agents are running" {
		t.Fatalf("createAgentJob() = %v, want the cluster's reason", err)
	}
	if q, _ := srv.store.LeftoverQueue(ctx, "pawly"); len(q) != 0 {
		t.Errorf("queue = %+v, want nothing queued", q)
	}
	if _, err := srv.store.Agent(ctx, "pawly", "agent-09"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the agent = %v, want it not made", err)
	}
}

// TestQueueStartsInLine: the pass starts the agents it can and leaves the rest,
// oldest first, and a queue that empties leaves nothing behind.
func TestQueueStartsInLine(t *testing.T) {
	srv, _ := queueTestServer(t, "k3d", map[string]api.ClusterCapacity{"k3d": {Agents: 0, Running: 4}})
	ctx := context.Background()
	first := queueTestAgent(t, srv, "agent-09", "all 4 of its agents are running")
	// A second agent, second in line.
	queue := true
	req, err := srv.checkCreate(ctx, api.CreateAgentRequest{
		Project: "pawly", Name: "agent-10", AI: "none", Queue: &queue,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.queueCreate(ctx, req, false, "all 4 of its agents are running"); err != nil {
		t.Fatal(err)
	}
	srv.jobs.wait()
	line, err := srv.store.LeftoverQueue(ctx, "pawly")
	if err != nil || len(line) != 2 || line[1].Position != 2 {
		t.Fatalf("queue = %+v, %v", line, err)
	}

	// One slot: only the first starts, and the second is still waiting.
	srv.setClusterCapacity("k3d", api.ClusterCapacity{Agents: 1, Running: 4, At: time.Now()})
	srv.startQueuedAgents(ctx)
	srv.jobs.wait()
	line, err = srv.store.LeftoverQueue(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(line) != 1 || line[0].Name != "agent-10" || line[0].Position != 1 {
		t.Fatalf("queue after one pass = %+v, want agent-10 waiting", line)
	}
	if a, err := srv.store.Agent(ctx, "pawly", "agent-09"); err == nil && a.Status == state.AgentQueued {
		t.Errorf("agent-09 = %+v, want it out of the queue", a)
	}
	_ = first
	srv.jobs.mu.Lock()
	for _, j := range srv.jobs.byID {
		j.cancel()
	}
	srv.jobs.mu.Unlock()
	srv.jobs.wait()
}
