package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"agentbox/internal/gateway"
	"agentbox/internal/k8s"
	"agentbox/internal/state"
)

// TestPodInfoIsRunningOnlyForAnAdoptedPod checks the one thing the adapter
// decides: a row that asked to run and has a pod the cluster didn't complain
// about is running, and every other row is stopped.
func TestPodInfoIsRunningOnlyForAnAdoptedPod(t *testing.T) {
	running := state.Agent{Project: "proj", Name: "agent-1", Pod: "agentbox-proj-agent-1", DesiredState: state.DesiredRunning}
	if got := podInfo(running); got.State != "Running" || got.Ref != running.Pod {
		t.Fatalf("an adopted pod is %+v, want Running as %s", got, running.Pod)
	}

	cases := []struct {
		name string
		a    state.Agent
	}{
		{"no pod yet", state.Agent{Project: "proj", Name: "agent-1", DesiredState: state.DesiredRunning}},
		{"the cluster complained", state.Agent{Project: "proj", Name: "agent-1", Pod: "p", DesiredState: state.DesiredRunning, StatusDetail: "ImagePullBackOff"}},
		{"the user stopped it", state.Agent{Project: "proj", Name: "agent-1", Pod: "p", DesiredState: state.DesiredStopped}},
		{"the user destroyed it", state.Agent{Project: "proj", Name: "agent-1", Pod: "p", DesiredState: state.DesiredDestroyed}},
	}
	for _, tc := range cases {
		if got := podInfo(tc.a); got.State != "Stopped" {
			t.Errorf("%s: %+v, want Stopped", tc.name, got)
		}
	}

	// An agent with no pod at all still names itself, so an error can say which
	// machine it was about.
	if got := podInfo(state.Agent{Project: "proj", Name: "agent-1"}); got.Ref != "proj/agent-1" {
		t.Errorf("a row with no pod says %q, want proj/agent-1", got.Ref)
	}
}

// clusterWithFakeClient stores cluster and hands the daemon the fake client the
// reconciler tests use, without opening its sealed kubeconfig: the client cache
// is keyed by the cluster's name, namespace and sealed bytes, so this is the
// same seam the reconciler uses.
func clusterWithFakeClient(t *testing.T, srv *Server, cluster state.Cluster, client *fake.Clientset) *k8s.Cluster {
	t.Helper()
	cluster.Kubeconfig = []byte("sealed-for-a-test")
	if err := srv.store.AddCluster(context.Background(), cluster); err != nil {
		t.Fatal(err)
	}
	kc := k8s.FromClient(cluster.Name, cluster.Namespace, client)
	srv.clusters.Lock()
	if srv.clusters.byKey == nil {
		srv.clusters.byKey = map[string]*k8s.Cluster{}
	}
	srv.clusters.byKey[cluster.Name+"\x00"+cluster.Namespace+"\x00"+string(cluster.Kubeconfig)] = kc
	srv.clusters.Unlock()
	return kc
}

// TestDestroyMachineDeletesThePodItsTokenAndItsClaim is the synchronous destroy
// path: one call takes everything the cluster holds for the agent, and a second
// call over an agent that is already gone is done rather than an error.
func TestDestroyMachineDeletesThePodItsTokenAndItsClaim(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	cluster := reconcilerCluster()
	client := podClient()
	kc := clusterWithFakeClient(t, srv, cluster, client)
	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatalf("the round = %v", err)
	}
	token := podSecretToken(t, client, "pawly", "agent-01")
	if _, _, err := srv.store.AgentForToken(ctx, token); err != nil {
		t.Fatalf("the reconciler's token doesn't authenticate: %v", err)
	}
	a, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}

	if err := srv.destroyMachine(ctx, a); err != nil {
		t.Fatalf("destroyMachine() = %v", err)
	}
	if _, err := kc.Get(ctx, "pawly", "agent-01"); !errors.Is(err, k8s.ErrNotFound) {
		t.Errorf("the pod is still there: %v", err)
	}
	secrets := client.CoreV1().Secrets(cluster.Namespace)
	if _, err := secrets.Get(ctx, k8s.PodTokenSecretName("pawly", "agent-01"), metav1.GetOptions{}); err == nil {
		t.Error("the pod's token secret is still there")
	}
	claims := client.CoreV1().PersistentVolumeClaims(cluster.Namespace)
	if _, err := claims.Get(ctx, k8s.ClaimName("pawly", "agent-01"), metav1.GetOptions{}); err == nil {
		t.Error("the pod's claim is still there")
	}
	if _, _, err := srv.store.AgentForToken(ctx, token); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the token still authenticates: %v", err)
	}

	if err := srv.destroyMachine(ctx, a); err != nil {
		t.Errorf("a second destroyMachine() = %v, want the work already done", err)
	}
}

