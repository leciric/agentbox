package k8s_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"agentbox/internal/k8s"
)

const testToken = "0123456789abcdef0123456789abcdef"

// createAgent makes the three objects an agent's pod needs and returns the pod.
func createAgent(t *testing.T, cluster *k8s.Cluster) *k8s.Pod {
	t.Helper()
	pod, err := cluster.Create(context.Background(), agentSpec(), testToken)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	return pod
}

func TestCreateMakesTheClaimSecretAndPod(t *testing.T) {
	client := fake.NewClientset()
	cluster := k8s.FromClient("k3d", "", client)
	pod := createAgent(t, cluster)
	if pod.Name != "ab-pawly-agent-01" || pod.Ref() != "pawly/agent-01" {
		t.Errorf("the pod = %+v, want ab-pawly-agent-01 for pawly/agent-01", pod)
	}
	// A pod the cluster just accepted is Pending, and says so.
	if pod.State() != "pending" {
		t.Errorf("state = %q, want pending before a node runs it", pod.State())
	}
	ctx := context.Background()
	claim, err := client.CoreV1().PersistentVolumeClaims("agentbox").Get(ctx, "ab-pawly-agent-01-workspace", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the claim wasn't made: %v", err)
	}
	if got := claim.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != k8s.DefaultWorkspaceSize {
		t.Errorf("the claim holds %s, want %s", got.String(), k8s.DefaultWorkspaceSize)
	}
	secret, err := client.CoreV1().Secrets("agentbox").Get(ctx, "ab-pawly-agent-01-token", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the token wasn't made: %v", err)
	}
	if got := string(secret.Data["token"]); got != testToken {
		t.Errorf("the token in the cluster = %q, want the one it was given", got)
	}
}

func TestCreateKeepsThePodThatIsAlreadyThere(t *testing.T) {
	client := fake.NewClientset()
	cluster := k8s.FromClient("k3d", "", client)
	first := createAgent(t, cluster)
	// A second start replaces the token but not the pod: the agent may be
	// working in it.
	second := createAgent(t, cluster)
	if first.UID != second.UID || first.Name != second.Name {
		t.Errorf("the second Create made another pod: %s then %s", first.UID, second.UID)
	}
	secrets, err := client.CoreV1().Secrets("agentbox").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets.Items) != 1 {
		t.Errorf("%d token secrets, want the one", len(secrets.Items))
	}
}

func TestCreateRefusesAClaimSmallerThanTheAgentAsks(t *testing.T) {
	client := fake.NewClientset()
	cluster := k8s.FromClient("k3d", "", client)
	small, err := k8s.BuildPVC(agentSpec())
	if err != nil {
		t.Fatal(err)
	}
	small.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("1Gi")
	small.Namespace = "agentbox"
	if _, err := client.CoreV1().PersistentVolumeClaims("agentbox").Create(context.Background(), small, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.Create(context.Background(), agentSpec(), testToken); err == nil || !strings.Contains(err.Error(), "holds 1Gi") {
		t.Errorf("Create() over a full claim = %v, want it to say the claim is smaller", err)
	}
}

func TestGetAndListReadTheAgentsLabels(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	cluster := k8s.FromClient("k3d", "", client)
	createAgent(t, cluster)

	// A pod of something else in the same namespace is not AgentBox's, and
	// adoption must not touch it.
	other := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "agentbox"}}
	if _, err := client.CoreV1().Pods("agentbox").Create(ctx, other, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	got, err := cluster.Get(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "ab-pawly-agent-01" {
		t.Errorf("Get() = %+v", got)
	}
	if _, err := cluster.Get(ctx, "pawly", "agent-02"); err == nil || !strings.Contains(err.Error(), "has no pod") {
		t.Errorf("Get() of an agent with no pod = %v, want it to say so", err)
	}
	pods, err := cluster.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0].Ref() != "pawly/agent-01" {
		t.Errorf("List() = %+v, want only AgentBox's pod", pods)
	}
}

