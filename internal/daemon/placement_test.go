package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// placementSetup is a daemon with one project, whose agents run where the test
// says, and the clusters it can choose between. A cluster's room is what the
// test wrote as its last look, which is fresh: placement trusts it rather than
// dialing a cluster the test has no fake client for.
func placementSetup(t *testing.T, projectCluster string, clusters map[string]api.ClusterCapacity) (*Server, state.Project) {
	t.Helper()
	srv := reconcilerServer(t)
	ctx := context.Background()
	p := state.Project{Name: "pawly", Root: t.TempDir(), CreatedAt: time.Now()}
	if err := srv.store.AddProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	for name, room := range clusters {
		cluster := state.Cluster{
			Name: name, Namespace: "agentbox", Image: "agentbox-agent:dev",
			MasterURL: "https://master.example.test", Tailnet: state.TailnetNone,
			MaxAgents: 4, Kubeconfig: []byte("sealed-for-a-test"), CreatedAt: time.Now(),
		}
		if err := srv.store.AddCluster(ctx, cluster); err != nil {
			t.Fatal(err)
		}
		room.At = time.Now()
		srv.setClusterCapacity(name, room)
	}
	if projectCluster != "" {
		if err := srv.store.SetProjectCluster(ctx, p.Name, projectCluster); err != nil {
			t.Fatal(err)
		}
	}
	p, err := srv.store.Project(ctx, p.Name)
	if err != nil {
		t.Fatal(err)
	}
	return srv, p
}

// TestPlacementFollowsTheProject: a create that names no cluster runs where its
// project said, and a project that said nothing keeps the agent on this host.
func TestPlacementFollowsTheProject(t *testing.T) {
	srv, p := placementSetup(t, "", nil)
	if got, err := srv.placeAgent(context.Background(), p, ""); err != nil || got != "" {
		t.Errorf("placeAgent() = %q, %v, want this host", got, err)
	}

	srv, p = placementSetup(t, "k3d", map[string]api.ClusterCapacity{"k3d": {Agents: 2}})
	if got, err := srv.placeAgent(context.Background(), p, ""); err != nil || got != "k3d" {
		t.Errorf("placeAgent() = %q, %v, want the project's k3d", got, err)
	}
	// "local" overrides it: it is how a caller says this machine when leaving
	// the field out would follow the project.
	if got, err := srv.placeAgent(context.Background(), p, state.ClusterLocal); err != nil || got != "" {
		t.Errorf("placeAgent(local) = %q, %v, want this host", got, err)
	}
	// A request that names one of its own wins over the project's.
	srv, p = placementSetup(t, "k3d", map[string]api.ClusterCapacity{
		"k3d": {Agents: 2},
		"eks": {Agents: 2},
	})
	if got, err := srv.placeAgent(context.Background(), p, "eks"); err != nil || got != "eks" {
		t.Errorf("placeAgent(eks) = %q, %v, want eks", got, err)
	}
}

// TestPlacementPicksTheMostRoom: auto takes the cluster with the most agents of
// room, and a tie goes to the project's own cluster before the name.
func TestPlacementPicksTheMostRoom(t *testing.T) {
	srv, p := placementSetup(t, "k3d", map[string]api.ClusterCapacity{
		"k3d": {Agents: 1, Running: 2, CPU: "2"},
		"eks": {Agents: 3, Running: 0, CPU: "8"},
	})
	if got, err := srv.placeAgent(context.Background(), p, state.ClusterAuto); err != nil || got != "eks" {
		t.Errorf("placeAgent(auto) = %q, %v, want eks, which has more room", got, err)
	}

	// Equal room: the project's cluster, not the alphabetically first name.
	srv, p = placementSetup(t, "xyz", map[string]api.ClusterCapacity{
		"abc": {Agents: 2},
		"xyz": {Agents: 2},
	})
	if got, err := srv.placeAgent(context.Background(), p, state.ClusterAuto); err != nil || got != "xyz" {
		t.Errorf("placeAgent(auto) = %q, %v, want the project's xyz on a tie", got, err)
	}

	// One more agent of room outranks more CPU: the agent is a whole pod.
	srv, p = placementSetup(t, "", map[string]api.ClusterCapacity{
		"small": {Agents: 2, CPU: "1"},
		"big":   {Agents: 1, CPU: "64"},
	})
	if got, err := srv.placeAgent(context.Background(), p, state.ClusterAuto); err != nil || got != "small" {
		t.Errorf("placeAgent(auto) = %q, %v, want small, which fits two more agents", got, err)
	}
}

