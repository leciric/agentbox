package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// drainRequest runs a drain through the router, the way the client does, and
// returns the job it started.
func drainRequest(t *testing.T, srv *Server, name, body string) api.Job {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/clusters/"+name+"/drain", strings.NewReader(body))
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("drain %s = %d: %s", name, rec.Code, rec.Body.String())
	}
	var j api.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	return j
}

// finishedClusterJob waits for a job and reads what it recorded, which is where
// its log and result are once the daemon has let it go.
func finishedClusterJob(t *testing.T, srv *Server, j api.Job) state.Job {
	t.Helper()
	srv.jobs.wait()
	job, err := srv.store.Job(context.Background(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// TestDrainKeepRunningMarksTheClusterAndLeavesTheAgents: --keep-running is the
// drain that only refuses new agents, so the ones there keep running and the
// room the daemon had counted for the cluster is forgotten.
func TestDrainKeepRunningMarksTheClusterAndLeavesTheAgents(t *testing.T) {
	srv, _ := queueTestServer(t, "", map[string]api.ClusterCapacity{"k3d": {Agents: 2}})
	ctx := context.Background()
	reconcilerAgent(t, srv.store, "agent-01")
	if _, ok := srv.clusterCapacity("k3d"); !ok {
		t.Fatal("the setup has no room for k3d, so forgetting it proves nothing")
	}

	j := drainRequest(t, srv, "k3d", `{"keepRunning":true}`)
	if j.Kind != "drain" || j.Target != "k3d" {
		t.Errorf("the job is %s %s, want drain k3d", j.Kind, j.Target)
	}
	job := finishedClusterJob(t, srv, j)
	if job.Status != api.JobSucceeded {
		t.Fatalf("the drain job = %s (%s), want it to succeed", job.Status, job.Error)
	}
	cluster, err := srv.store.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if !cluster.Draining {
		t.Error("the cluster is not draining after the job")
	}
	a, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if a.DesiredState != state.DesiredRunning {
		t.Errorf("agent-01 is %q after a keep-running drain, want it running", a.DesiredState)
	}
	if _, ok := srv.clusterCapacity("k3d"); ok {
		t.Error("the cluster's room is still cached after a drain")
	}
	if !strings.Contains(job.Log, "kept pawly/agent-01 running") {
		t.Errorf("the job log = %q, want it to say what it kept", job.Log)
	}
	var result api.Cluster
	if err := json.Unmarshal([]byte(job.Result), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Draining || result.Name != "k3d" {
		t.Errorf("the job's result = %+v, want a draining k3d", result)
	}
}

// TestDrainStopsEveryRunningAgent: with neither flag the drain stops what runs
// there and leaves a stopped agent alone, which is what the desired state says.
// The stop itself is the cluster's: the row asks for stopped here, and the
// reconciler deletes the pod on its next round, which this fake does not run.
func TestDrainStopsEveryRunningAgent(t *testing.T) {
	srv, _ := queueTestServer(t, "", map[string]api.ClusterCapacity{"k3d": {Agents: 2}})
	ctx := context.Background()
	reconcilerAgent(t, srv.store, "agent-01")
	reconcilerAgent(t, srv.store, "agent-02")
	reconcilerAgent(t, srv.store, "agent-03")
	if err := srv.store.SetAgentDesiredState(ctx, "pawly", "agent-02", state.DesiredStopped); err != nil {
		t.Fatal(err)
	}
	// A row made before desired_state existed asks for running by being empty.
	if err := srv.store.SetAgentDesiredState(ctx, "pawly", "agent-03", ""); err != nil {
		t.Fatal(err)
	}

	job := finishedClusterJob(t, srv, drainRequest(t, srv, "k3d", `{}`))
	if job.Status != api.JobSucceeded {
		t.Fatalf("the drain job = %s (%s), want it to succeed", job.Status, job.Error)
	}
	for name, want := range map[string]string{
		"agent-01": state.DesiredStopped,
		"agent-02": state.DesiredStopped,
		"agent-03": state.DesiredStopped,
	} {
		a, err := srv.store.Agent(ctx, "pawly", name)
		if err != nil {
			t.Fatal(err)
		}
		if a.DesiredState != want {
			t.Errorf("%s is %q after the drain, want %q", name, a.DesiredState, want)
		}
	}
	if !strings.Contains(job.Log, "stopped pawly/agent-01") {
		t.Errorf("the job log = %q, want it to say agent-01 was stopped", job.Log)
	}
	if !strings.Contains(job.Log, "stopped pawly/agent-03") {
		t.Errorf("the job log = %q, want it to treat an empty desired state as running", job.Log)
	}
	// A stopped agent had nothing to stop, and the log says so rather than
	// counting it as work.
	if !strings.Contains(job.Log, "skipped pawly/agent-02: it is not running") {
		t.Errorf("the job log = %q, want it to skip agent-02", job.Log)
	}
	cluster, err := srv.store.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if !cluster.Draining {
		t.Error("the cluster is not draining after stopping its agents")
	}
}

// TestDrainRefusesATargetThatTakesNoAgents: a cluster being drained, or the one
// being drained itself, is refused before anything is marked or stopped.
func TestDrainRefusesATargetThatTakesNoAgents(t *testing.T) {
	srv, _ := queueTestServer(t, "", map[string]api.ClusterCapacity{
		"k3d": {Agents: 2},
		"e2e": {Agents: 2},
	})
	ctx := context.Background()
	if err := srv.store.SetClusterDraining(ctx, "e2e", true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ body, want string }{
		{`{"moveTo":"e2e"}`, "cluster e2e is draining: it takes no new agents"},
		{`{"moveTo":"k3d"}`, "cluster k3d is the one being drained"},
		{`{"moveTo":"nope"}`, `no cluster named "nope"`},
		{`{"moveTo":"auto"}`, `a drain moves its agents to a cluster by name, not "auto"`},
		{`{"moveTo":"e2e","keepRunning":true}`, "can't move its agents and keep them running"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/clusters/k3d/drain", strings.NewReader(tc.body))
		srv.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("drain %s = %d: %s, want 400", tc.body, rec.Code, rec.Body.String())
			continue
		}
		var e api.Error
		if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(e.Error, tc.want) {
			t.Errorf("drain %s = %q, want %q", tc.body, e.Error, tc.want)
		}
	}
	// A refused drain touches nothing.
	cluster, err := srv.store.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if cluster.Draining {
		t.Error("k3d is draining after a drain that was refused")
	}
}

// TestUndrainLetsTheClusterTakeAgentsAgain: undrain clears the flag and the
// room the daemon counted before the drain.
func TestUndrainLetsTheClusterTakeAgentsAgain(t *testing.T) {
	srv, _ := queueTestServer(t, "", map[string]api.ClusterCapacity{"k3d": {Agents: 2}})
	ctx := context.Background()
	if err := srv.store.SetClusterDraining(ctx, "k3d", true); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/clusters/k3d/undrain", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("undrain = %d: %s", rec.Code, rec.Body.String())
	}
	var out api.Cluster
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Draining {
		t.Error("undrain answered with a draining cluster")
	}
	cluster, err := srv.store.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if cluster.Draining {
		t.Error("the cluster is still draining after undrain")
	}
	if _, ok := srv.clusterCapacity("k3d"); ok {
		t.Error("the room from before the drain is still cached")
	}
}
