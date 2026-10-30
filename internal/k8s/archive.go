package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// An agent's claim is archived and restored by a pod of its own, because a claim
// is ReadWriteOnce: the caller stops the agent before one of these runs, so this
// pod is the one that mounts it. The helper runs nothing but tar, over the API
// server's exec, and it is deleted as soon as the stream is done.
const (
	// helperTag is in a helper pod's name, after the agent's, so that it reads
	// as what it is in `kubectl get pods`.
	helperTag = "-archive-"
	// helperMount is where the helper mounts the claim: its root, so that an
	// archive carries both directories the agent lives in.
	helperMount = "/mnt"
	// helperVolume and helperContainer name the volume and the container in the
	// helper pod. The exec path asks for a container by name, and that name is
	// "agent".
	helperVolume    = "claim"
	helperContainer = "agent"
	// helperTimeout is how long a helper pod has to start, and helperPoll is
	// how often it is read again while it does.
	helperTimeout = 5 * time.Minute
	helperPoll    = 2 * time.Second
	// helperDeadline is how long a helper pod may exist. It runs `sleep
	// infinity`, so a daemon that crashes mid-snapshot would otherwise leave it
	// holding a ReadWriteOnce claim for good, keeping the agent's pod from
	// mounting it on another node. The cluster stops it after this, and the
	// reconciler sweeps one that is older than this even if the cluster's own
	// deadline didn't fire.
	helperDeadline = time.Hour
	// LabelHelper marks a helper pod, and HelperArchive is its value: the
	// reconciler sweeps the pods that carry it.
	LabelHelper   = "agentbox/helper"
	HelperArchive = "archive"
	// stderrLimit is how much of tar's own words an error keeps: enough to say
	// what it failed on, not a listing of every file it could not write.
	stderrLimit = 8 << 10
)

// HelperPodName is the deterministic name of the pod that archives an agent's
// claim. tag distinguishes two operations (a snapshot name, a new claim name).
// The result is a valid pod name of at most 63 characters.
func HelperPodName(project, agent, tag string) string {
	sum := sha256.Sum256([]byte(tag))
	suffix := helperTag + hex.EncodeToString(sum[:4])
	base := strings.ToLower(PodName(project, agent))
	// The hash has to fit after the agent's name, so a name with no room for it
	// is cut where a label stops. The agent's own pod keeps the whole name.
	if room := 63 - len(suffix); len(base) > room {
		base = strings.TrimRight(base[:room], "-")
	}
	return base + suffix
}

// BuildHelperPod mounts spec.WorkspaceClaim() at /mnt, read-write, on the given
// image, and does nothing else: the exec that follows is what runs tar. It is
// not selected by ManagedSelector(), so the reconciler never adopts it, and it
// carries no project or agent label, so nothing reads it as an agent.
func BuildHelperPod(spec Spec, name, image string) (*corev1.Pod, error) {
	if err := checkAgent(spec); err != nil {
		return nil, err
	}
	switch {
	case name == "":
		return nil, fmt.Errorf("the helper pod of %s/%s has no name", spec.Project, spec.Agent)
	case image == "":
		return nil, fmt.Errorf("the helper pod of %s/%s has no image: set one on its cluster (agentbox cluster set --image)", spec.Project, spec.Agent)
	}
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			// The two labels and nothing else: a helper is not an agent, and
			// the reconciler must not adopt it. The helper label is what the
			// reconciler's own sweep selects.
			Labels: map[string]string{
				LabelHelper: HelperArchive,
				LabelApp:    AppArchive,
			},
		},
		Spec: corev1.PodSpec{
			// It runs one command and is deleted: nothing restarts it, and
			// nothing waits to shut it down.
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: ptr(int64(0)),
			EnableServiceLinks:            ptr(false),
			// The namespace's default ServiceAccount token is no use to a
			// helper, and nothing should be able to read one out of it.
			AutomountServiceAccountToken: ptr(false),
			// A helper a crashed daemon left behind is stopped rather than
			// kept: it holds the claim it mounted until it goes.
			ActiveDeadlineSeconds: ptr(int64(helperDeadline.Seconds())),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:        ptr(true),
				RunAsUser:           ptr(int64(AgentUser)),
				RunAsGroup:          ptr(int64(AgentUser)),
				FSGroup:             ptr(int64(AgentUser)),
				FSGroupChangePolicy: ptr(corev1.FSGroupChangeOnRootMismatch),
				SeccompProfile:      &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			ImagePullSecrets: pullSecrets(spec.PullSecret),
			Containers: []corev1.Container{{
				Name:            helperContainer,
				Image:           image,
				ImagePullPolicy: pullPolicy(spec.FromRegistry),
				// It has to be running when the exec arrives, and it has
				// nothing to do until then.
				Command: []string{"sleep", "infinity"},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				VolumeMounts: []corev1.VolumeMount{{Name: helperVolume, MountPath: helperMount}},
			}},
			Volumes: []corev1.Volume{{
				Name: helperVolume,
				VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: spec.WorkspaceClaim(),
				}},
			}},
		},
	}, nil
}

