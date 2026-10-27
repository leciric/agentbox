package k8s

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// archiveSpec is the agent whose claim these tests archive and restore. It is
// this package's own copy of the fixture in spec_test.go, because these tests
// are inside the package: the wait and the naming under test are unexported.
func archiveSpec() Spec {
	return Spec{
		Cluster:      "k3d",
		Project:      "pawly",
		Agent:        "agent-01",
		Image:        "hub.example.com/h4ad/agentbox-agent:dev",
		FromRegistry: true,
		PullSecret:   "agentbox-forgejo-pull",
	}
}

// readyOnCreate has the fake cluster run a pod the moment it is made, as a node
// with the image already would: the operations that go past the wait need a pod
// a command can run in.
func readyOnCreate(client *fake.Clientset) {
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(k8stesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		pod, ok := create.GetObject().(*corev1.Pod)
		if !ok {
			return false, nil, nil
		}
		pod.Status = readyStatus()
		// Not handled: the clientset stores the pod it was given, with the
		// status a node would have set.
		return false, nil, nil
	})
}

func readyStatus() corev1.PodStatus {
	return corev1.PodStatus{
		Phase:      corev1.PodRunning,
		Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
	}
}

// makeClaim puts the agent's claim in the fake cluster.
func makeClaim(t *testing.T, client *fake.Clientset, spec Spec) {
	t.Helper()
	claim, err := BuildPVC(spec)
	if err != nil {
		t.Fatal(err)
	}
	claim.Namespace = "agentbox"
	if _, err := client.CoreV1().PersistentVolumeClaims("agentbox").Create(context.Background(), claim, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestHelperPodMountsTheClaimAndRunsNothing(t *testing.T) {
	spec := archiveSpec()
	name := HelperPodName(spec.Project, spec.Agent, "snapshot-1")
	pod, err := BuildHelperPod(spec, name, spec.Image)
	if err != nil {
		t.Fatalf("BuildHelperPod() = %v", err)
	}
	if pod.Name != name {
		t.Errorf("name = %q, want %q", pod.Name, name)
	}
	// It belongs to no agent: the reconciler selects managed=true, and a helper
	// that answered it would be adopted as one.
	if len(pod.Labels) != 2 || pod.Labels["agentbox/helper"] != "archive" ||
		pod.Labels["app.kubernetes.io/name"] != "agentbox-archive" {
		t.Errorf("labels = %v, want the helper's own two and nothing else", pod.Labels)
	}
	if pod.Labels[LabelManaged] != "" || pod.Labels[LabelProject] != "" || pod.Labels[LabelAgent] != "" {
		t.Errorf("the helper carries an agent's labels: %v", pod.Labels)
	}

	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %q, want Never", pod.Spec.RestartPolicy)
	}
	if pod.Spec.TerminationGracePeriodSeconds == nil || *pod.Spec.TerminationGracePeriodSeconds != 0 {
		t.Errorf("the grace period = %v, want 0", pod.Spec.TerminationGracePeriodSeconds)
	}
	if pod.Spec.EnableServiceLinks == nil || *pod.Spec.EnableServiceLinks {
		t.Errorf("service links = %v, want off", pod.Spec.EnableServiceLinks)
	}
	if len(pod.Spec.ImagePullSecrets) != 1 || pod.Spec.ImagePullSecrets[0].Name != "agentbox-forgejo-pull" {
		t.Errorf("pull secrets = %+v, want the registry's", pod.Spec.ImagePullSecrets)
	}

	// The agent's user, and never root.
	sc := pod.Spec.SecurityContext
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot ||
		sc.RunAsUser == nil || *sc.RunAsUser != AgentUser ||
		sc.RunAsGroup == nil || *sc.RunAsGroup != AgentUser ||
		sc.FSGroup == nil || *sc.FSGroup != AgentUser {
		t.Errorf("the pod's security context = %+v, want user and group %d, not root", sc, AgentUser)
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("the pod's seccomp profile = %+v, want RuntimeDefault", sc.SeccompProfile)
	}
	// The agent's own pod sets it too: a claim whose root is already the
	// group's doesn't need a walk over every file in it before tar runs.
	if sc.FSGroupChangePolicy == nil || *sc.FSGroupChangePolicy != corev1.FSGroupChangeOnRootMismatch {
		t.Errorf("the fsGroup policy = %v, want OnRootMismatch", sc.FSGroupChangePolicy)
	}

	// One container, waiting for the exec that follows.
	if len(pod.Spec.InitContainers) != 0 {
		t.Errorf("the helper has %d init containers, want none", len(pod.Spec.InitContainers))
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("the helper has %d containers, want the one", len(pod.Spec.Containers))
	}
	c := pod.Spec.Containers[0]
	if c.Name != "agent" {
		t.Errorf("container name = %q, want agent, which is the name a command is run against", c.Name)
	}
	if c.Image != spec.Image {
		t.Errorf("image = %q, want %q", c.Image, spec.Image)
	}
	if c.ImagePullPolicy != corev1.PullAlways {
		t.Errorf("pull policy = %q, want Always from a registry", c.ImagePullPolicy)
	}
	if got, want := strings.Join(c.Command, " "), "sleep infinity"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	if len(c.Args) != 0 {
		t.Errorf("args = %v, want none", c.Args)
	}
	if c.SecurityContext == nil || c.SecurityContext.AllowPrivilegeEscalation == nil ||
		*c.SecurityContext.AllowPrivilegeEscalation {
		t.Errorf("the container's security context = %+v, want no privilege escalation", c.SecurityContext)
	}
	if c.SecurityContext.Capabilities == nil || len(c.SecurityContext.Capabilities.Drop) != 1 ||
		c.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Errorf("the container's capabilities = %+v, want every one dropped", c.SecurityContext.Capabilities)
	}
	if len(c.VolumeMounts) != 1 {
		t.Fatalf("the container mounts %+v, want the claim at /mnt", c.VolumeMounts)
	}
	claimed := c.VolumeMounts[0]
	if claimed.MountPath != helperMount || claimed.ReadOnly || claimed.SubPath != "" {
		t.Errorf("the claim's mount = %+v, want all of it at %s, read-write", claimed, helperMount)
	}

	if len(pod.Spec.Volumes) != 1 {
		t.Fatalf("the pod has %+v, want the one claim", pod.Spec.Volumes)
	}
	if pod.Spec.Volumes[0].Name != claimed.Name {
		t.Errorf("the mount names %q and the volume %q", claimed.Name, pod.Spec.Volumes[0].Name)
	}
	pvc := pod.Spec.Volumes[0].PersistentVolumeClaim
	if pvc == nil || pvc.ClaimName != "ab-pawly-agent-01-workspace" {
		t.Errorf("the volume = %+v, want the agent's claim", pvc)
	}

	// A restore makes a new claim, and the helper follows the spec's.
	spec.Claim = "ab-pawly-agent-01-workspace-restored"
	restored, err := BuildHelperPod(spec, HelperPodName(spec.Project, spec.Agent, "restore"), spec.Image)
	if err != nil {
		t.Fatalf("BuildHelperPod() on a named claim = %v", err)
	}
	if got := restored.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; got != spec.Claim {
		t.Errorf("the volume names %q, want the claim the spec names, %q", got, spec.Claim)
	}
}

