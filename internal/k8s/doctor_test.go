package k8s_test

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
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
	for _, name := range []string{"api", "namespace", "nodes", "storage class", "workspace claim", "registry", "image"} {
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
	doctor.Timeout = 100 * time.Millisecond
	report := doctor.Run(context.Background())
	if !report.Failed() {
		t.Fatal("a cluster that can't run agents passed")
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

func mustQuantity(s string) resource.Quantity { return resource.MustParse(s) }