func TestDeleteKeepsTheWorkAndForgetsEverything(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	cluster := k8s.FromClient("k3d", "", client)
	createAgent(t, cluster)

	if err := cluster.Delete(ctx, "pawly", "agent-01"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if _, err := client.CoreV1().Pods("agentbox").Get(ctx, "ab-pawly-agent-01", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("the pod is still there: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("agentbox").Get(ctx, "ab-pawly-agent-01-workspace", metav1.GetOptions{}); err != nil {
		t.Errorf("Delete() took the agent's work with it: %v", err)
	}
	// Asking for a state it is already in is not an error.
	if err := cluster.Delete(ctx, "pawly", "agent-01"); err != nil {
		t.Errorf("Delete() of a pod that is gone = %v", err)
	}

	if err := cluster.DeleteAll(ctx, "pawly", "agent-01", ""); err != nil {
		t.Fatalf("DeleteAll() = %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("agentbox").Get(ctx, "ab-pawly-agent-01-workspace", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("the claim is still there: %v", err)
	}
	if _, err := client.CoreV1().Secrets("agentbox").Get(ctx, "ab-pawly-agent-01-token", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("the token is still there: %v", err)
	}
}

func TestCapacityIsWhatTheNodesOfferLessWhatIsAsked(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("32Gi")},
		},
	}
	// A node that isn't ready is not room an agent can be placed in.
	down := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
		Status: corev1.NodeStatus{
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}},
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("32Gi")},
		},
	}
	pod, err := k8s.BuildPod(agentSpec())
	if err != nil {
		t.Fatal(err)
	}
	pod.Namespace = "agentbox"
	client := fake.NewClientset(node, down, pod)
	cluster := k8s.FromClient("k3d", "", client)
	capacity, err := cluster.Capacity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capacity.Nodes != 1 {
		t.Errorf("nodes = %d, want the one that is ready", capacity.Nodes)
	}
	if got := capacity.CPU(); got.String() != "6" {
		t.Errorf("cpu left = %s, want 6 (8 allocatable, 2 asked for)", got.String())
	}
	if got := capacity.Memory(); got.String() != "24Gi" {
		t.Errorf("memory left = %s, want 24Gi (32 allocated, 8 asked for)", got.String())
	}
	// Nothing left is zero, never negative.
	over := k8s.Capacity{AllocatableCPU: resource.MustParse("2"), RequestedCPU: resource.MustParse("4")}
	if got := over.CPU(); !got.IsZero() {
		t.Errorf("cpu left on an over-committed node = %s, want zero", got.String())
	}
}

func TestLogsStreamsTheAgentsContainer(t *testing.T) {
	var gotPath, gotContainer, gotTail string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContainer = r.URL.Query().Get("container")
		gotTail = r.URL.Query().Get("tailLines")
		_, _ = fmt.Fprint(w, "agentd: connected\n")
	}))
	defer server.Close()
	client, err := realClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cluster := k8s.FromClient("k3d", "", client)
	stream, err := cluster.Logs(context.Background(), "pawly", "agent-01", 20)
	if err != nil {
		t.Fatalf("Logs() = %v", err)
	}
	defer func() { _ = stream.Close() }()
	body, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "agentd: connected\n"; got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
	if gotPath != "/api/v1/namespaces/agentbox/pods/ab-pawly-agent-01/log" {
		t.Errorf("the request went to %s", gotPath)
	}
	if gotContainer != "agent" || gotTail != "20" {
		t.Errorf("container = %q, tail = %q, want the agent's and 20 lines", gotContainer, gotTail)
	}
}

func TestExecNeedsTheAPIServersOwnConfiguration(t *testing.T) {
	// A pod's exec goes through the API server, not through the clientset, so
	// a cluster built from a clientset alone can't run one: it says so instead
	// of panicking.
	cluster := k8s.FromClient("k3d", "", fake.NewClientset())
	err := cluster.Exec(context.Background(), "pawly", "agent-01", k8s.ExecOptions{Command: []string{"true"}})
	if err == nil || !strings.Contains(err.Error(), "REST config") {
		t.Errorf("Exec() without a REST config = %v, want it to say there is none", err)
	}
	err = cluster.Exec(context.Background(), "pawly", "agent-01", k8s.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "no command") {
		t.Errorf("Exec() with no command = %v", err)
	}
}

// realClient is a clientset that talks over HTTP, for the calls the fake
// clientset can't answer: a log stream, which is a plain GET.
func realClient(server string) (k8sclient.Interface, error) {
	return k8sclient.NewForConfig(&rest.Config{Host: server})
}

// collidingRefs are two refs whose pod, claim and token Secret names are the
// same, because both parts of a ref may contain hyphens.
var collidingRefs = []struct{ name, project, agent, otherProject, otherAgent string }{
	{"a-b/c", "a-b", "c", "a", "b-c"},
	{"a/b-c", "a", "b-c", "a-b", "c"},
}

func refSpec(project, agent string) k8s.Spec {
	spec := agentSpec()
	spec.Project, spec.Agent = project, agent
	return spec
}

