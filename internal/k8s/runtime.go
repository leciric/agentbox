package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Pod is one agent's pod as the cluster has it.
type Pod struct {
	// Name, Project and Agent name it: the name comes from the pod, the two
	// others from its labels, which is what adoption reads.
	Name    string
	Project string
	Agent   string
	UID     types.UID
	// Phase is the cluster's: Pending, Running, Succeeded, Failed, Unknown.
	Phase corev1.PodPhase
	// Ready says every container in it passed its readiness check. It has no
	// readiness probe, so this means running, not serving: agentd opens the
	// connection out, and nothing comes in.
	Ready bool
	Node  string
	Image string
	// ImageID is the image the node actually ran, a name and a digest, which
	// is what pins a snapshot to the machine that was saved.
	ImageID string
	// Restarts is how many times its container has been restarted: a pod that
	// keeps restarting is a pod that keeps crashing.
	Restarts int32
	// Created is when the cluster made it. A pod being deleted keeps the
	// deletion time here instead, which is what says it is on its way out.
	Created  time.Time
	Deleting bool
	// Reason and Message are the cluster's own words for what is wrong, when
	// something is: ImagePullBackOff, CrashLoopBackOff, a node with no room.
	Reason  string
	Message string
}

// State is what the pod is doing, in the words the agent's row keeps: pending,
// running, stopped (no pod at all), failed or deleting.
func (p Pod) State() string {
	switch {
	case p.Deleting:
		return "deleting"
	case p.Phase == corev1.PodRunning && p.Ready:
		return "running"
	case p.Phase == corev1.PodFailed:
		return "failed"
	case p.Phase == corev1.PodSucceeded:
		return "stopped"
	default:
		return "pending"
	}
}

// Failed reports whether the pod is one the cluster has given up on: a node that
// evicted it, or a container that can't be started at all. Such a pod is deleted
// and made again, which is what a new machine means.
func (p Pod) Failed() bool { return p.Phase == corev1.PodFailed }

// Detail is why the pod isn't running, in the cluster's words, or "" when
// nothing is wrong.
func (p Pod) Detail() string {
	if p.State() == "running" {
		return ""
	}
	switch {
	case p.Reason != "" && p.Message != "":
		return p.Reason + ": " + p.Message
	case p.Reason != "":
		return p.Reason
	case p.Phase == corev1.PodPending:
		return "the cluster hasn't scheduled it yet"
	}
	return p.Message
}

// Ref is the agent the pod is for, from its labels.
func (p Pod) Ref() string {
	if p.Project == "" || p.Agent == "" {
		return ""
	}
	return p.Project + "/" + p.Agent
}

// Create makes the three objects an agent's pod needs, in the order that leaves
// nothing behind if a later one fails: the claim its work lives on, the Secret
// its session token is in, and the pod.
//
// It is what starting an agent on a cluster means. A pod that already exists is
// returned as it is rather than replaced: it may be the one the agent is already
// working in.
func (c *Cluster) Create(ctx context.Context, spec Spec, token string) (*Pod, error) {
	claim, err := BuildPVC(spec)
	if err != nil {
		return nil, err
	}
	if err := c.ensureClaim(ctx, claim); err != nil {
		return nil, err
	}
	secret, err := BuildTokenSecret(spec, token)
	if err != nil {
		return nil, err
	}
	if err := c.applySecret(ctx, secret); err != nil {
		return nil, err
	}
	pod, err := BuildPod(spec)
	if err != nil {
		return nil, err
	}
	pod.Namespace = c.namespace
	made, err := c.client.CoreV1().Pods(c.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return c.Get(ctx, spec.Project, spec.Agent)
	}
	if err != nil {
		return nil, fmt.Errorf("creating the pod %s: %w", pod.Name, err)
	}
	out := podFrom(made)
	return &out, nil
}

