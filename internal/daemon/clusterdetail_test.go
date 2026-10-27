package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// clusterDetailRequest is the page's read, with the path value the router
// would have set.
func clusterDetailRequest(name string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/clusters/"+name+"/detail", nil)
	r.SetPathValue("name", name)
	return r
}

// queueForTest puts one agent in the project's queue with the create request it
// would start from.
func queueForTest(t *testing.T, srv *Server, project, name, cluster, reason string) {
	t.Helper()
	request, err := json.Marshal(queuedRequest{Request: api.CreateAgentRequest{Project: project, Name: name, AI: "none", Cluster: cluster}})
	if err != nil {
		t.Fatal(err)
	}
	a := state.Agent{Project: project, Name: name, Instance: agent.InstanceName(project, name), Status: state.AgentQueued, CreatedAt: time.Now()}
	if err := srv.store.QueueAgent(context.Background(), a, request, reason); err != nil {
		t.Fatal(err)
	}
}

// TestClusterDetailListsPodsClaimsEventsAndTheQueue is the detail end to end:
// the cluster's own objects, its events, and the queued agents waiting for it,
// each mapped to what the page draws.
func TestClusterDetailListsPodsClaimsEventsAndTheQueue(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	cluster := reconcilerCluster()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ab-pawly-agent-01",
			Namespace: "agentbox",
			Labels: map[string]string{
				"agentbox/managed": "true",
				"agentbox/project": "pawly",
				"agentbox/agent":   "agent-01",
			},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second)),
		},
		Spec: corev1.PodSpec{
			NodeName:   "worker-1",
			Containers: []corev1.Container{{Name: "agent", Image: "agentbox-agent:dev"}},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ab-pawly-agent-01-workspace",
			Namespace: "agentbox",
			Labels: map[string]string{
				"agentbox/managed": "true",
				"agentbox/project": "pawly",
				"agentbox/agent":   "agent-01",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("40Gi")},
			},
		},
	}
	said := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "ab-pawly-agent-01.started", Namespace: "agentbox"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "ab-pawly-agent-01"},
		Type:           "Normal",
		Reason:         "Started",
		Message:        "Started container agent",
		Count:          2,
		LastTimestamp:  metav1.NewTime(time.Now().Add(-30 * time.Minute).Truncate(time.Second)),
	}
	client := fake.NewClientset(pod, claim, said)
	clusterWithFakeClient(t, srv, cluster, client)
	if err := srv.store.SetProjectCluster(ctx, "pawly", "k3d"); err != nil {
		t.Fatal(err)
	}
	// One request naming this cluster, one following the project, and one for
	// another cluster: only the first two are this cluster's queue.
	queueForTest(t, srv, "pawly", "agent-02", "k3d", "it is at its ceiling of 2 agents")
	queueForTest(t, srv, "pawly", "agent-03", "", "its nodes have no room for one more agent")
	queueForTest(t, srv, "pawly", "agent-04", "nas", "it is at its ceiling of 2 agents")

	rec := httptest.NewRecorder()
	if err := srv.clusterDetail(rec, clusterDetailRequest("k3d")); err != nil {
		t.Fatalf("clusterDetail() = %v", err)
	}
	var detail api.ClusterDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("the detail isn't JSON: %v\n%s", err, rec.Body.String())
	}

	if detail.Cluster.Name != "k3d" {
		t.Errorf("the cluster = %+v, want k3d", detail.Cluster)
	}
	if detail.Cluster.LastSeen == nil || detail.Cluster.LastError != "" {
		t.Errorf("the cluster's health = %+v, want it seen and well", detail.Cluster)
	}
	if len(detail.Pods) != 1 {
		t.Fatalf("pods = %+v, want the one", detail.Pods)
	}
	if pod := detail.Pods[0]; pod.Name != "ab-pawly-agent-01" || pod.Project != "pawly" || pod.Agent != "agent-01" ||
		pod.Phase != "Running" || !pod.Ready || pod.Node != "worker-1" || pod.Image != "agentbox-agent:dev" {
		t.Errorf("the pod = %+v, want it mapped from its labels and status", pod)
	}
	if len(detail.Claims) != 1 {
		t.Fatalf("claims = %+v, want the one", detail.Claims)
	}
	if claim := detail.Claims[0]; claim.Agent != "agent-01" || claim.Storage != "40Gi" {
		t.Errorf("the claim = %+v, want pawly/agent-01's 40Gi", claim)
	}
	if len(detail.Events) != 1 {
		t.Fatalf("events = %+v, want the one", detail.Events)
	}
	if event := detail.Events[0]; event.Object != "Pod/ab-pawly-agent-01" || event.Reason != "Started" || event.Count != 2 {
		t.Errorf("the event = %+v, want it mapped to Kind/Name with its count", event)
	}
	if len(detail.Queue) != 2 {
		t.Fatalf("queue = %+v, want the two waiting for k3d", detail.Queue)
	}
	if detail.Queue[0].Agent != "agent-02" || detail.Queue[0].Position != 1 || detail.Queue[0].Reason == "" {
		t.Errorf("the first queued agent = %+v, want agent-02 first with its reason", detail.Queue[0])
	}
	if detail.Queue[1].Agent != "agent-03" || detail.Queue[1].Position != 2 {
		t.Errorf("the second queued agent = %+v, want agent-03 following the project", detail.Queue[1])
	}
}

// TestClusterDetailShowsTheClusterWhenItCannotBeReached: an unreachable cluster
// is a page with the cluster, why it could not be read and its queue, not an
// error. The lists from the cluster are empty and Error says why; the cluster's
// health is what the doctor found and is left alone.
func TestClusterDetailShowsTheClusterWhenItCannotBeReached(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	cluster := reconcilerCluster()
	// Sealed bytes nothing can open: the client can't be built, which is a
	// cluster whose token was rotated away or whose secrets key changed.
	cluster.Kubeconfig = []byte("not-a-sealed-kubeconfig")
	if err := srv.store.AddCluster(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	queueForTest(t, srv, "pawly", "agent-02", "k3d", "it is at its ceiling of 2 agents")

	rec := httptest.NewRecorder()
	if err := srv.clusterDetail(rec, clusterDetailRequest("k3d")); err != nil {
		t.Fatalf("clusterDetail() over an unreachable cluster = %v, want the page still", err)
	}
	var detail api.ClusterDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("the detail isn't JSON: %v\n%s", err, rec.Body.String())
	}
	if detail.Cluster.Name != "k3d" || detail.Error == "" {
		t.Errorf("the cluster = %+v, want it named with why it couldn't be read", detail.Cluster)
	}
	if len(detail.Pods) != 0 || len(detail.Claims) != 0 || len(detail.Events) != 0 {
		t.Errorf("the lists = %+v/%+v/%+v, want them empty", detail.Pods, detail.Claims, detail.Events)
	}
	if len(detail.Queue) != 1 || detail.Queue[0].Agent != "agent-02" {
		t.Errorf("queue = %+v, want the one waiting for it", detail.Queue)
	}
	stored, err := srv.store.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if stored.LastError != "" {
		t.Errorf("the cluster's health = %q after a failed detail read, want the doctor's word kept", stored.LastError)
	}
}

// TestClusterDetailOfAnUnknownClusterIsNotFound: a name nothing knows is the
// 404 every other cluster route answers.
func TestClusterDetailOfAnUnknownClusterIsNotFound(t *testing.T) {
	srv := reconcilerServer(t)
	err := srv.clusterDetail(httptest.NewRecorder(), clusterDetailRequest("ghost"))
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("clusterDetail() over an unknown cluster = %v, want ErrNotFound", err)
	}
}