func TestTwoRefsThatShareANameDoNotShareObjects(t *testing.T) {
	for _, tc := range collidingRefs {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := fake.NewClientset()
			cluster := k8s.FromClient("k3d", "", client)
			if got, want := k8s.PodName(tc.project, tc.agent), k8s.PodName(tc.otherProject, tc.otherAgent); got != want {
				t.Fatalf("PodName(%s, %s) = %q and PodName(%s, %s) = %q: the refs do not share a name", tc.project, tc.agent, got, tc.otherProject, tc.otherAgent, want)
			}
			first := refSpec(tc.project, tc.agent)
			second := refSpec(tc.otherProject, tc.otherAgent)
			made, err := cluster.Create(ctx, first, testToken)
			if err != nil {
				t.Fatalf("Create(%s) = %v", tc.name, err)
			}

			otherToken := strings.Repeat("b", 32)
			if _, err := cluster.Create(ctx, second, otherToken); err == nil || !strings.Contains(err.Error(), "already belongs to "+tc.project+"/"+tc.agent) {
				t.Fatalf("Create(%s/%s) = %v, want it to refuse the name %s already holds", tc.otherProject, tc.otherAgent, err, tc.name)
			}

			// The first ref's pod, claim and token are untouched.
			pod, err := client.CoreV1().Pods("agentbox").Get(ctx, k8s.PodName(tc.project, tc.agent), metav1.GetOptions{})
			if err != nil {
				t.Fatalf("the first pod is gone: %v", err)
			}
			if pod.UID != made.UID {
				t.Errorf("the pod is %s, want the one %s made", pod.UID, tc.name)
			}
			secret, err := client.CoreV1().Secrets("agentbox").Get(ctx, k8s.PodTokenSecretName(tc.project, tc.agent), metav1.GetOptions{})
			if err != nil {
				t.Fatalf("the first token is gone: %v", err)
			}
			if got := string(secret.Data["token"]); got != testToken {
				t.Errorf("the token = %q, want the first ref's, not the second's", got)
			}
			claim, err := client.CoreV1().PersistentVolumeClaims("agentbox").Get(ctx, k8s.ClaimName(tc.project, tc.agent), metav1.GetOptions{})
			if err != nil {
				t.Fatalf("the first claim is gone: %v", err)
			}
			if got := claim.Labels[k8s.LabelProject] + "/" + claim.Labels[k8s.LabelAgent]; got != tc.project+"/"+tc.agent {
				t.Errorf("the claim belongs to %s, want %s", got, tc.project+"/"+tc.agent)
			}

			// Nothing was made for the second ref, and it has no pod to read.
			pods, err := client.CoreV1().Pods("agentbox").List(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(pods.Items) != 1 {
				t.Errorf("%d pods, want the first ref's one", len(pods.Items))
			}
			if _, err := cluster.Get(ctx, tc.otherProject, tc.otherAgent); err == nil || !strings.Contains(err.Error(), "has no pod") {
				t.Errorf("Get(%s/%s) = %v, want no pod because the name belongs to another ref", tc.otherProject, tc.otherAgent, err)
			}
		})
	}
}

func TestDestroyingOneOfTwoRefsThatShareANameLeavesTheOtherAlone(t *testing.T) {
	for _, tc := range collidingRefs {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := fake.NewClientset()
			cluster := k8s.FromClient("k3d", "", client)
			made, err := cluster.Create(ctx, refSpec(tc.project, tc.agent), testToken)
			if err != nil {
				t.Fatalf("Create(%s) = %v", tc.name, err)
			}

			// The other ref never made these objects, so stopping and
			// forgetting it must not take them.
			if err := cluster.Delete(ctx, tc.otherProject, tc.otherAgent); err != nil {
				t.Fatalf("Delete(%s/%s) = %v", tc.otherProject, tc.otherAgent, err)
			}
			if err := cluster.DeleteAll(ctx, tc.otherProject, tc.otherAgent, ""); err != nil {
				t.Fatalf("DeleteAll(%s/%s) = %v", tc.otherProject, tc.otherAgent, err)
			}

			pod, err := client.CoreV1().Pods("agentbox").Get(ctx, k8s.PodName(tc.project, tc.agent), metav1.GetOptions{})
			if err != nil {
				t.Fatalf("destroying %s/%s took %s's pod: %v", tc.otherProject, tc.otherAgent, tc.name, err)
			}
			if pod.UID != made.UID {
				t.Errorf("the pod is %s, want %s's", pod.UID, tc.name)
			}
			claim, err := client.CoreV1().PersistentVolumeClaims("agentbox").Get(ctx, k8s.ClaimName(tc.project, tc.agent), metav1.GetOptions{})
			if err != nil {
				t.Fatalf("destroying %s/%s took %s's claim: %v", tc.otherProject, tc.otherAgent, tc.name, err)
			}
			if got := claim.Labels[k8s.LabelProject] + "/" + claim.Labels[k8s.LabelAgent]; got != tc.project+"/"+tc.agent {
				t.Errorf("the claim belongs to %s, want %s", got, tc.project+"/"+tc.agent)
			}
			secret, err := client.CoreV1().Secrets("agentbox").Get(ctx, k8s.PodTokenSecretName(tc.project, tc.agent), metav1.GetOptions{})
			if err != nil {
				t.Fatalf("destroying %s/%s took %s's token: %v", tc.otherProject, tc.otherAgent, tc.name, err)
			}
			if got := string(secret.Data["token"]); got != testToken {
				t.Errorf("the token = %q, want %s's", got, tc.name)
			}
		})
	}
}
