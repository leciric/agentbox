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

// testDigest is a full sha256 digest, and testDigestB another one, so a test
// that moves between them can tell which is which.
func testDigest(hex string) string { return digestPrefix + strings.Repeat(hex, 64) }

// upgradeRequest runs an upgrade through the router and returns the job it
// started.
func upgradeRequest(t *testing.T, srv *Server, name, body string) api.Job {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/clusters/"+name+"/upgrade", strings.NewReader(body))
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upgrade %s = %d: %s", body, rec.Code, rec.Body.String())
	}
	var j api.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	return j
}

// TestUpgradeImages: an upgrade keeps the tag and appends the digest, a rollback
// swaps the two images, and a second rollback goes forward again.
func TestUpgradeImages(t *testing.T) {
	digest := testDigest("a")
	old := "agentbox-agent:dev"

	cluster := state.Cluster{Name: "k3d", Image: old}
	image, previous, err := upgradeImages(cluster, api.UpgradeClusterRequest{Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	if image != old+"@"+digest || previous != old {
		t.Errorf("an upgrade = %q, previous %q, want %q and %q", image, previous, old+"@"+digest, old)
	}

	// An image that already ran at a digest keeps its name, not the old digest.
	cluster.Image = old + "@" + testDigest("b")
	if got, _, err := upgradeImages(cluster, api.UpgradeClusterRequest{Digest: digest}); err != nil || got != old+"@"+digest {
		t.Errorf("an upgrade from a digest = %q, %v, want %q", got, err, old+"@"+digest)
	}

	// Rollback goes to the image before the upgrade, and keeps the one it is
	// leaving as the new previous.
	upgraded := state.Cluster{Name: "k3d", Image: old + "@" + digest, PreviousImage: old}
	image, previous, err = upgradeImages(upgraded, api.UpgradeClusterRequest{Rollback: true})
	if err != nil {
		t.Fatal(err)
	}
	if image != old || previous != old+"@"+digest {
		t.Errorf("a rollback = %q, previous %q, want %q and %q", image, previous, old, old+"@"+digest)
	}
	// A second rollback swaps them back, which is the way forward again.
	back := state.Cluster{Name: "k3d", Image: image, PreviousImage: previous}
	if image, previous, err = upgradeImages(back, api.UpgradeClusterRequest{Rollback: true}); err != nil ||
		image != old+"@"+digest || previous != old {
		t.Errorf("a second rollback = %q, previous %q, %v, want %q and %q", image, previous, err, old+"@"+digest, old)
	}
}

// TestUpgradeImagesRefusesWhatItCantRun: the digest and the rollback are each
// asked for alone, a digest is a full sha256 one, and a cluster with no image or
// nothing to roll back to is a sentence.
func TestUpgradeImagesRefusesWhatItCantRun(t *testing.T) {
	digest := testDigest("a")
	cases := []struct {
		name    string
		cluster state.Cluster
		req     api.UpgradeClusterRequest
		want    string
	}{
		{"both", state.Cluster{Name: "k3d", Image: "agentbox-agent:dev"}, api.UpgradeClusterRequest{Digest: digest, Rollback: true}, "pass a digest or --rollback, not both"},
		{"neither", state.Cluster{Name: "k3d", Image: "agentbox-agent:dev"}, api.UpgradeClusterRequest{}, "pass a digest like sha256:..."},
		{"no image", state.Cluster{Name: "k3d"}, api.UpgradeClusterRequest{Digest: digest}, "cluster k3d has no image to upgrade"},
		{"a tag", state.Cluster{Name: "k3d", Image: "agentbox-agent:dev"}, api.UpgradeClusterRequest{Digest: "agentbox-agent:dev"}, "a digest is sha256: and 64 hex characters"},
		{"short", state.Cluster{Name: "k3d", Image: "agentbox-agent:dev"}, api.UpgradeClusterRequest{Digest: "sha256:abcd"}, "64 hex characters"},
		{"not hex", state.Cluster{Name: "k3d", Image: "agentbox-agent:dev"}, api.UpgradeClusterRequest{Digest: "sha256:" + strings.Repeat("g", 64)}, "not hexadecimal"},
		{"uppercase", state.Cluster{Name: "k3d", Image: "agentbox-agent:dev"}, api.UpgradeClusterRequest{Digest: "sha256:" + strings.Repeat("A", 64)}, "lowercase"},
		{"nothing to roll back to", state.Cluster{Name: "k3d", Image: "agentbox-agent:dev"}, api.UpgradeClusterRequest{Rollback: true}, "cluster k3d has not been upgraded: there is nothing to roll back to"},
	}
	for _, tc := range cases {
		if _, _, err := upgradeImages(tc.cluster, tc.req); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// TestUpgradeRecordsTheImagesAndSkipsAStoppedAgent: the job writes both images
// before it touches a pod, and an agent that is stopped has no pod to replace:
// it runs the new image when it starts. Rollback then swaps the two, at the
// store, which is what the next rollback reads.
func TestUpgradeRecordsTheImagesAndSkipsAStoppedAgent(t *testing.T) {
	srv, _ := queueTestServer(t, "", map[string]api.ClusterCapacity{"k3d": {Agents: 2}})
	ctx := context.Background()
	reconcilerAgent(t, srv.store, "agent-01")
	if err := srv.store.SetAgentDesiredState(ctx, "pawly", "agent-01", state.DesiredStopped); err != nil {
		t.Fatal(err)
	}
	digest := testDigest("a")
	old := "agentbox-agent:dev"

	job := finishedClusterJob(t, srv, upgradeRequest(t, srv, "k3d", `{"digest":"`+digest+`"}`))
	if job.Status != api.JobSucceeded {
		t.Fatalf("the upgrade job = %s (%s), want it to succeed", job.Status, job.Error)
	}
	cluster, err := srv.store.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if cluster.Image != old+"@"+digest || cluster.PreviousImage != old {
		t.Errorf("after the upgrade the cluster runs %q, previous %q, want %q and %q",
			cluster.Image, cluster.PreviousImage, old+"@"+digest, old)
	}
	if !strings.Contains(job.Log, "pawly/agent-01 is stopped: it runs the new image when it starts") {
		t.Errorf("the job log = %q, want it to say the stopped agent was skipped", job.Log)
	}
	a, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if a.DesiredState != state.DesiredStopped || a.Pod != "" || a.Image != "" {
		t.Errorf("the stopped agent = %+v, want it stopped with no pod and no pinned image", a)
	}
	var result api.Cluster
	if err := json.Unmarshal([]byte(job.Result), &result); err != nil {
		t.Fatal(err)
	}
	if result.Image != cluster.Image || result.PreviousImage != cluster.PreviousImage {
		t.Errorf("the job's result = %+v, want the cluster's new and previous image", result)
	}

	job = finishedClusterJob(t, srv, upgradeRequest(t, srv, "k3d", `{"rollback":true}`))
	if job.Status != api.JobSucceeded {
		t.Fatalf("the rollback job = %s (%s), want it to succeed", job.Status, job.Error)
	}
	cluster, err = srv.store.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if cluster.Image != old || cluster.PreviousImage != old+"@"+digest {
		t.Errorf("after the rollback the cluster runs %q, previous %q, want %q and %q",
			cluster.Image, cluster.PreviousImage, old, old+"@"+digest)
	}
}

// TestUpgradeFailureNamesTheAgentAndTheRollback: the image is recorded before
// the pods move, so a pod that can't be recreated leaves the cluster at the new
// image and the job says which agent stopped it and how to go back.
//
// What this does not prove is a pod actually being recreated: the fake cluster
// here has no readable kubeconfig, so the recreate fails at its first call and
// the message is what is under test.
func TestUpgradeFailureNamesTheAgentAndTheRollback(t *testing.T) {
	srv, _ := queueTestServer(t, "", map[string]api.ClusterCapacity{"k3d": {Agents: 2}})
	ctx := context.Background()
	reconcilerAgent(t, srv.store, "agent-01")
	digest := testDigest("a")

	job := finishedClusterJob(t, srv, upgradeRequest(t, srv, "k3d", `{"digest":"`+digest+`"}`))
	if job.Status != api.JobFailed {
		t.Fatalf("the upgrade job = %s (%s), want it to fail without a kubeconfig to recreate the pod with", job.Status, job.Error)
	}
	if !strings.Contains(job.Error, "upgrading pawly/agent-01 failed") {
		t.Errorf("the job error = %q, want it to name the agent", job.Error)
	}
	if !strings.Contains(job.Error, "agentbox cluster upgrade k3d --rollback") {
		t.Errorf("the job error = %q, want it to say how to roll back", job.Error)
	}
	cluster, err := srv.store.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if cluster.Image != "agentbox-agent:dev@"+digest {
		t.Errorf("the cluster runs %q after a failed upgrade, want the new image recorded first", cluster.Image)
	}
}