// ensureClaim creates the claim an agent's work lives on. A claim that exists is
// kept as the cluster has it; one smaller than the agent asks for is refused
// rather than quietly left as it is, because a full disk mid-build is worse than
// an error here.
func (c *Cluster) ensureClaim(ctx context.Context, claim *corev1.PersistentVolumeClaim) error {
	claim.Namespace = c.namespace
	claims := c.client.CoreV1().PersistentVolumeClaims(c.namespace)
	live, err := claims.Get(ctx, claim.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := claims.Create(ctx, claim, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating the claim %s: %w", claim.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the claim %s: %w", claim.Name, err)
	}
	want := claim.Spec.Resources.Requests[corev1.ResourceStorage]
	have := live.Spec.Resources.Requests[corev1.ResourceStorage]
	if have.Cmp(want) < 0 {
		return fmt.Errorf("the claim %s holds %s and the agent asks for %s: a claim can only be grown by its storage class, so either grow it or destroy the agent and start it again", claim.Name, have.String(), want.String())
	}
	return nil
}

// applySecret writes a secret that already exists in place of the old one, which
// is how a new session token replaces the previous one.
func (c *Cluster) applySecret(ctx context.Context, secret *corev1.Secret) error {
	secret.Namespace = c.namespace
	secrets := c.client.CoreV1().Secrets(c.namespace)
	live, err := secrets.Get(ctx, secret.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating the secret %s: %w", secret.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the secret %s: %w", secret.Name, err)
	}
	secret.ResourceVersion = live.ResourceVersion
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating the secret %s: %w", secret.Name, err)
	}
	return nil
}

// Get is the pod of one agent, ErrNotFound when it has none.
func (c *Cluster) Get(ctx context.Context, project, agent string) (*Pod, error) {
	name := PodName(project, agent)
	pod, err := c.client.CoreV1().Pods(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("%s/%s has no pod in cluster %s: %w", project, agent, c.name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading the pod %s: %w", name, err)
	}
	out := podFrom(pod)
	return &out, nil
}

// List is every pod AgentBox made in the namespace, which is what adoption reads:
// each one carries the project and the agent it belongs to in its labels.
func (c *Cluster) List(ctx context.Context) ([]Pod, error) {
	selector := ManagedSelector()
	pods, err := c.client.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("listing the pods of cluster %s: %w", c.name, err)
	}
	out := make([]Pod, 0, len(pods.Items))
	for i := range pods.Items {
		out = append(out, podFrom(&pods.Items[i]))
	}
	return out, nil
}

// Delete stops an agent: its pod goes, and its claim stays, so its work is there
// when it starts again. Deleting a pod that is already gone is not an error: the
// state being asked for is the state it is in.
func (c *Cluster) Delete(ctx context.Context, project, agent string) error {
	name := PodName(project, agent)
	err := c.client.CoreV1().Pods(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting the pod %s: %w", name, err)
	}
	return nil
}

// DeleteAll forgets an agent entirely: its pod, its token and the claim its work
// was on. The claim is last, so a failure before it leaves a stopped agent with
// its work rather than a claim nobody can name. claim is the one the agent's row
// names, "" for the name its identity derives: a restore moves an agent onto a
// new claim, and the old one is only safe to delete once the row names the new.
func (c *Cluster) DeleteAll(ctx context.Context, project, agent, claim string) error {
	if claim == "" {
		claim = ClaimName(project, agent)
	}
	if err := c.Delete(ctx, project, agent); err != nil {
		return err
	}
	err := c.client.CoreV1().Secrets(c.namespace).Delete(ctx, PodTokenSecretName(project, agent), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting the token of %s/%s: %w", project, agent, err)
	}
	err = c.client.CoreV1().PersistentVolumeClaims(c.namespace).Delete(ctx, claim, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting the claim of %s/%s: %w", project, agent, err)
	}
	return nil
}

// DeleteClaim deletes one claim by name, which is how a restore lets go of the
// volume an agent lived on before it moved to a new one. A claim that is already
// gone is not an error.
func (c *Cluster) DeleteClaim(ctx context.Context, name string) error {
	err := c.client.CoreV1().PersistentVolumeClaims(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting the claim %s: %w", name, err)
	}
	return nil
}

// Capacity is what the cluster has, and what the agents in this namespace already
// ask for. It counts every pod in the namespace, not only AgentBox's: they share
// the nodes with whatever else runs there.
type Capacity struct {
	Nodes int
	// Allocatable is what the ready nodes offer together.
	AllocatableCPU    resource.Quantity
	AllocatableMemory resource.Quantity
	// Requested is what the pods in the namespace ask for.
	RequestedCPU    resource.Quantity
	RequestedMemory resource.Quantity
}

