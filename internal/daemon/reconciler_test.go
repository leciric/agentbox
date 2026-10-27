package daemon

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"agentbox/internal/incus"
	"agentbox/internal/k8s"
	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// reconcilerServer is a daemon with a store and nothing else running: the
// reconciler's round is called by hand, so no ticker decides when it happens.
func reconcilerServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	p := paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}
	srv, err := New(Config{Paths: p, Incus: incus.Client{Bin: "false"}, Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.store.Close() })
	return srv
}

// reconcilerProject is the project the agents below belong to.
func reconcilerProject(t *testing.T, st *state.Store) {
	t.Helper()
	err := st.AddProject(context.Background(), state.Project{Name: "pawly", Root: t.TempDir(), CreatedAt: time.Now().Truncate(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
}

// reconcilerAgent is one agent of that project, running in k3d.
func reconcilerAgent(t *testing.T, st *state.Store, name string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	err := st.AddAgent(ctx, state.Agent{
		Project: "pawly", Name: name, Instance: "ab-pawly-" + name, AI: "claude",
		Branch: "agentbox/" + name, BaseRef: "main", BaseCommit: "abc", Status: "running", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PlaceAgent(ctx, "pawly", name, "k3d", "/workspace", state.DesiredRunning); err != nil {
		t.Fatal(err)
	}
}

// podClient is a fake cluster that gives a pod a UID when it is made, the way a
// real API server does: the fake clientset stores what it is handed.
func podClient() *fake.Clientset {
	client := fake.NewClientset()
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pod := action.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		if pod.UID == "" {
			pod.UID = types.UID("uid-" + pod.Name)
		}
		return false, pod, nil
	})
	return client
}

func reconcilerCluster() state.Cluster {
	return state.Cluster{Name: "k3d", Namespace: "agentbox", Image: "agentbox-agent:dev", CreatedAt: time.Now()}
}

// reconcileRound is one cluster's round against a fake cluster, the way
// reconcileCluster runs it after listing.
func reconcileRound(t *testing.T, srv *Server, cluster state.Cluster, kc *k8s.Cluster) error {
	t.Helper()
	ctx := context.Background()
	pods, err := kc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	agents, err := srv.store.AgentsOnCluster(ctx, cluster.Name)
	if err != nil {
		t.Fatal(err)
	}
	return srv.reconcileAgents(ctx, cluster, kc, pods, agents)
}

func TestReconcilerStartsThePodARunningAgentAsksFor(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	cluster := reconcilerCluster()
	client := podClient()
	kc := k8s.FromClient("k3d", "agentbox", client)

	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatalf("the round = %v", err)
	}
	pod, err := kc.Get(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatalf("no pod was made: %v", err)
	}
	got, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Pod != pod.Name || got.PodUID != string(pod.UID) {
		t.Errorf("the row says pod %q (%s), the cluster has %q (%s)", got.Pod, got.PodUID, pod.Name, pod.UID)
	}

	// A second round leaves it alone: the pod it made is the one it finds.
	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatalf("the second round = %v", err)
	}
	pods, err := kc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 {
		t.Errorf("%d pods, want the one the agent asked for", len(pods))
	}
}

func TestReconcilerAdoptsAPodTheRowDoesNotName(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	cluster := reconcilerCluster()
	client := podClient()
	kc := k8s.FromClient("k3d", "agentbox", client)
	// The pod was made by an earlier daemon, which never got to write the row.
	spec := srv.podSpec(ctx, cluster, nil, state.Agent{Project: "pawly", Name: "agent-01", Branch: "agentbox/agent-01", BaseCommit: "abc"})
	if _, err := kc.Create(ctx, spec, "0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatal(err)
	}
	got, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Pod == "" || got.PodUID == "" {
		t.Errorf("the row wasn't adopted: %+v", got)
	}
}

func TestReconcilerStopsAndDestroysWhatItIsAskedTo(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	cluster := reconcilerCluster()
	client := podClient()
	kc := k8s.FromClient("k3d", "agentbox", client)
	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatal(err)
	}

	// Stopped takes the pod and keeps the work.
	if err := srv.store.SetAgentDesiredState(ctx, "pawly", "agent-01", state.DesiredStopped); err != nil {
		t.Fatal(err)
	}
	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Get(ctx, "pawly", "agent-01"); err == nil {
		t.Error("a stopped agent still has its pod")
	}
	if got, err := srv.store.Agent(ctx, "pawly", "agent-01"); err != nil {
		t.Fatal(err)
	} else if got.Pod != "" || got.PodUID != "" {
		t.Errorf("the row still names the pod it stopped: %q", got.Pod)
	}

	// Destroyed takes the claim too.
	if err := srv.store.SetAgentDesiredState(ctx, "pawly", "agent-01", state.DesiredRunning); err != nil {
		t.Fatal(err)
	}
	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.MarkAgentDestroyed(ctx, "pawly", "agent-01", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("agentbox").Get(ctx, "ab-pawly-agent-01-workspace", metav1.GetOptions{}); err == nil {
		t.Error("a destroyed agent's claim is still there")
	}
}

func TestReconcilerSaysWhyItCannotMakeAPod(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	cluster := reconcilerCluster()
	cluster.Image = ""
	kc := k8s.FromClient("k3d", "agentbox", podClient())

	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatalf("the round = %v", err)
	}
	got, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.StatusDetail, "has no image") {
		t.Errorf("detail = %q, want the cluster's missing image", got.StatusDetail)
	}
}