// TestPlacementRefusesAFullCluster: a cluster at its ceiling is a reason, not a
// pod the cluster will leave Pending, and it is the one error a create that
// asked to wait queues instead of failing.
func TestPlacementRefusesAFullCluster(t *testing.T) {
	srv, p := placementSetup(t, "", map[string]api.ClusterCapacity{"k3d": {Agents: 0, Running: 4}})
	_, err := srv.placeAgent(context.Background(), p, "k3d")
	var noRoom *NoRoomError
	if !errors.As(err, &noRoom) {
		t.Fatalf("placeAgent(k3d) = %v, want a NoRoomError", err)
	}
	if !strings.Contains(err.Error(), "it is at its ceiling of 4 agents") {
		t.Errorf("the reason is %q, want it to say the cluster is at its ceiling", err)
	}

	// And auto over the only cluster that is full is the same answer.
	if _, err := srv.placeAgent(context.Background(), p, state.ClusterAuto); !errors.As(err, &noRoom) {
		t.Errorf("placeAgent(auto) = %v, want a NoRoomError", err)
	}
}

// TestPlacementSkipsAClusterThatCantPullTheProjectsImage: a project whose base
// save made an image runs it from the registry of the cluster that built it, so
// the other cluster is not a candidate.
func TestPlacementSkipsAClusterThatCantPullTheProjectsImage(t *testing.T) {
	srv, p := placementSetup(t, "", map[string]api.ClusterCapacity{
		"ka": {Agents: 1},
		"kb": {Agents: 5},
	})
	ctx := context.Background()
	for name, url := range map[string]string{"reg-a": "a.example.test/you", "reg-b": "b.example.test/you"} {
		if err := srv.store.AddRegistry(ctx, state.Registry{
			Name: name, URL: url, SealedUsername: []byte{}, SealedSecret: []byte{}, CA: []byte{}, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.store.SetClusterConfig(ctx, state.Cluster{
		Name: "ka", Namespace: "agentbox", Image: "agentbox-agent:dev", Registry: "reg-a",
		MasterURL: "https://master.example.test", Tailnet: state.TailnetNone, MaxAgents: 4,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetClusterConfig(ctx, state.Cluster{
		Name: "kb", Namespace: "agentbox", Image: "agentbox-agent:dev", Registry: "reg-b",
		MasterURL: "https://master.example.test", Tailnet: state.TailnetNone, MaxAgents: 4,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetProjectImage(ctx, state.ProjectImage{
		Project: "pawly", Image: "a.example.test/you/pawly-agent:20261027-120000", SavedFrom: "pawly/agent-01",
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := srv.placeAgent(ctx, p, state.ClusterAuto); err != nil || got != "ka" {
		t.Errorf("placeAgent(auto) = %q, %v, want ka, the cluster that serves the project image", got, err)
	}
	if _, err := srv.placeAgent(ctx, p, "kb"); err == nil || !strings.Contains(err.Error(), "a.example.test/you/pawly-agent") {
		t.Errorf("placeAgent(kb) = %v, want it to name the image kb can't pull", err)
	}
}

// TestPlacementRefusesAClusterThatCantStartAPod: the two things the reconciler
// would leave a pod waiting for are refused before the agent is made, and auto
// skips the cluster rather than choosing one whose pod can never start.
func TestPlacementRefusesAClusterThatCantStartAPod(t *testing.T) {
	srv, p := placementSetup(t, "", map[string]api.ClusterCapacity{"k3d": {Agents: 2}})
	ctx := context.Background()
	cluster, err := srv.store.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	cluster.MasterURL = ""
	if err := srv.store.SetClusterConfig(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.placeAgent(ctx, p, "k3d"); err == nil || !strings.Contains(err.Error(), "no master URL") {
		t.Errorf("placeAgent() = %v, want it to say the cluster has no master URL", err)
	}
	if _, err := srv.placeAgent(ctx, p, state.ClusterAuto); err == nil || !strings.Contains(err.Error(), "no master URL") {
		t.Errorf("placeAgent(auto) = %v, want it to name what k3d is missing", err)
	}
}

// TestPlacementLeavesAnUnmeasuredClusterToTheCluster: nothing has looked at it
// yet, so a named one is tried rather than refused on a number nobody has, and
// auto doesn't pick it because it can't compare it.
func TestPlacementLeavesAnUnmeasuredClusterToTheCluster(t *testing.T) {
	srv, p := placementSetup(t, "k3d", map[string]api.ClusterCapacity{"k3d": {Agents: 1}})
	// What a cluster added a moment ago looks like: it exists, and no look has
	// written a number for it.
	srv.forgetClusterCapacity("k3d")
	if got, err := srv.placeAgent(context.Background(), p, "k3d"); err != nil || got != "k3d" {
		t.Errorf("placeAgent(k3d) = %q, %v, want it tried", got, err)
	}
	_, err := srv.placeAgent(context.Background(), p, state.ClusterAuto)
	var noRoom *NoRoomError
	if !errors.As(err, &noRoom) || !strings.Contains(err.Error(), "room couldn't be read") {
		t.Errorf("placeAgent(auto) = %v, want a NoRoomError saying the room wasn't read", err)
	}
}