// TestRecreateOfAPodKeepsItsClaimAndDropsItsPin is the cluster half of
// `agentbox recreate`: the pod goes, the claim the row names stays, the image a
// restore pinned is dropped, and the next round makes a new pod at the
// cluster's current image with a token of its own.
func TestRecreateOfAPodKeepsItsClaimAndDropsItsPin(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	cluster := reconcilerCluster()
	client := podClient()
	kc := clusterWithFakeClient(t, srv, cluster, client)
	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatalf("the round = %v", err)
	}
	// What a restore leaves: a claim of its own and the image its snapshot was
	// taken with.
	const claim = "ab-pawly-agent-01-workspace-r1793128698"
	if err := srv.store.SetAgentClaim(ctx, "pawly", "agent-01", claim, "agentbox-agent:snapshot"); err != nil {
		t.Fatal(err)
	}
	oldToken := podSecretToken(t, client, "pawly", "agent-01")

	p := podLifecycle{s: srv}
	if err := p.Recreate(ctx, gateway.Ref{Project: "pawly", Agent: "agent-01"}); err != nil {
		t.Fatalf("Recreate() = %v", err)
	}
	if _, err := kc.Get(ctx, "pawly", "agent-01"); !errors.Is(err, k8s.ErrNotFound) {
		t.Errorf("the pod is still there after Recreate: %v", err)
	}
	got, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.DesiredState != state.DesiredRunning || got.Pod != "" || got.Image != "" || got.Claim != claim {
		t.Errorf("the row after Recreate = %+v, want running, no pod, no pinned image, claim %s", got, claim)
	}
	if _, _, err := srv.store.AgentForToken(ctx, oldToken); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the old pod's token still authenticates: %v", err)
	}

	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatalf("the round after Recreate = %v", err)
	}
	pod, err := kc.Get(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatalf("no pod was made again: %v", err)
	}
	if pod.Image != cluster.Image {
		t.Errorf("the new pod runs %q, want the cluster's %q", pod.Image, cluster.Image)
	}
	raw, err := client.CoreV1().Pods("agentbox").Get(ctx, k8s.PodName("pawly", "agent-01"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	onClaim := false
	for _, volume := range raw.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == claim {
			onClaim = true
		}
	}
	if !onClaim {
		t.Errorf("the new pod is not on %s: %+v", claim, raw.Spec.Volumes)
	}
	if newToken := podSecretToken(t, client, "pawly", "agent-01"); newToken == oldToken {
		t.Error("the new pod kept the old pod's token")
	}
}