// SweepHelpers deletes helper pods a daemon left behind. A helper is created
// for one operation and deleted when it ends, so one that outlived its deadline,
// or that the cluster has already stopped, is holding its ReadWriteOnce claim
// for nothing: the agent's next pod can't mount it until this one goes. A helper
// that is still running and younger than the deadline may be one an operation is
// using, so it stays.
func (c *Cluster) SweepHelpers(ctx context.Context) error {
	pods := c.client.CoreV1().Pods(c.namespace)
	live, err := pods.List(ctx, metav1.ListOptions{LabelSelector: LabelHelper + "=" + HelperArchive})
	if err != nil {
		return fmt.Errorf("listing the helper pods of cluster %s: %w", c.name, err)
	}
	now := time.Now()
	var problems []string
	for i := range live.Items {
		pod := &live.Items[i]
		if !staleHelper(pod, now) {
			continue
		}
		uid := pod.UID
		err := pods.Delete(ctx, pod.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
		if err != nil && !apierrors.IsNotFound(err) {
			problems = append(problems, fmt.Sprintf("deleting the helper pod %s: %v", pod.Name, err))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// staleHelper is whether a helper pod has outlived what a helper is for: the
// cluster stopped it, or it has been there longer than helperDeadline. A pod
// already being deleted is left to finish.
func staleHelper(pod *corev1.Pod, now time.Time) bool {
	switch pod.Status.Phase {
	case corev1.PodFailed, corev1.PodSucceeded:
		return true
	}
	if pod.DeletionTimestamp != nil {
		return false
	}
	return now.Sub(pod.CreationTimestamp.Time) >= helperDeadline
}

// ArchiveClaim streams the claim's contents as a gzipped tar to w: a pod runs
// `tar -C /mnt -czf - .` and the stream is that process's stdout.
//
// A claim that is not there is an error and not an empty archive: the caller
// stops the agent before this runs, and an archive that quietly holds nothing
// is the one way this can lose the work it was meant to keep.
func (c *Cluster) ArchiveClaim(ctx context.Context, spec Spec, image, tag string, w io.Writer) error {
	if err := checkAgent(spec); err != nil {
		return err
	}
	claim := spec.WorkspaceClaim()
	_, err := c.client.CoreV1().PersistentVolumeClaims(c.namespace).Get(ctx, claim, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("archiving %s/%s's claim %s: %w: the claim is not there, and archiving nothing would lose what it held", spec.Project, spec.Agent, claim, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("archiving %s/%s's claim %s: reading it: %w", spec.Project, spec.Agent, claim, err)
	}
	return c.runHelper(ctx, spec, image, tag, helperOp{
		what:    fmt.Sprintf("archiving %s/%s's claim %s", spec.Project, spec.Agent, claim),
		command: []string{"tar", "-C", helperMount, "-czf", "-", "."},
		stdout:  w,
	})
}

// RestoreClaim creates spec's claim (if it is not there) and unpacks a gzipped
// tar from r into it: a pod runs `tar -C /mnt --strip-components=1 -xzf -`.
//
// A claim that already exists is kept as the cluster has it, and one smaller
// than the spec asks for is refused before anything is unpacked into it.
func (c *Cluster) RestoreClaim(ctx context.Context, spec Spec, image, tag string, r io.Reader) error {
	if err := checkAgent(spec); err != nil {
		return err
	}
	claim, err := BuildPVC(spec)
	if err != nil {
		return fmt.Errorf("restoring %s/%s's claim %s from its archive: %w", spec.Project, spec.Agent, spec.WorkspaceClaim(), err)
	}
	what := fmt.Sprintf("restoring %s/%s's claim %s from its archive", spec.Project, spec.Agent, claim.Name)
	if err := c.ensureClaim(ctx, claim); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return c.runHelper(ctx, spec, image, tag, helperOp{
		what: what,
		// An archive starts at the claim's root, so every member is "./"
		// something. The root itself belongs to root with this pod's group,
		// and tar tries to give it its recorded mode back, which it can't:
		// that is a permission error and exit status 2, over a restore whose
		// files all landed. Stripping one component drops that entry and
		// keeps every path below it, including the two directories a
		// brand-new claim doesn't have yet.
		command: []string{"tar", "-C", helperMount, "--strip-components=1", "-xzf", "-"},
		stdin:   r,
	})
}

// helperOp is the one command a helper pod runs, and where its two streams go.
type helperOp struct {
	// what names the operation in an error: "archiving pawly/agent-01's claim".
	what    string
	command []string
	stdin   io.Reader
	stdout  io.Writer
}

// runHelper makes the helper pod, waits for it to run, runs op in it, and
// deletes it, leaving nothing behind even when the operation failed or was
// cancelled.
func (c *Cluster) runHelper(ctx context.Context, spec Spec, image, tag string, op helperOp) error {
	name := HelperPodName(spec.Project, spec.Agent, tag)
	pod, err := BuildHelperPod(spec, name, image)
	if err != nil {
		return fmt.Errorf("%s: %w", op.what, err)
	}
	pod.Namespace = c.namespace
	pods := c.client.CoreV1().Pods(c.namespace)
	// A helper left by a daemon that crashed would block this one twice: by
	// name, and by holding the claim it has not let go of yet.
	if err := pods.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("%s: deleting the helper pod %s: %w", op.what, name, err)
	}
	if err := c.waitHelperGone(ctx, name, helperTimeout, helperPoll); err != nil {
		return fmt.Errorf("%s: %w", op.what, err)
	}
	if _, err := pods.Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("%s: creating the helper pod %s: %w", op.what, name, err)
	}
	defer func() {
		// The claim is ReadWriteOnce, so a helper that outlives the operation
		// keeps the agent's own pod from mounting it. It is deleted even when
		// ctx is done, which is when a helper is most likely to be left over.
		_ = pods.Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{})
	}()
	if err := c.waitForHelper(ctx, name, helperTimeout, helperPoll); err != nil {
		return fmt.Errorf("%s: %w", op.what, err)
	}
	if err := c.execTar(ctx, name, op); err != nil {
		return fmt.Errorf("%s: %w", op.what, err)
	}
	return nil
}

// execTar runs the operation's command in the helper pod, with tar's own words
// in the error when it fails.
func (c *Cluster) execTar(ctx context.Context, pod string, op helperOp) error {
	stderr := &boundedStderr{}
	err := c.ExecPod(ctx, pod, ExecOptions{
		Command: op.command,
		Stdin:   op.stdin,
		Stdout:  op.stdout,
		Stderr:  stderr,
	})
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return fmt.Errorf("running %s in %s: %w: %s", strings.Join(op.command, " "), pod, err, msg)
	}
	return fmt.Errorf("running %s in %s: %w", strings.Join(op.command, " "), pod, err)
}

// waitForHelper polls the helper pod until a command can run in it, or the
// cluster has failed it. It polls rather than watches: a watch is a connection
// kept open for a pod that lives for seconds, and its first answer is usually
// the one that matters.
func (c *Cluster) waitForHelper(ctx context.Context, name string, timeout, poll time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pods := c.client.CoreV1().Pods(c.namespace)
	for {
		pod, err := pods.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading the helper pod %s: %w", name, err)
		}
		switch pod.Status.Phase {
		case corev1.PodFailed:
			return fmt.Errorf("the helper pod %s failed: %s", name, failedReason(pod))
		case corev1.PodSucceeded:
			// It runs `sleep infinity`: one that exited ran nothing, and
			// waiting out the timeout would only hide that.
			return fmt.Errorf("the helper pod %s exited before it ran anything", name)
		}
		// An image that can't be pulled keeps a pod Pending for minutes at a
		// time, and the reason is already there to be read.
		if reason := waitingReason(pod); reason != "" {
			return fmt.Errorf("the helper pod %s can't start: %s", name, reason)
		}
		if podReady(pod) {
			return nil
		}
		select {
		case <-ctx.Done():
			return stillWaiting(ctx, fmt.Sprintf("the helper pod %s was still %s", name, lower(string(pod.Status.Phase), "pending")), timeout)
		case <-time.After(poll):
		}
	}
}

