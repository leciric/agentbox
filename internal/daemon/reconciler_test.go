package daemon

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"agentbox/internal/api"
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
	return state.Cluster{
		Name:      "k3d",
		Namespace: "agentbox",
		Image:     "agentbox-agent:dev",
		// Where a pod of this cluster dials the daemon. Every test that expects
		// a pod needs one: a cluster without it is a case of its own.
		MasterURL: "https://master.example.test",
		CreatedAt: time.Now(),
	}
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

func TestCapacityCountsWhatFits(t *testing.T) {
	now := time.Now()
	room := k8s.Capacity{
		AllocatableCPU:    resource.MustParse("8"),
		AllocatableMemory: resource.MustParse("32Gi"),
		RequestedCPU:      resource.MustParse("4"),
		RequestedMemory:   resource.MustParse("8Gi"),
	}
	// Three agent slots under the ceiling, two by CPU (4 free / 2 each), three
	// by memory (24Gi / 8Gi each): the smallest of them is what fits.
	cluster := state.Cluster{Name: "k3d", MaxAgents: 4, AgentCPU: "2", AgentMemory: "8Gi"}
	got := capacityOf(cluster, room, 1, now)
	if got.Agents != 2 || got.Running != 1 {
		t.Errorf("capacity = %+v, want 2 more of 4, 1 running", got)
	}
	if got.CPU != "4" || got.Memory != "24Gi" {
		t.Errorf("room = %s CPU, %s memory, want 4 and 24Gi", got.CPU, got.Memory)
	}

	// The cluster's own budget is a ceiling under the nodes'.
	budgeted := state.Cluster{Name: "k3d", MaxAgents: 10, CPU: "6", Memory: "32Gi", AgentCPU: "2", AgentMemory: "8Gi"}
	if got := capacityOf(budgeted, room, 0, now); got.Agents != 1 || got.CPU != "2" || got.Memory != "24Gi" {
		t.Errorf("budgeted capacity = %+v, want 1 more and 2 CPU left", got)
	}

	// With no ceiling and no size, nothing says how many fit.
	if got := capacityOf(state.Cluster{Name: "k3d"}, room, 2, now); got.Agents != -1 {
		t.Errorf("unbounded capacity = %+v, want -1 for no ceiling", got)
	}

	// A cluster whose free CPU cannot hold one agent is full, not negative.
	full := state.Cluster{Name: "k3d", MaxAgents: 4, AgentCPU: "8"}
	if got := capacityOf(full, room, 2, now); got.Agents != 0 {
		t.Errorf("full capacity = %+v, want 0", got)
	}
}

func TestCapacityIsShownBesideTheCluster(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	cluster := reconcilerCluster()
	cluster.MaxAgents = 4
	cluster.AgentCPU = "1"
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-01"},
		Status: corev1.NodeStatus{
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi")},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "ab-pawly-agent-01", Namespace: "agentbox"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "agent", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}},
		}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	kc := k8s.FromClient("k3d", "agentbox", fake.NewClientset(node, pod))
	srv.measureCapacity(ctx, cluster, kc, []k8s.Pod{{Name: pod.Name, Phase: corev1.PodRunning}})

	info := srv.clusterInfo(cluster)
	if info.Capacity == nil {
		t.Fatal("the cluster has no capacity after a look")
	}
	// 4 allocatable less the pod's 1 is 3; the ceiling leaves 3.
	if info.Capacity.Agents != 3 || info.Capacity.Running != 1 || info.Capacity.CPU != "3" {
		t.Errorf("capacity = %+v, want 3 more, 1 running and 3 CPU left", info.Capacity)
	}
	if info.Capacity.At.IsZero() {
		t.Error("the capacity has no time it was taken")
	}
}

