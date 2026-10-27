package cli

import (
	"testing"
	"time"

	"agentbox/internal/api"
)

func TestClusterListWords(t *testing.T) {
	full := api.Cluster{Name: "k3d", MaxAgents: 4, Capacity: &api.ClusterCapacity{Agents: 0, Running: 4, At: time.Now()}}
	if got, want := clusterAgentsWord(full), "4 of 4"; got != want {
		t.Errorf("agents = %q, want %q", got, want)
	}
	if got, want := freeWord(full), "0"; got != want {
		t.Errorf("free = %q, want %q", got, want)
	}
	open := api.Cluster{Name: "k3d", Capacity: &api.ClusterCapacity{Agents: 2, Running: 1, At: time.Now()}}
	if got, want := clusterAgentsWord(open), "1"; got != want {
		t.Errorf("agents without a ceiling = %q, want %q", got, want)
	}
	if got, want := freeWord(open), "2"; got != want {
		t.Errorf("free = %q, want %q", got, want)
	}
	// Nothing counted yet, or a look that failed: no number is better than a
	// wrong one.
	if got, want := freeWord(api.Cluster{Name: "k3d"}), "-"; got != want {
		t.Errorf("free before a look = %q, want %q", got, want)
	}
	if got, want := clusterAgentsWord(api.Cluster{Name: "k3d", MaxAgents: 4}), "? of 4"; got != want {
		t.Errorf("agents before a look = %q, want %q", got, want)
	}
	failed := api.Cluster{Name: "k3d", Capacity: &api.ClusterCapacity{Agents: 2, Running: 1, Error: "no route"}}
	if got, want := freeWord(failed), "-"; got != want {
		t.Errorf("free after a failed look = %q, want %q", got, want)
	}
	// Looked, and there is no ceiling to count.
	unbounded := api.Cluster{Name: "k3d", Capacity: &api.ClusterCapacity{Agents: -1, Running: 1, At: time.Now()}}
	if got, want := freeWord(unbounded), "no limit"; got != want {
		t.Errorf("free with no ceiling = %q, want %q", got, want)
	}
}

func TestPodSizeWords(t *testing.T) {
	cluster := api.Cluster{AgentCPU: "2", AgentMemory: "8Gi", WorkspaceSize: "40Gi", EphemeralStorage: "10Gi"}
	if got, want := podSizeWords(cluster), "2 CPU, 8Gi memory, 40Gi workspace"; got != want {
		t.Errorf("pod size = %q, want %q", got, want)
	}
	if got := podSizeWords(api.Cluster{}); got != "" {
		t.Errorf("an unsized cluster = %q, want nothing to print", got)
	}
}