func TestHelperPodRefusesWhatItCannotRun(t *testing.T) {
	spec := archiveSpec()
	if _, err := BuildHelperPod(spec, "ab-pawly-agent-01-archive-x", ""); err == nil || !strings.Contains(err.Error(), "no image") {
		t.Errorf("BuildHelperPod() with no image = %v, want it to say there is none", err)
	}
	if _, err := BuildHelperPod(spec, "", spec.Image); err == nil || !strings.Contains(err.Error(), "no name") {
		t.Errorf("BuildHelperPod() with no name = %v, want it to say there is none", err)
	}
	spec.Agent = ""
	if _, err := BuildHelperPod(spec, "ab-pawly--archive-x", spec.Image); err == nil || !strings.Contains(err.Error(), "project and an agent") {
		t.Errorf("BuildHelperPod() for no agent = %v, want it to say which one is missing", err)
	}
}

func TestHelperPodNameStaysALabel(t *testing.T) {
	for _, tc := range []struct{ name, project, agent, tag string }{
		{name: "an ordinary agent", project: "pawly", agent: "agent-01", tag: "nightly-2026-10-27"},
		{name: "a project at the limit", project: strings.Repeat("p", 50), agent: "agent-01", tag: "snap"},
		{name: "an agent at the limit", project: "pawly", agent: strings.Repeat("a", 59), tag: "snap"},
		{name: "both at the limit", project: strings.Repeat("p", 60), agent: strings.Repeat("a", 60), tag: "snap"},
		{name: "a tag with capitals and slashes", project: "pawly", agent: "agent-01", tag: "Snapshots/Nightly 01"},
		{name: "no tag", project: "pawly", agent: "agent-01", tag: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := HelperPodName(tc.project, tc.agent, tc.tag)
			if problems := validation.IsDNS1123Label(got); len(problems) > 0 {
				t.Errorf("HelperPodName(%q, %q, %q) = %q, which is no label: %s",
					tc.project, tc.agent, tc.tag, got, strings.Join(problems, "; "))
			}
			if len(got) > 63 {
				t.Errorf("HelperPodName(%q, %q, %q) is %d characters, and a pod's name is at most 63",
					tc.project, tc.agent, tc.tag, len(got))
			}
			if !strings.Contains(got, helperTag) {
				t.Errorf("%q says nothing about what the pod is for", got)
			}
			if again := HelperPodName(tc.project, tc.agent, tc.tag); again != got {
				t.Errorf("the same tag made %q and then %q", got, again)
			}
		})
	}

	// The agent's name is kept whole, and two operations of one agent differ.
	name := HelperPodName("pawly", "agent-01", "snap")
	if want := PodName("pawly", "agent-01") + helperTag; !strings.HasPrefix(name, want) {
		t.Errorf("HelperPodName(pawly, agent-01, snap) = %q, want it to start with %q", name, want)
	}
	if same := HelperPodName("pawly", "agent-01", "another-snap"); same == name {
		t.Errorf("two tags made the same pod name, %q: the second would block the first", same)
	}
	// A name that had to be cut still ends on the hash, not on a dash.
	long := HelperPodName(strings.Repeat("p", 60), strings.Repeat("a", 60), "snap")
	if strings.Contains(long, "--") {
		t.Errorf("%q has an empty piece in it", long)
	}
}

