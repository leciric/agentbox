package k8s_test

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"agentbox/internal/k8s"
)

// healthyCluster is a fake cluster with a ready node, the storage class, the
// pull secret, and the controller that binds claims and starts pods.
func healthyCluster(t *testing.T) *fake.Clientset {
	t.Helper()
	client := fake.NewClientset(
		// The namespace as `cluster add` creates it.
		k8s.NewAccess("agentbox").Namespace,
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    mustQuantity("3500m"),
					corev1.ResourceMemory: mustQuantity("8Gi"),
				},
			},
		},
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "local-path"}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "forgejo-pull", Namespace: "agentbox"},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		},
		// The policy `cluster add` applies. An address instead of a name keeps
		// the build from asking a resolver, and the doctor doesn't compare the
		// policy against the master URL anyway.
		k8s.NetPol("agentbox", "https://10.8.8.1:443"),
	)
	client.PrependReactor("get", "persistentvolumeclaims", func(action k8stesting.Action) (bool, runtime.Object, error) {
		get := action.(k8stesting.GetAction)
		object, err := client.Tracker().Get(action.GetResource(), get.GetNamespace(), get.GetName())
		if err != nil {
			return true, nil, err
		}
		claim := object.(*corev1.PersistentVolumeClaim).DeepCopy()
		claim.Status.Phase = corev1.ClaimBound
		return true, claim, nil
	})
	client.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		get := action.(k8stesting.GetAction)
		object, err := client.Tracker().Get(action.GetResource(), get.GetNamespace(), get.GetName())
		if err != nil {
			return true, nil, err
		}
		pod := object.(*corev1.Pod).DeepCopy()
		pod.Status.Phase = corev1.PodRunning
		return true, pod, nil
	})
	return client
}

func healthyDoctor(t *testing.T, client *fake.Clientset) k8s.Doctor {
	t.Helper()
	return k8s.Doctor{
		Cluster:      k8s.FromClient("k3d", "agentbox", client),
		StorageClass: "local-path",
		Image:        "hub.example.com/h4ad/agentbox-agent:dev",
		PullSecret:   "forgejo-pull",
		MasterURL:    "https://master.example.test",
		Timeout:      2 * time.Second,
		Suffix:       "test",
	}
}

// check finds one of the doctor's checks by name.
func check(t *testing.T, report k8s.Report, name string) k8s.Check {
	t.Helper()
	for _, found := range report.Checks {
		if found.Name == name {
			return found
		}
	}
	t.Fatalf("the doctor never looked at %s: %+v", name, report.Checks)
	return k8s.Check{}
}

func TestDoctorPassesAClusterThatCanRunAgents(t *testing.T) {
	client := healthyCluster(t)
	report := healthyDoctor(t, client).Run(context.Background())
	if report.Failed() {
		t.Errorf("a healthy cluster failed: %+v", report.Checks)
	}
	for _, name := range []string{"api", "master url", "namespace", "pod security", "network policy", "nodes", "storage class", "workspace claim", "registry", "image"} {
		found := check(t, report, name)
		if !found.OK || found.Skipped {
			t.Errorf("%s = %+v, want a pass", name, found)
		}
	}
	if got := check(t, report, "nodes").Detail; !strings.Contains(got, "1 of 1 ready") || !strings.Contains(got, "3500m") {
		t.Errorf("the node detail = %q, want the count and the room", got)
	}
	// Nothing the doctor made is left behind.
	pods, err := client.CoreV1().Pods("agentbox").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, pod := range pods.Items {
		if strings.HasPrefix(pod.Name, "agentbox-doctor-") {
			t.Errorf("the doctor left pod %s behind", pod.Name)
		}
	}
	claims, err := client.CoreV1().PersistentVolumeClaims("agentbox").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(claims.Items) != 0 {
		t.Errorf("the doctor left %d claims behind", len(claims.Items))
	}
}

