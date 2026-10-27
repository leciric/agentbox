package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentbox/internal/state"
)

// TestProjectClusterIsWhereItsAgentsRun: a project remembers where its new
// agents run, refuses one that isn't there, and goes back to this host when the
// cluster it named is removed.
func TestProjectClusterIsWhereItsAgentsRun(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if err := s.AddProject(ctx, state.Project{Name: "pawly", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddCluster(ctx, state.Cluster{
		Name: "k3d", Namespace: "agentbox", Image: "agentbox-agent:dev",
		MasterURL: "https://master.example.test", Tailnet: state.TailnetNone,
		Kubeconfig: []byte("sealed-for-a-test"), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProjectCluster(ctx, "pawly", "k3d"); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Project(ctx, "pawly"); err != nil || p.Cluster != "k3d" {
		t.Fatalf("Project() = %+v, %v, want it on k3d", p, err)
	}
	// And the list the app reads says the same.
	if ps, err := s.Projects(ctx); err != nil || len(ps) != 1 || ps[0].Cluster != "k3d" {
		t.Fatalf("Projects() = %+v, %v", ps, err)
	}

	if err := s.SetProjectCluster(ctx, "pawly", state.ClusterAuto); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Project(ctx, "pawly"); p.Cluster != state.ClusterAuto {
		t.Errorf("after auto: cluster = %q", p.Cluster)
	}
	// "local" is how a request says this host, and is stored as "".
	if err := s.SetProjectCluster(ctx, "pawly", state.ClusterLocal); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Project(ctx, "pawly"); p.Cluster != "" {
		t.Errorf("after local: cluster = %q, want this host", p.Cluster)
	}

	if err := s.SetProjectCluster(ctx, "pawly", "nope"); err == nil || !strings.Contains(err.Error(), "no cluster named") {
		t.Errorf("setting a cluster that isn't there: %v", err)
	}
	if err := s.SetProjectCluster(ctx, "nobody", "k3d"); err == nil {
		t.Error("setting the cluster of a project that isn't there was accepted")
	}

	// Removing the cluster a project named puts it back on this host, rather
	// than leaving a default nothing answers to.
	if err := s.SetProjectCluster(ctx, "pawly", "k3d"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveCluster(ctx, "k3d"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Project(ctx, "pawly"); p.Cluster != "" {
		t.Errorf("after removing its cluster: cluster = %q, want this host", p.Cluster)
	}
}