func TestArchiveClaimRefusesAClaimThatIsNotThere(t *testing.T) {
	client := fake.NewClientset()
	cluster := FromClient("k3d", "", client)
	spec := archiveSpec()
	var archive strings.Builder
	err := cluster.ArchiveClaim(context.Background(), spec, spec.Image, "snap", &archive)
	if err == nil {
		t.Fatal("ArchiveClaim() of a claim that is not there = nil, want an error")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ArchiveClaim() = %v, want it to wrap ErrNotFound", err)
	}
	for _, want := range []string{"ab-pawly-agent-01-workspace", "pawly/agent-01"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ArchiveClaim() = %v, want it to name %q", err, want)
		}
	}
	if archive.Len() != 0 {
		t.Errorf("it wrote %d bytes of an archive built from nothing", archive.Len())
	}
	pods, err := client.CoreV1().Pods("agentbox").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 0 {
		t.Errorf("it made %d pods for a claim that is not there: %+v", len(pods.Items), pods.Items)
	}
}

func TestRestoreClaimMakesTheClaimAndKeepsAFullOne(t *testing.T) {
	t.Run("a claim that is not there", func(t *testing.T) {
		client := fake.NewClientset()
		readyOnCreate(client)
		cluster := FromClient("k3d", "", client)
		spec := archiveSpec()
		// The exec cannot run against a fake clientset, so the operation ends
		// there: what is under test is the claim it makes first.
		err := cluster.RestoreClaim(context.Background(), spec, spec.Image, "restore", strings.NewReader(""))
		if err == nil || !strings.Contains(err.Error(), "REST config") {
			t.Fatalf("RestoreClaim() = %v, want it to reach the exec and say there is no REST config", err)
		}
		if !strings.Contains(err.Error(), "restoring pawly/agent-01's claim") {
			t.Errorf("RestoreClaim() = %v, want the operation and the agent in it", err)
		}
		claim, err := client.CoreV1().PersistentVolumeClaims("agentbox").Get(context.Background(), "ab-pawly-agent-01-workspace", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("the claim wasn't made: %v", err)
		}
		if got := claim.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != DefaultWorkspaceSize {
			t.Errorf("the claim holds %s, want %s", got.String(), DefaultWorkspaceSize)
		}
	})

	t.Run("a claim that is too small", func(t *testing.T) {
		client := fake.NewClientset()
		cluster := FromClient("k3d", "", client)
		spec := archiveSpec()
		small, err := BuildPVC(spec)
		if err != nil {
			t.Fatal(err)
		}
		small.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("1Gi")
		small.Namespace = "agentbox"
		if _, err := client.CoreV1().PersistentVolumeClaims("agentbox").Create(context.Background(), small, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		err = cluster.RestoreClaim(context.Background(), spec, spec.Image, "restore", strings.NewReader(""))
		if err == nil || !strings.Contains(err.Error(), "holds 1Gi") {
			t.Fatalf("RestoreClaim() over a full claim = %v, want it to say the claim is smaller", err)
		}
		pods, err := client.CoreV1().Pods("agentbox").List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(pods.Items) != 0 {
			t.Errorf("it ran %d helper pods into a claim it refused: %+v", len(pods.Items), pods.Items)
		}
	})
}

func TestClaimOperationsReachTheExecAndDeleteTheHelper(t *testing.T) {
	for _, tc := range []struct {
		name string
		tag  string
		want string
		op   func(*Cluster, Spec) error
	}{
		{
			name: "archive",
			tag:  "snap",
			want: "archiving pawly/agent-01's claim",
			op: func(c *Cluster, spec Spec) error {
				return c.ArchiveClaim(context.Background(), spec, spec.Image, "snap", io.Discard)
			},
		},
		{
			name: "restore",
			tag:  "restore",
			want: "restoring pawly/agent-01's claim",
			op: func(c *Cluster, spec Spec) error {
				return c.RestoreClaim(context.Background(), spec, spec.Image, "restore", strings.NewReader(""))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientset()
			readyOnCreate(client)
			cluster := FromClient("k3d", "", client)
			spec := archiveSpec()
			makeClaim(t, client, spec)

			err := tc.op(cluster, spec)
			// SPDY needs the API server's own address, which a clientset
			// alone doesn't carry: the operation gets as far as the command
			// and says so.
			if err == nil || !strings.Contains(err.Error(), "REST config") {
				t.Fatalf("%s with no REST config = %v, want it to reach the exec and say so", tc.name, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s = %v, want the operation and the agent in it", tc.name, err)
			}
			name := HelperPodName(spec.Project, spec.Agent, tc.tag)
			if _, err := client.CoreV1().Pods("agentbox").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Errorf("the helper pod %s is still there after %s: %v", name, tc.name, err)
			}
		})
	}
}

func TestArchiveClaimReplacesAHelperLeftBehind(t *testing.T) {
	client := fake.NewClientset()
	readyOnCreate(client)
	cluster := FromClient("k3d", "", client)
	spec := archiveSpec()
	makeClaim(t, client, spec)
	name := HelperPodName(spec.Project, spec.Agent, "snap")
	// What a daemon that crashed halfway leaves: the pod of an older attempt,
	// which is still holding the claim.
	stale := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agentbox"}}
	if _, err := client.CoreV1().Pods("agentbox").Create(context.Background(), stale, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	err := cluster.ArchiveClaim(context.Background(), spec, spec.Image, "snap", io.Discard)
	// It reached the exec rather than failing on the pod that was already
	// there, which is what says the old one was deleted and a new one made.
	if err == nil || !strings.Contains(err.Error(), "REST config") {
		t.Fatalf("ArchiveClaim() over a stale helper = %v, want it to reach the exec", err)
	}
}

func TestWaitForHelperReportsAFailedPod(t *testing.T) {
	name := HelperPodName("pawly", "agent-01", "snap")
	client := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agentbox"},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "agent",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					Reason:  "ErrImagePull",
					Message: "pull access denied for the image",
				}},
			}},
		},
	})
	cluster := FromClient("k3d", "", client)
	err := cluster.waitForHelper(context.Background(), name, time.Second, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "ErrImagePull: pull access denied for the image") {
		t.Errorf("waitForHelper() of a failed pod = %v, want the container's own reason", err)
	}
}

