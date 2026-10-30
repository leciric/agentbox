package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentbox/internal/state"
)

// placedAgent is a project with one agent in it, ready to be placed somewhere.
func placedAgent(t *testing.T, st *state.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: t.TempDir(), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddAgent(ctx, state.Agent{
		Project: "pawly", Name: "agent-01", Instance: "ab-pawly-agent-01", AI: "claude",
		Branch: "agentbox/agent-01", BaseRef: "main", BaseCommit: "abc", Worktree: "/home/agent/agentbox/pawly",
		Status: "running", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPlaceAgentRecordsWhereItsMachineRuns(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	placedAgent(t, st)

	if err := st.PlaceAgent(ctx, "pawly", "agent-01", "k3d", "/workspace", state.DesiredRunning); err != nil {
		t.Fatalf("PlaceAgent() = %v", err)
	}
	got, err := st.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cluster != "k3d" || !got.OnCluster() {
		t.Errorf("cluster = %q, want k3d", got.Cluster)
	}
	if got.Runtime != state.RuntimeKubernetes {
		t.Errorf("runtime = %q, want %q", got.Runtime, state.RuntimeKubernetes)
	}
	if got.Workspace != "/workspace" || got.DesiredState != state.DesiredRunning {
		t.Errorf("workspace = %q, desired = %q", got.Workspace, got.DesiredState)
	}

	// Replacing the pod forgets the one the row named: a stale UID would make
	// the reconciler think a recreated pod is the one an agent's session is in.
	if err := st.SetAgentPod(ctx, "pawly", "agent-01", "ab-pawly-agent-01", "uid-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.PlaceAgent(ctx, "pawly", "agent-01", "k3d", "/workspace", state.DesiredStopped); err != nil {
		t.Fatal(err)
	}
	got, err = st.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Pod != "" || got.PodUID != "" {
		t.Errorf("pod = %q uid = %q, want both forgotten", got.Pod, got.PodUID)
	}
	if got.DesiredState != state.DesiredStopped {
		t.Errorf("desired = %q, want stopped", got.DesiredState)
	}

	// An empty cluster puts it back on this host, which is an Incus instance
	// and has no runtime to name.
	if err := st.PlaceAgent(ctx, "pawly", "agent-01", "", "", ""); err != nil {
		t.Fatal(err)
	}
	got, err = st.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.OnCluster() || got.Runtime != "" || got.DesiredState != state.DesiredRunning {
		t.Errorf("placing an agent back on the host = %+v", got)
	}
}

func TestSetAgentPodIsWhatTheClusterSays(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	placedAgent(t, st)
	if err := st.PlaceAgent(ctx, "pawly", "agent-01", "k3d", "/workspace", state.DesiredRunning); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentPod(ctx, "pawly", "agent-01", "ab-pawly-agent-01", "uid-1", "ImagePullBackOff: no such image"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Pod != "ab-pawly-agent-01" || got.PodUID != "uid-1" {
		t.Errorf("pod = %q uid = %q", got.Pod, got.PodUID)
	}
	if want := "ImagePullBackOff: no such image"; got.StatusDetail != want {
		t.Errorf("detail = %q, want %q", got.StatusDetail, want)
	}
	// When it is fine again, the reason goes.
	if err := st.SetAgentStatusDetail(ctx, "pawly", "agent-01", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Agent(ctx, "pawly", "agent-01"); got.StatusDetail != "" {
		t.Errorf("detail = %q, want it cleared", got.StatusDetail)
	}
}

func TestAgentsOnClusterAreItsReconcilerWork(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	placedAgent(t, st)
	if err := st.PlaceAgent(ctx, "pawly", "agent-01", "k3d", "/workspace", state.DesiredRunning); err != nil {
		t.Fatal(err)
	}
	// A second agent on the host is not the cluster's business.
	if err := st.AddAgent(ctx, state.Agent{
		Project: "pawly", Name: "agent-02", Instance: "ab-pawly-agent-02", AI: "claude",
		Branch: "agentbox/agent-02", BaseRef: "main", BaseCommit: "abc", Status: "running", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	agents, err := st.AgentsOnCluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Name != "agent-01" {
		t.Errorf("AgentsOnCluster() = %+v, want the one on it", agents)
	}
	clusters, err := st.ClustersInUse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0] != "k3d" {
		t.Errorf("ClustersInUse() = %v, want [k3d]", clusters)
	}
	// A destroyed agent stays in the cluster's list until its pod is gone.
	if err := st.MarkAgentDestroyed(ctx, "pawly", "agent-01", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := st.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.DesiredState != state.DesiredDestroyed || got.DestroyedAt.IsZero() {
		t.Errorf("desired = %q destroyed at = %v, want a destroyed agent with its clock", got.DesiredState, got.DestroyedAt)
	}
}

func TestPlacementOfAnAgentThatIsGoneSaysSo(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	err := st.PlaceAgent(ctx, "pawly", "agent-01", "k3d", "/workspace", state.DesiredRunning)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("PlaceAgent() = %v, want ErrNotFound", err)
	}
	if err := st.SetAgentPod(ctx, "pawly", "agent-01", "pod", "uid", ""); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("SetAgentPod() = %v, want ErrNotFound", err)
	}
}

// A destroy is final: a restore, snapshot or move that finishes after it would
// otherwise ask for its pod again, or name a claim nothing deletes.
func TestADestroyedAgentStaysDestroyed(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	placedAgent(t, st)
	if err := st.PlaceAgent(ctx, "pawly", "agent-01", "k3d", "/workspace", state.DesiredRunning); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentClaim(ctx, "pawly", "agent-01", "claim-a", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkAgentDestroyed(ctx, "pawly", "agent-01", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentDesiredState(ctx, "pawly", "agent-01", state.DesiredRunning); !errors.Is(err, state.ErrDestroyed) {
		t.Errorf("SetAgentDesiredState() = %v, want ErrDestroyed", err)
	}
	if err := st.SetAgentClaim(ctx, "pawly", "agent-01", "claim-b", ""); !errors.Is(err, state.ErrDestroyed) {
		t.Errorf("SetAgentClaim() = %v, want ErrDestroyed", err)
	}
	if err := st.MoveAgent(ctx, "pawly", "agent-01", state.MovePlacement{Cluster: "other", Claim: "claim-c"}); !errors.Is(err, state.ErrDestroyed) {
		t.Errorf("MoveAgent() = %v, want ErrDestroyed", err)
	}
	got, err := st.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.DesiredState != state.DesiredDestroyed || got.Claim != "claim-a" || got.Cluster != "k3d" {
		t.Errorf("row = desired %q claim %q cluster %q, want it destroyed on claim-a in k3d", got.DesiredState, got.Claim, got.Cluster)
	}
	// A missing agent is still ErrNotFound, not destroyed.
	if err := st.SetAgentDesiredState(ctx, "pawly", "agent-99", state.DesiredRunning); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("SetAgentDesiredState() of no agent = %v, want ErrNotFound", err)
	}
}
