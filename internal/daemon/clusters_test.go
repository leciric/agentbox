package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"

	"agentbox/internal/api"
	"agentbox/internal/k8s"
	"agentbox/internal/state"
)

// egressAPI is a stub API server with just what `cluster add` and the egress
// call that follows it need: it accepts the objects AddAccess applies, fills the
// token Secret in, and serves a new namespace's Services as a real one would.
// The add path speaks HTTP to whatever kubeconfig it is handed, so a fake
// clientset can't stand in for it.
//
// client-go sends those objects as protobuf, which is why the stub decodes with
// the clients' own scheme rather than encoding JSON by hand.
type egressAPI struct {
	mu       sync.Mutex
	services map[string]*corev1.Service
}

func (a *egressAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.serveGet(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "the stub only creates and reads", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	object, _, err := scheme.Codecs.UniversalDeserializer().Decode(raw, nil, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if secret, ok := object.(*corev1.Secret); ok {
		secret.Data = map[string][]byte{corev1.ServiceAccountTokenKey: []byte("the-service-account-token")}
	}
	if service, ok := object.(*corev1.Service); ok {
		service.ResourceVersion = "1"
		a.mu.Lock()
		if a.services == nil {
			a.services = map[string]*corev1.Service{}
		}
		a.services[service.Name] = service
		a.mu.Unlock()
	}
	writeStubJSON(w, http.StatusCreated, object)
}

func (a *egressAPI) serveGet(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	a.mu.Lock()
	service, ok := a.services[name]
	a.mu.Unlock()
	if !ok {
		writeStubJSON(w, http.StatusNotFound, metav1.Status{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
			Status:   metav1.StatusFailure,
			Reason:   metav1.StatusReasonNotFound,
			Code:     http.StatusNotFound,
			Message:  "not found",
		})
		return
	}
	writeStubJSON(w, http.StatusOK, service)
}

func (a *egressAPI) service(t *testing.T, name string) *corev1.Service {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	service, ok := a.services[name]
	if !ok {
		t.Fatalf("the egress Service %s was never created: %v", name, a.services)
	}
	return service
}

func writeStubJSON(w http.ResponseWriter, code int, object any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(object)
}

// adminKubeconfig is an admin kubeconfig for the stub API server.
func adminKubeconfig(server string) string {
	return `apiVersion: v1
kind: Config
clusters:
- name: stub
  cluster:
    server: ` + server + `
    insecure-skip-tls-verify: true
users:
- name: stub
  user:
    token: the-admin-token
contexts:
- name: stub
  context:
    cluster: stub
    user: stub
current-context: stub
`
}

// TestClusterAddDeclaresTheEgressService is the wiring `cluster add --tailnet
// operator` needs: recording the choice is not enough, the operator has to be
// asked for the Service that makes the master reachable.
func TestClusterAddDeclaresTheEgressService(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	stub := &egressAPI{}
	server := httptest.NewTLSServer(stub)
	t.Cleanup(server.Close)
	body, err := json.Marshal(api.AddClusterRequest{
		Name:       "k3d",
		Namespace:  "agentbox",
		MasterURL:  "https://nas.example.ts.net",
		Tailnet:    state.TailnetOperator,
		Kubeconfig: adminKubeconfig(server.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/clusters", strings.NewReader(string(body)))
	if err := srv.addCluster(httptest.NewRecorder(), req); err != nil {
		t.Fatalf("addCluster() = %v", err)
	}
	service := stub.service(t, k8s.EgressMasterService)
	if service.Spec.ExternalName != "nas.example.ts.net" {
		t.Errorf("external name = %q, want the master URL's name", service.Spec.ExternalName)
	}
	if got := service.Annotations[k8s.AnnotationProxyGroup]; got != k8s.EgressProxyGroup {
		t.Errorf("%s = %q, want %s", k8s.AnnotationProxyGroup, got, k8s.EgressProxyGroup)
	}
	cluster, err := srv.store.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatalf("the row wasn't kept: %v", err)
	}
	if cluster.Tailnet != state.TailnetOperator {
		t.Errorf("the row's tailnet = %q, want operator", cluster.Tailnet)
	}
}

// TestClusterDoctorChecksTheEgressService is the read-only half: doctor asks
// for the operator's Service and never creates it.
func TestClusterDoctorChecksTheEgressService(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	bindsOnUse := storagev1.VolumeBindingWaitForFirstConsumer
	client := fake.NewClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			},
		},
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "local-path"}, VolumeBindingMode: &bindsOnUse},
	)
	cluster := reconcilerCluster()
	cluster.Tailnet = state.TailnetOperator
	cluster.StorageClass = "local-path"
	// No image: the doctor's image check fails at once instead of waiting for a
	// pod to run, and that is not what this test is about.
	cluster.Image = ""
	kc := clusterWithFakeClient(t, srv, cluster, client)
	host, ok := k8s.MasterEgressHost(cluster.MasterURL)
	if !ok {
		t.Fatal("the cluster's master URL gave no egress host")
	}
	if err := kc.EnsureEgress(ctx, []k8s.EgressHost{host}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/clusters/k3d/doctor", nil)
	req.SetPathValue("name", cluster.Name)
	response := httptest.NewRecorder()
	if err := srv.doctorCluster(response, req); err != nil {
		t.Fatalf("doctorCluster() = %v", err)
	}
	var report api.ClusterReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatalf("the report isn't JSON: %v\n%s", err, response.Body.String())
	}
	for _, found := range report.Checks {
		if found.Name != "tailnet "+host.Host {
			continue
		}
		if !found.OK || found.Skipped {
			t.Errorf("the tailnet check = %+v, want it passing", found)
		}
		return
	}
	t.Errorf("the doctor never looked at the egress Service: %+v", report.Checks)
}