// CPU and Memory are what is left, floored at zero.
func (cap Capacity) CPU() resource.Quantity {
	return left(cap.AllocatableCPU, cap.RequestedCPU)
}

func (cap Capacity) Memory() resource.Quantity {
	return left(cap.AllocatableMemory, cap.RequestedMemory)
}

func left(total, used resource.Quantity) resource.Quantity {
	if total.Cmp(used) <= 0 {
		return resource.Quantity{}
	}
	out := total.DeepCopy()
	out.Sub(used)
	return out
}

// Capacity reads the nodes and the namespace's pods. The nodes are a cluster-wide
// read, which the read-only ClusterRole `cluster add` applies grants.
func (c *Cluster) Capacity(ctx context.Context) (Capacity, error) {
	nodes, err := c.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Capacity{}, fmt.Errorf("listing the nodes of cluster %s: %w", c.name, err)
	}
	var cap Capacity
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if !nodeReady(node) {
			continue
		}
		cap.Nodes++
		add(&cap.AllocatableCPU, node.Status.Allocatable[corev1.ResourceCPU])
		add(&cap.AllocatableMemory, node.Status.Allocatable[corev1.ResourceMemory])
	}
	pods, err := c.client.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return Capacity{}, fmt.Errorf("listing the pods of cluster %s: %w", c.name, err)
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		for _, container := range pod.Spec.Containers {
			add(&cap.RequestedCPU, container.Resources.Requests[corev1.ResourceCPU])
			add(&cap.RequestedMemory, container.Resources.Requests[corev1.ResourceMemory])
		}
	}
	return cap, nil
}

// add sums a quantity into total, ignoring a container that asks for nothing.
func add(total *resource.Quantity, q resource.Quantity) {
	if q.IsZero() {
		return
	}
	total.Add(q)
}

// Logs is the pod's output, from its container: what it printed before it
// connected, and whatever the AI tool writes to the terminal it runs in. It is
// the fallback for when there is no session: the gateway's terminal is the
// normal way to see an agent.
func (c *Cluster) Logs(ctx context.Context, project, agent string, tailLines int64) (io.ReadCloser, error) {
	options := &corev1.PodLogOptions{Container: "agent"}
	if tailLines > 0 {
		options.TailLines = &tailLines
	}
	request := c.client.CoreV1().Pods(c.namespace).GetLogs(PodName(project, agent), options)
	stream, err := request.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the log of %s/%s: %w", project, agent, err)
	}
	return stream, nil
}

// podFrom is a pod as the cluster has it, in this package's words.
func podFrom(pod *corev1.Pod) Pod {
	out := Pod{
		Name:     pod.Name,
		Project:  pod.Labels[LabelProject],
		Agent:    pod.Labels[LabelAgent],
		UID:      pod.UID,
		Phase:    pod.Status.Phase,
		Node:     pod.Spec.NodeName,
		Created:  pod.CreationTimestamp.Time,
		Deleting: pod.DeletionTimestamp != nil,
	}
	if len(pod.Spec.Containers) > 0 {
		out.Image = pod.Spec.Containers[0].Image
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			out.Ready = true
			break
		}
	}
	if pod.Status.Reason != "" {
		out.Reason, out.Message = pod.Status.Reason, pod.Status.Message
	}
	// A container waiting on something says why where the pod's own status
	// doesn't: an image that can't be pulled, a crash loop.
	for _, status := range pod.Status.ContainerStatuses {
		out.Restarts += status.RestartCount
		if out.ImageID == "" {
			out.ImageID = status.ImageID
		}
		if status.State.Waiting != nil {
			if out.Reason == "" {
				out.Reason, out.Message = status.State.Waiting.Reason, status.State.Waiting.Message
			}
		} else if status.State.Terminated != nil && status.State.Terminated.Reason != "Completed" {
			if out.Reason == "" {
				out.Reason, out.Message = status.State.Terminated.Reason, status.State.Terminated.Message
			}
		}
	}
	return out
}

// ManagedSelector is what every object AgentBox makes for an agent carries, and
// what adoption and the reconciler select on.
func ManagedSelector() string { return LabelManaged + "=true" }

// ErrNotFound is the pod, or the claim, that isn't there.
var ErrNotFound = errors.New("not found")
