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
