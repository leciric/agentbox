package cli

import (
	"testing"
	"time"

	"agentbox/internal/api"
)

// TestClusterListMarksADrainingCluster: a draining cluster is obvious in the
// table, and its FREE is 0 because admission refuses it, whatever room it still
// has.
func TestClusterListMarksADrainingCluster(t *testing.T) {
	draining := api.Cluster{
		Name: "k3d", Draining: true,
		Capacity: &api.ClusterCapacity{Agents: 3, Running: 1, At: time.Now()},
	}
	if got, want := clusterStateWord(draining), "DRAINING"; got != want {
		t.Errorf("state = %q, want %q", got, want)
	}
	if got, want := freeWord(draining), "0"; got != want {
		t.Errorf("free = %q, want %q", got, want)
	}
	// Even with nothing measured, a draining cluster takes no agent.
	if got, want := freeWord(api.Cluster{Name: "k3d", Draining: true}), "0"; got != want {
		t.Errorf("free before a look = %q, want %q", got, want)
	}
	if got, want := clusterStateWord(api.Cluster{Name: "k3d"}), "-"; got != want {
		t.Errorf("state of a cluster taking agents = %q, want %q", got, want)
	}
}

// TestShortImageKeepsTheDigestReadable: the list shows the image name and the
// first eight characters of its digest, and an image with no digest is left as
// it is.
func TestShortImageKeepsTheDigestReadable(t *testing.T) {
	const digest = "sha256:abcd1234ef567890abcd1234ef567890abcd1234ef567890abcd1234ef567890"
	cases := []struct{ image, want string }{
		{"agentbox-agent:dev", "agentbox-agent:dev"},
		{"", ""},
		{"agentbox-agent:dev@" + digest, "agentbox-agent:dev@sha256:abcd1234"},
		// Already short: nothing to cut.
		{"agentbox-agent:dev@sha256:abcd", "agentbox-agent:dev@sha256:abcd"},
	}
	for _, tc := range cases {
		if got := shortImage(tc.image); got != tc.want {
			t.Errorf("shortImage(%q) = %q, want %q", tc.image, got, tc.want)
		}
	}
}