// TestClusterSetDeclaresTheEgressService is the same wiring on the change path,
// with a new master URL.
func TestClusterSetDeclaresTheEgressService(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	client := fake.NewClientset()
	clusterWithFakeClient(t, srv, reconcilerCluster(), client)
	cfg := api.ClusterConfig{
		MasterURL: "https://nas.example.ts.net",
		Tailnet:   state.TailnetOperator,
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/v1/clusters/k3d", strings.NewReader(string(body)))
	req.SetPathValue("name", "k3d")
	if err := srv.setClusterConfig(httptest.NewRecorder(), req); err != nil {
		t.Fatalf("setClusterConfig() = %v", err)
	}
	service, err := client.CoreV1().Services("agentbox").Get(ctx, k8s.EgressMasterService, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the master's egress Service wasn't declared: %v", err)
	}
	if service.Spec.ExternalName != "nas.example.ts.net" {
		t.Errorf("external name = %q, want the master URL's name", service.Spec.ExternalName)
	}
	if got := service.Annotations[k8s.AnnotationProxyGroup]; got != k8s.EgressProxyGroup {
		t.Errorf("%s = %q, want %s", k8s.AnnotationProxyGroup, got, k8s.EgressProxyGroup)
	}
}

// TestClusterSetWithTailnetNoneDeclaresNothing keeps the operator's objects out
// of a cluster that doesn't ask for them.
func TestClusterSetWithTailnetNoneDeclaresNothing(t *testing.T) {
	srv := reconcilerServer(t)
	client := fake.NewClientset()
	clusterWithFakeClient(t, srv, reconcilerCluster(), client)
	cfg := api.ClusterConfig{MasterURL: "https://nas.example.ts.net", Tailnet: state.TailnetNone}
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/v1/clusters/k3d", strings.NewReader(string(body)))
	req.SetPathValue("name", "k3d")
	if err := srv.setClusterConfig(httptest.NewRecorder(), req); err != nil {
		t.Fatalf("setClusterConfig() = %v", err)
	}
	if _, err := client.CoreV1().Services("agentbox").Get(context.Background(), k8s.EgressMasterService, metav1.GetOptions{}); err == nil {
		t.Error("a cluster with --tailnet none got an egress Service")
	}
}

// TestClusterSetFailureSaysTheOperatorIsMissing is the failure the user has to
// be able to act on: a cluster the script didn't prepare has no operator, and
// the error says so rather than only that a Service couldn't be made.
func TestClusterSetFailureSaysTheOperatorIsMissing(t *testing.T) {
	srv := reconcilerServer(t)
	client := fake.NewClientset()
	client.PrependReactor("create", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("the server could not find the requested resource")
	})
	clusterWithFakeClient(t, srv, reconcilerCluster(), client)
	cfg := api.ClusterConfig{MasterURL: "https://nas.example.ts.net", Tailnet: state.TailnetOperator}
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/v1/clusters/k3d", strings.NewReader(string(body)))
	req.SetPathValue("name", "k3d")
	err = srv.setClusterConfig(httptest.NewRecorder(), req)
	if err == nil {
		t.Fatal("setClusterConfig() = nil, want the missing operator to fail it")
	}
	for _, want := range []string{"saved", "Tailscale operator", "scripts/k3d-up.sh --operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error = %q, want it to say %q", err, want)
		}
	}
}