func TestCapacityLoopLooksAtClustersWithNoAgents(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	sealed, err := srv.sealValue(`apiVersion: v1
kind: Config
clusters:
- name: dead
  cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
users:
- name: agentbox
  user:
    token: a-token
contexts:
- name: dead
  context:
    cluster: dead
    user: agentbox
current-context: dead
`)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.AddCluster(ctx, state.Cluster{
		Name: "k3d", Namespace: "agentbox", Tailnet: state.TailnetNone,
		Kubeconfig: sealed, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	srv.measureClusterCapacities(ctx)
	capacity, ok := srv.clusterCapacity("k3d")
	if !ok {
		t.Fatal("a cluster with no agents was never measured")
	}
	if capacity.Agents != -1 || capacity.Error == "" {
		t.Errorf("capacity = %+v, want it to say the cluster couldn't be reached", capacity)
	}
}

func TestCapacityKeepsTheLastGoodLook(t *testing.T) {
	srv := reconcilerServer(t)
	cluster := reconcilerCluster()
	srv.setClusterCapacity(cluster.Name, api.ClusterCapacity{Agents: 2, Running: 1, CPU: "2", At: time.Now()})
	srv.recordClusterCapacity(cluster, k8s.Capacity{}, 0, errors.New("no route to the API server"))

	info := srv.clusterInfo(cluster)
	if info.Capacity == nil || info.Capacity.Agents != 2 || info.Capacity.Error == "" {
		t.Errorf("capacity after a failed look = %+v, want the last numbers and the error", info.Capacity)
	}
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
	// The token the pod reads from its Secret is the one this machine kept the
	// hash of, and it is for this agent only.
	token := podSecretToken(t, client, "pawly", "agent-01")
	if project, agent, err := srv.store.AgentForToken(ctx, token); err != nil || project != "pawly" || agent != "agent-01" {
		t.Errorf("the pod's token belongs to %q/%q (%v), want pawly/agent-01", project, agent, err)
	}
	if env := podEnv(t, client, "pawly", "agent-01", k8s.EnvMasterURL); env != cluster.MasterURL {
		t.Errorf("%s = %q, want the cluster's master url %q", k8s.EnvMasterURL, env, cluster.MasterURL)
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
	// The token goes with the pod it authenticated, stopped or destroyed: the
	// pod is gone either way, and a start writes a new one.
	token := podSecretToken(t, client, "pawly", "agent-01")
	if _, _, err := srv.store.AgentForToken(ctx, token); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a stopped agent's token still authenticates: %v", err)
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

func TestReconcilerRefusesAClusterWithNoMasterURL(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	cluster := reconcilerCluster()
	cluster.MasterURL = ""
	client := podClient()
	kc := k8s.FromClient("k3d", "agentbox", client)

	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatalf("the round = %v", err)
	}
	got, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.StatusDetail, "--master-url") {
		t.Errorf("detail = %q, want it to name the command that sets the master url", got.StatusDetail)
	}
	pods, err := kc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 0 {
		t.Errorf("%d pods were made for a cluster whose agentd has nowhere to dial", len(pods))
	}
}

func TestReconcilerAdoptsAPodTheClusterMadeButDidNotAnswerFor(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	cluster := reconcilerCluster()
	client := fake.NewClientset()
	// The API server stored the pod and the answer never came back: the create
	// is an error to the caller, and the pod is there, ready to dial.
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pod := action.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		pod.UID = types.UID("uid-" + pod.Name)
		if err := client.Tracker().Add(pod); err != nil {
			t.Fatalf("storing the pod the API server made: %v", err)
		}
		return true, nil, errors.New("the API server timed out writing the pod")
	})
	kc := k8s.FromClient("k3d", "agentbox", client)

	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatalf("the round = %v, and a pod the cluster made is adopted", err)
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
		t.Errorf("the row says pod %q (%s), and the cluster has %q (%s)", got.Pod, got.PodUID, pod.Name, pod.UID)
	}
	// The token the pod reads is the one its row kept, so it can dial: removing
	// it would leave a pod the cluster has asking with a token nothing knows.
	token := podSecretToken(t, client, "pawly", "agent-01")
	if project, agent, err := srv.store.AgentForToken(ctx, token); err != nil || project != "pawly" || agent != "agent-01" {
		t.Errorf("the adopted pod's token belongs to %q/%q (%v), want pawly/agent-01", project, agent, err)
	}
	if got.StatusDetail != "" {
		t.Errorf("detail = %q, and the pod the cluster made is not a failure", got.StatusDetail)
	}

	// And the next round adopts the same pod rather than making another.
	if err := reconcileRound(t, srv, cluster, kc); err != nil {
		t.Fatalf("the second round = %v", err)
	}
	pods, err := kc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 {
		t.Errorf("%d pods, and the round adopted the one the cluster made", len(pods))
	}
}