// waitHelperGone waits for the helper pod of that name to be gone. Deleting a pod
// is not immediate, and one on its way out may still hold the claim.
func (c *Cluster) waitHelperGone(ctx context.Context, name string, timeout, poll time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pods := c.client.CoreV1().Pods(c.namespace)
	for {
		_, err := pods.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the helper pod %s: %w", name, err)
		}
		select {
		case <-ctx.Done():
			return stillWaiting(ctx, "the helper pod "+name+" is still there", timeout)
		case <-time.After(poll):
		}
	}
}

// stillWaiting is the error of a poll that ran out: the timeout says how long it
// waited, and a cancelled operation says it was cancelled rather than late.
func stillWaiting(ctx context.Context, what string, timeout time.Duration) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s after %s: %w", what, timeout, ctx.Err())
	}
	return fmt.Errorf("%s: %w", what, ctx.Err())
}

// podReady reports whether every container in the pod has passed its readiness
// check, which for a pod with no probe means it is running.
func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// failedReason is why a helper pod the cluster gave up on can't run, in the
// cluster's words: the image it could not pull, the container that exited at
// once.
func failedReason(pod *corev1.Pod) string {
	for _, status := range pod.Status.ContainerStatuses {
		if terminated := status.State.Terminated; terminated != nil {
			if reason := joinReason(terminated.Reason, terminated.Message); reason != "" {
				return reason
			}
		}
	}
	if reason := joinReason(pod.Status.Reason, pod.Status.Message); reason != "" {
		return reason
	}
	return "the cluster gave up on it"
}

// joinReason is a reason and its message as one sentence, without a colon that
// has nothing after it.
func joinReason(reason, message string) string {
	switch {
	case reason != "" && message != "":
		return reason + ": " + message
	default:
		return reason + message
	}
}

// checkAgent refuses a spec that names no agent: a claim, an archive and a
// helper pod are all one agent's.
func checkAgent(spec Spec) error {
	if spec.Project == "" || spec.Agent == "" {
		return fmt.Errorf("an agent's claim needs a project and an agent, not %q/%q", spec.Project, spec.Agent)
	}
	return nil
}

// boundedStderr keeps the first stderrLimit bytes of a command's stderr and
// drops the rest: a failure still has to fit in an error.
type boundedStderr struct{ buf strings.Builder }

func (b *boundedStderr) Write(p []byte) (int, error) {
	n := len(p)
	if room := stderrLimit - b.buf.Len(); room > 0 {
		if n > room {
			p = p[:room]
		}
		_, _ = b.buf.Write(p)
	}
	return n, nil
}

func (b *boundedStderr) String() string { return b.buf.String() }