func TestWaitForHelperReportsAnImageThatCannotStart(t *testing.T) {
	name := HelperPodName("pawly", "agent-01", "snap")
	client := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agentbox"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "agent",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason:  "ImagePullBackOff",
					Message: "the registry refused the pull secret",
				}},
			}},
		},
	})
	cluster := FromClient("k3d", "", client)
	// Waiting out five minutes on a pull that will not happen is five minutes
	// an archive didn't need to spend.
	err := cluster.waitForHelper(context.Background(), name, time.Second, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "ImagePullBackOff: the registry refused the pull secret") {
		t.Errorf("waitForHelper() of a pod whose image can't be pulled = %v, want the reason it can't", err)
	}
}

func TestWaitForHelperUnblocksWhenThePodIsReady(t *testing.T) {
	name := HelperPodName("pawly", "agent-01", "snap")
	client := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agentbox"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	})
	// The first read is a pod with no node behind it yet, and the second is
	// the one a node reports a moment later.
	var reads int
	client.PrependReactor("get", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		reads++
		if reads < 2 {
			return false, nil, nil
		}
		return true, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agentbox"},
			Status:     readyStatus(),
		}, nil
	})
	cluster := FromClient("k3d", "", client)
	start := time.Now()
	if err := cluster.waitForHelper(context.Background(), name, time.Second, 5*time.Millisecond); err != nil {
		t.Fatalf("waitForHelper() of a pod that becomes ready = %v", err)
	}
	if reads < 2 {
		t.Errorf("it answered on %d reads, want it to read the pod again", reads)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("it waited %s, want it to answer as soon as the pod is ready", waited)
	}
}

func TestWaitForHelperStopsWhenTheOperationIsCancelled(t *testing.T) {
	name := HelperPodName("pawly", "agent-01", "snap")
	client := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agentbox"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	})
	cluster := FromClient("k3d", "", client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cluster.waitForHelper(ctx, name, time.Minute, 5*time.Millisecond)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("waitForHelper() on a cancelled operation = %v, want it to say it was cancelled", err)
	}
	if strings.Contains(err.Error(), "after 1m0s") {
		t.Errorf("waitForHelper() = %v, want the cancel named rather than the timeout", err)
	}
}