func TestReconcilerForgetsTheTokenOfAPodItCouldNotMake(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	cluster := reconcilerCluster()
	client := podClient()
	// The Secret is written before the pod, so a pod the cluster refuses still
	// leaves a token in the cluster: the agent's row must not keep it.
	client.PrependReactor("create", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("no node has room")
	})
	kc := k8s.FromClient("k3d", "agentbox", client)

	if err := reconcileRound(t, srv, cluster, kc); err == nil {
		t.Fatal("the round said it made a pod the cluster refused")
	}
	token := podSecretToken(t, client, "pawly", "agent-01")
	if _, _, err := srv.store.AgentForToken(ctx, token); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the refused pod's token still authenticates: %v", err)
	}
	got, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.StatusDetail, "couldn't be created") {
		t.Errorf("detail = %q, want the reason the pod wasn't made", got.StatusDetail)
	}
}

// podSecretToken is the token a pod was handed: the Secret Create wrote from the
// value it was given, which is what the daemon keeps the hash of.
func podSecretToken(t *testing.T, client *fake.Clientset, project, agent string) string {
	t.Helper()
	secret, err := client.CoreV1().Secrets("agentbox").Get(context.Background(), k8s.PodTokenSecretName(project, agent), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the pod's token secret: %v", err)
	}
	return string(secret.Data["token"])
}

// podEnv is one environment variable the pod's container was given.
func podEnv(t *testing.T, client *fake.Clientset, project, agent, name string) string {
	t.Helper()
	pod, err := client.CoreV1().Pods("agentbox").Get(context.Background(), k8s.PodName(project, agent), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the pod: %v", err)
	}
	for _, container := range pod.Spec.Containers {
		for _, env := range container.Env {
			if env.Name == name {
				return env.Value
			}
		}
	}
	return ""
}

// TestReconcilerSweepsAHelperLeftBehind is the garbage collection a crashed
// daemon leaves to the next one: a helper older than its deadline holds the
// agent's ReadWriteOnce claim until the round deletes it.
func TestReconcilerSweepsAHelperLeftBehind(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	cluster := reconcilerCluster()
	cluster.Kubeconfig = []byte("sealed-for-the-test")
	cluster.Tailnet = state.TailnetNone
	if err := srv.store.AddCluster(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	stale := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ab-pawly-agent-01-archive-old",
			Namespace:         "agentbox",
			UID:               "uid-helper",
			Labels:            map[string]string{k8s.LabelHelper: k8s.HelperArchive, k8s.LabelApp: k8s.AppArchive},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	kc := k8s.FromClient("k3d", "agentbox", fake.NewClientset(stale))
	// The client is the one clusterClient would hand back for that kubeconfig.
	key := cluster.Name + "\x00" + cluster.Namespace + "\x00" + string(cluster.Kubeconfig)
	srv.clusters.byKey = map[string]*k8s.Cluster{key: kc}

	if err := srv.reconcileCluster(ctx, cluster.Name); err != nil {
		t.Fatalf("the round = %v", err)
	}
	if _, err := kc.Client().CoreV1().Pods("agentbox").Get(ctx, stale.Name, metav1.GetOptions{}); err == nil {
		t.Error("the helper left behind survived the reconciler's round")
	}
}