// TestPodSpecRunsTheProjectsImage: a saved project image is what its agents run,
// by digest when the save recorded one, and a restored agent's pinned image
// still wins over it.
func TestPodSpecRunsTheProjectsImage(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	a, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	cluster := reconcilerCluster()

	if got := srv.podSpec(ctx, cluster, nil, a).Image; got != cluster.Image {
		t.Errorf("a project with no image runs %q, want the cluster's %q", got, cluster.Image)
	}
	if err := srv.store.SetProjectImage(ctx, state.ProjectImage{
		Project: "pawly", Image: "hub.example/pawly-agent:1", Digest: "sha256:abc",
		SavedFrom: a.Ref(), SavedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := srv.podSpec(ctx, cluster, nil, a).Image, "hub.example/pawly-agent:1@sha256:abc"; got != want {
		t.Errorf("the project image = %q, want %q", got, want)
	}
	a.Image = "agentbox-agent:snapshot"
	if got, want := srv.podSpec(ctx, cluster, nil, a).Image, "agentbox-agent:snapshot"; got != want {
		t.Errorf("a restored agent's pin = %q, want %q", got, want)
	}
}

// TestPodSpecSizesThePodFromTheCluster: one agent's requests, its claim and its
// own disk come from the cluster, so every agent of a cluster is the same size
// and its capacity is a count of them.
func TestPodSpecSizesThePodFromTheCluster(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	a, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	cluster := reconcilerCluster()
	cluster.AgentCPU = "2"
	cluster.AgentMemory = "8Gi"
	cluster.WorkspaceSize = "40Gi"
	cluster.EphemeralStorage = "20Gi"

	spec := srv.podSpec(ctx, cluster, nil, a)
	if spec.CPU != "2" || spec.Memory != "8Gi" || spec.WorkspaceSize != "40Gi" || spec.EphemeralStorage != "20Gi" {
		t.Errorf("spec sizes = %q, %q, %q, %q, want the cluster's 2, 8Gi, 40Gi, 20Gi",
			spec.CPU, spec.Memory, spec.WorkspaceSize, spec.EphemeralStorage)
	}
	// A cluster that says nothing asks for nothing, except the disk guard the
	// pod always has.
	cluster.AgentCPU, cluster.AgentMemory, cluster.WorkspaceSize, cluster.EphemeralStorage = "", "", "", ""
	spec = srv.podSpec(ctx, cluster, nil, a)
	if spec.CPU != "" || spec.Memory != "" || spec.WorkspaceSize != "" || spec.EphemeralStorage != "" {
		t.Errorf("an unsized cluster gave %q, %q, %q, %q, want its defaults", spec.CPU, spec.Memory, spec.WorkspaceSize, spec.EphemeralStorage)
	}
}

// TestRemoveProjectRefusesBeforeItDeletesAnyMachine checks the order the handler
// keeps: the store's refusal comes first, so a project that still has agents
// keeps their pods, tokens and claims. Deleting the machines and refusing after
// would throw away work for a removal that did not happen.
func TestRemoveProjectRefusesBeforeItDeletesAnyMachine(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	// A host agent has no pod to delete, and the removal must not trip over it.
	if err := srv.store.AddAgent(ctx, state.Agent{
		Project: "pawly", Name: "agent-02", Instance: "ab-pawly-agent-02", AI: "claude",
		Branch: "agentbox/agent-02", BaseRef: "main", BaseCommit: "abc", Status: "running", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	cluster := reconcilerCluster()
	client := podClient()
	kc := clusterWithFakeClient(t, srv, cluster, client)
	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatalf("the round = %v", err)
	}
	token := podSecretToken(t, client, "pawly", "agent-01")

	req := httptest.NewRequest(http.MethodDelete, "/v1/projects/pawly", nil)
	req.SetPathValue("project", "pawly")
	if err := srv.removeProject(httptest.NewRecorder(), req); err == nil {
		t.Fatal("removeProject() = nil, want the store to refuse a project that still has agents")
	} else if !strings.Contains(err.Error(), "still has") {
		t.Fatalf("removeProject() = %v, want the store's refusal about its agents", err)
	}
	if _, err := kc.Get(ctx, "pawly", "agent-01"); err != nil {
		t.Errorf("the project's pod went before the removal was refused: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(cluster.Namespace).Get(ctx, k8s.ClaimName("pawly", "agent-01"), metav1.GetOptions{}); err != nil {
		t.Errorf("the project's claim went before the removal was refused: %v", err)
	}
	if _, _, err := srv.store.AgentForToken(ctx, token); err != nil {
		t.Errorf("the project's token stopped authenticating before the removal was refused: %v", err)
	}
}