func TestDoctorSaysWhatIsMissing(t *testing.T) {
	client := fake.NewClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}},
			},
		},
	)
	// The image cannot be pulled, which is what a private image with no pull
	// secret looks like from the outside.
	client.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		get := action.(k8stesting.GetAction)
		object, err := client.Tracker().Get(action.GetResource(), get.GetNamespace(), get.GetName())
		if err != nil {
			return true, nil, err
		}
		pod := object.(*corev1.Pod).DeepCopy()
		pod.Status.Phase = corev1.PodPending
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason:  "ImagePullBackOff",
				Message: "Back-off pulling image",
			}},
		}}
		return true, pod, nil
	})
	doctor := healthyDoctor(t, client)
	doctor.StorageClass = ""
	doctor.PullSecret = ""
	doctor.MasterURL = ""
	doctor.Timeout = 100 * time.Millisecond
	report := doctor.Run(context.Background())
	if !report.Failed() {
		t.Fatal("a cluster that can't run agents passed")
	}
	if got := check(t, report, "master url"); got.OK || !strings.Contains(got.Detail, "--master-url") {
		t.Errorf("master url = %+v, want it to name the command that sets it", got)
	}
	if got := check(t, report, "nodes"); got.OK || !strings.Contains(got.Detail, "worker-1") {
		t.Errorf("nodes = %+v, want it to name the node that isn't ready", got)
	}
	if got := check(t, report, "storage class"); !got.Skipped {
		t.Errorf("storage class = %+v, want it skipped", got)
	}
	if got := check(t, report, "registry"); !got.Skipped {
		t.Errorf("registry = %+v, want it skipped", got)
	}
	if got := check(t, report, "image"); got.OK || !strings.Contains(got.Detail, "ImagePullBackOff") {
		t.Errorf("image = %+v, want the pull failure", got)
	}
}

func TestDoctorFailsAClusterWithNoImage(t *testing.T) {
	// The reconciler refuses every pod without an image, so a doctor that
	// skipped the check would pass a cluster that can start nothing.
	client := healthyCluster(t)
	doctor := healthyDoctor(t, client)
	doctor.Image = ""
	report := doctor.Run(context.Background())
	if !report.Failed() {
		t.Fatal("a cluster with no image passed")
	}
	got := check(t, report, "image")
	if got.OK || got.Skipped {
		t.Errorf("image = %+v, want a failure", got)
	}
	if !strings.Contains(got.Detail, "agentbox cluster set k3d --image") {
		t.Errorf("image detail = %q, want the command that sets one", got.Detail)
	}
}

func TestDoctorGivesUpOnAClaimThatNeverBinds(t *testing.T) {
	// A cluster with nothing behind the class: the claim is created and stays
	// pending.
	client := fake.NewClientset()
	doctor := healthyDoctor(t, client)
	doctor.Timeout = 50 * time.Millisecond
	report := doctor.Run(context.Background())
	got := check(t, report, "workspace claim")
	if got.OK || !strings.Contains(got.Detail, "still pending") {
		t.Errorf("workspace claim = %+v, want it to say it is still pending", got)
	}
}

func TestDoctorTakesAClaimItsClassBindsOnUse(t *testing.T) {
	// k3s's local-path, and every local volume, keeps a claim Pending until a
	// pod mounts it: waiting for it to bind on its own would fail a cluster
	// that is fine.
	wait := storagev1.VolumeBindingWaitForFirstConsumer
	client := fake.NewClientset(&storagev1.StorageClass{
		ObjectMeta:        metav1.ObjectMeta{Name: "local-path"},
		VolumeBindingMode: &wait,
	})
	doctor := healthyDoctor(t, client)
	doctor.StorageClass = "local-path"
	doctor.Timeout = 50 * time.Millisecond
	report := doctor.Run(context.Background())
	if got := check(t, report, "storage class"); !got.OK || got.Detail != "local-path" {
		t.Errorf("storage class = %+v, want the class the config names", got)
	}
	if got := check(t, report, "workspace claim"); !got.OK || !strings.Contains(got.Detail, "binds it when a pod mounts it") {
		t.Errorf("workspace claim = %+v, want it to say the class binds on first use", got)
	}
}

func TestDoctorNamesTheClustersDefaultClass(t *testing.T) {
	client := fake.NewClientset(&storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "local-path",
			Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"},
		},
	})
	doctor := healthyDoctor(t, client)
	doctor.StorageClass = ""
	doctor.Timeout = 50 * time.Millisecond
	report := doctor.Run(context.Background())
	if got := check(t, report, "storage class"); !got.OK || !strings.Contains(got.Detail, "local-path (the cluster's default)") {
		t.Errorf("storage class = %+v, want the cluster's default named", got)
	}
}

func TestDoctorSaysAnHTTPMasterIsUnencrypted(t *testing.T) {
	client := healthyCluster(t)
	doctor := healthyDoctor(t, client)
	doctor.MasterURL = "http://host.k3d.internal:7782"
	report := doctor.Run(context.Background())
	got := check(t, report, "master url")
	if !got.OK {
		t.Errorf("master url = %+v, want http accepted for a local test cluster", got)
	}
	if !strings.Contains(got.Detail, "unencrypted") {
		t.Errorf("master url detail = %q, want it to say the address is unencrypted", got.Detail)
	}
}

func TestDoctorChecksTheTailscaleEgress(t *testing.T) {
	client := healthyCluster(t)
	ctx := context.Background()
	host, ok := k8s.MasterEgressHost("https://master.example.test")
	if !ok {
		t.Fatal("the master URL gave no egress host")
	}
	if err := k8s.FromClient("k3d", "agentbox", client).EnsureEgress(ctx, []k8s.EgressHost{host}); err != nil {
		t.Fatal(err)
	}
	doctor := healthyDoctor(t, client)
	doctor.Egress = []k8s.EgressHost{host}
	report := doctor.Run(ctx)
	if report.Failed() {
		t.Errorf("a cluster with a right egress Service failed: %+v", report.Checks)
	}
	got := check(t, report, "tailnet "+host.Host)
	if !got.OK || !strings.Contains(got.Detail, host.Name) {
		t.Errorf("the egress check = %+v, want it passing and naming the Service", got)
	}
}

func TestDoctorSaysWhenTheNetworkPolicyIsMissing(t *testing.T) {
	// A cluster whose policy never got applied, which is every cluster added
	// before the policy existed until it is rotated.
	client := healthyCluster(t)
	ctx := context.Background()
	if err := client.NetworkingV1().NetworkPolicies("agentbox").Delete(ctx, k8s.NetPolName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	report := healthyDoctor(t, client).Run(ctx)
	got := check(t, report, "network policy")
	if got.OK || got.Skipped {
		t.Errorf("network policy = %+v, want a failure", got)
	}
	if !strings.Contains(got.Detail, "agentbox cluster rotate k3d") {
		t.Errorf("network policy detail = %q, want the command that applies it", got.Detail)
	}
}

func TestDoctorSaysWhenTheNetworkPolicyLetsTrafficIn(t *testing.T) {
	client := healthyCluster(t)
	ctx := context.Background()
	policy, err := client.NetworkingV1().NetworkPolicies("agentbox").Get(ctx, k8s.NetPolName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	policy.Spec.Ingress = append(policy.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{})
	if _, err := client.NetworkingV1().NetworkPolicies("agentbox").Update(ctx, policy, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	report := healthyDoctor(t, client).Run(ctx)
	got := check(t, report, "network policy")
	if got.OK || got.Skipped {
		t.Errorf("network policy = %+v, want a failure", got)
	}
	if !strings.Contains(got.Detail, "agentbox cluster rotate k3d") {
		t.Errorf("network policy detail = %q, want the command that applies it", got.Detail)
	}
}

func mustQuantity(s string) resource.Quantity { return resource.MustParse(s) }

// TestDoctorFailsANamespaceThatAdmitsPrivilegedPods is the check that keeps the
// sealed credential inside its namespace: it can create pods, so a namespace
// with no pod security label lets it be root on a node.
func TestDoctorFailsANamespaceThatAdmitsPrivilegedPods(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
		fix    string
	}{
		// Added before the label: rotate applies it.
		{"ours", map[string]string{k8s.LabelManaged: "true"}, "agentbox cluster rotate k3d"},
		// Someone else's: never relabelled, so the doctor says how to.
		{"foreign", nil, "kubectl label namespace agentbox pod-security.kubernetes.io/enforce=baseline"},
		{"privileged", map[string]string{k8s.LabelPodSecurity: "privileged"}, "kubectl label"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := healthyCluster(t)
			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "agentbox", Labels: tc.labels}}
			if _, err := client.CoreV1().Namespaces().Update(context.Background(), namespace, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			report := healthyDoctor(t, client).Run(context.Background())
			got := check(t, report, "pod security")
			if got.OK || got.Skipped || !report.Failed() {
				t.Fatalf("pod security = %+v, want a failure", got)
			}
			if !strings.Contains(got.Detail, tc.fix) {
				t.Errorf("pod security detail = %q, want it to say %q", got.Detail, tc.fix)
			}
		})
	}
}

// TestDoctorSaysWhenItCantReadTheNamespace is a cluster added before the reader
// ClusterRole could read the namespace: a failure that names the fix, not a pass.
func TestDoctorSaysWhenItCantReadTheNamespace(t *testing.T) {
	client := healthyCluster(t)
	client.PrependReactor("get", "namespaces", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(corev1.Resource("namespaces"), "agentbox", nil)
	})
	got := check(t, healthyDoctor(t, client).Run(context.Background()), "pod security")
	if got.OK || !strings.Contains(got.Detail, "agentbox cluster rotate k3d") {
		t.Errorf("pod security = %+v, want a failure that names cluster rotate", got)
	}
}
