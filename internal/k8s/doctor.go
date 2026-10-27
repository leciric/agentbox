package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Check is one thing `agentbox cluster doctor` looked at.
type Check struct {
	// Name is what it looked at, short enough for a table: api, namespace,
	// nodes, storage class, workspace claim, registry, image.
	Name string
	OK   bool
	// Skipped is a check this cluster's own configuration makes impossible to
	// run: no image to pull, no registry to pull from. It is not a failure.
	Skipped bool
	// Detail says what it found: a count, the error the API server gave, or why
	// the canary never started.
	Detail string
}

// Report is what the doctor found, in the order it looked, and what it talked to.
type Report struct {
	Server string
	Checks []Check
}

// Failed reports whether anything it found is wrong.
func (r Report) Failed() bool {
	for _, check := range r.Checks {
		if !check.OK && !check.Skipped {
			return true
		}
	}
	return false
}

// Doctor checks one cluster with the access the daemon will really have: not an
// admin's, but the namespace's ServiceAccount. Every check runs even after one
// fails, because a cluster with a bad storage class and an unreachable registry
// should say both.
//
// It creates and deletes two canaries, a claim and (when there is an image) a
// pod. Nothing else in the cluster is touched.
type Doctor struct {
	// Cluster is the sealed access a `cluster add` left behind.
	Cluster *Cluster
	// StorageClass, Image and PullSecret are what the row says this cluster
	// uses: the class an agent's workspace asks for, the image a pod starts
	// from, and the name of the secret its registry's login was written to.
	StorageClass string
	Image        string
	PullSecret   string
	// Timeout bounds each check that waits for the cluster. 30 seconds when it
	// is zero.
	Timeout time.Duration
	// Suffix makes the canaries' names unique, so two doctors don't collide.
	// The time it started is the default, and a test sets its own.
	Suffix string
}

// canary names a doctor-owned object.
func (d Doctor) canary(kind string) string {
	suffix := d.Suffix
	if suffix == "" {
		suffix = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return "agentbox-doctor-" + kind + "-" + suffix
}

func (d Doctor) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return 30 * time.Second
}

// Run is every check.
func (d Doctor) Run(ctx context.Context) Report {
	client := d.Cluster.Client()
	report := Report{}
	if restConfig, err := d.Cluster.RESTConfig(); err == nil {
		report.Server = restConfig.Host
	}
	// The API first, because everything else fails the same way and says less.
	version, err := client.Discovery().ServerVersion()
	if err != nil {
		report.Checks = append(report.Checks, Check{Name: "api", Detail: err.Error()})
		return report
	}
	report.Checks = append(report.Checks, Check{Name: "api", OK: true, Detail: version.GitVersion})
	report.Checks = append(report.Checks, d.namespace(ctx), d.nodes(ctx), d.storageClass(ctx),
		d.workspaceClaim(ctx), d.pullSecret(ctx), d.image(ctx))
	return report
}

// namespace proves the access works at all: the token is accepted and the Role
// lets it read its own namespace's pods.
func (d Doctor) namespace(ctx context.Context) Check {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	_, err := d.Cluster.Client().CoreV1().Pods(d.Cluster.Namespace()).List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return Check{Name: "namespace", Detail: fmt.Sprintf("reading pods in %s: %v", d.Cluster.Namespace(), err)}
	}
	return Check{Name: "namespace", OK: true, Detail: d.Cluster.Namespace()}
}

// nodes is how much room there is, and whether anything can be placed at all.
func (d Doctor) nodes(ctx context.Context) Check {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	nodes, err := d.Cluster.Client().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Check{Name: "nodes", Detail: err.Error()}
	}
	if len(nodes.Items) == 0 {
		return Check{Name: "nodes", Detail: "the cluster has no nodes"}
	}
	var ready, cpus, memory int64
	var notReady []string
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if nodeReady(node) {
			ready++
			cpus += node.Status.Allocatable.Cpu().MilliValue()
			memory += node.Status.Allocatable.Memory().Value()
		} else {
			notReady = append(notReady, node.Name)
		}
	}
	detail := fmt.Sprintf("%d of %d ready, %s CPU and %s memory to place agents on",
		ready, len(nodes.Items), cpuText(cpus), bytesText(memory))
	if len(notReady) > 0 {
		detail += "; not ready: " + strings.Join(notReady, ", ")
	}
	return Check{Name: "nodes", OK: ready == int64(len(nodes.Items)), Detail: detail}
}

// storageClass is whether the class an agent's workspace asks for exists, and
// which class that is when the config names none.
func (d Doctor) storageClass(ctx context.Context) Check {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	if d.StorageClass == "" {
		name := d.defaultStorageClass(ctx)
		if name == "" {
			return Check{Name: "storage class", Skipped: true, Detail: "the cluster has no default class, and the config names none"}
		}
		return Check{Name: "storage class", OK: true, Detail: name + " (the cluster's default)"}
	}
	if _, err := d.Cluster.Client().StorageV1().StorageClasses().Get(ctx, d.StorageClass, metav1.GetOptions{}); err != nil {
		return Check{Name: "storage class", Detail: fmt.Sprintf("%s: %v", d.StorageClass, err)}
	}
	return Check{Name: "storage class", OK: true, Detail: d.StorageClass}
}

// defaultStorageClass is the class the cluster marks as its default, "" when it
// marks none. It is what a claim which names no class is made from.
func (d Doctor) defaultStorageClass(ctx context.Context) string {
	classes, err := d.Cluster.Client().StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ""
	}
	for _, class := range classes.Items {
		if class.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" ||
			class.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true" {
			return class.Name
		}
	}
	return ""
}

// bindingMode is when a class binds a claim: at once, or when a pod first uses
// it, which is what k3s's local-path and every local volume do.
func (d Doctor) bindingMode(ctx context.Context, name string) storagev1.VolumeBindingMode {
	if name == "" {
		return storagev1.VolumeBindingImmediate
	}
	class, err := d.Cluster.Client().StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
	if err != nil || class.VolumeBindingMode == nil {
		return storagev1.VolumeBindingImmediate
	}
	return *class.VolumeBindingMode
}

// workspaceClaim creates the claim an agent's workspace would, waits for it to
// bind, and deletes it. It is the one check that proves the class has a
// provisioner behind it and that the ServiceAccount may make claims.
func (d Doctor) workspaceClaim(ctx context.Context) Check {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	claims := d.Cluster.Client().CoreV1().PersistentVolumeClaims(d.Cluster.Namespace())
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: d.canary("claim")},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
			StorageClassName: storageClass(d.StorageClass),
		},
	}
	created, err := claims.Create(ctx, claim, metav1.CreateOptions{})
	if err != nil {
		return Check{Name: "workspace claim", Detail: fmt.Sprintf("creating one: %v", err)}
	}
	defer func() {
		// The canary leaves nothing behind, even when it failed.
		_ = claims.Delete(context.WithoutCancel(ctx), created.Name, metav1.DeleteOptions{})
	}()
	// A class that binds on first use keeps a claim Pending for as long as no
	// pod mounts it, which is k3s's local-path and every local volume: waiting
	// for it to bind on its own would fail a cluster that is fine.
	class := d.StorageClass
	if class == "" {
		class = d.defaultStorageClass(ctx)
	}
	bindsOnUse := d.bindingMode(ctx, class) == storagev1.VolumeBindingWaitForFirstConsumer
	for {
		live, err := claims.Get(ctx, created.Name, metav1.GetOptions{})
		if err != nil {
			return Check{Name: "workspace claim", Detail: fmt.Sprintf("reading it back: %v", err)}
		}
		switch live.Status.Phase {
		case corev1.ClaimBound:
			return Check{Name: "workspace claim", OK: true, Detail: "1Gi bound"}
		case corev1.ClaimLost:
			return Check{Name: "workspace claim", Detail: "the claim was lost: its class provisioned a volume and then lost it"}
		}
		if bindsOnUse {
			return Check{Name: "workspace claim", OK: true, Detail: "1Gi accepted; " + class + " binds it when a pod mounts it"}
		}
		select {
		case <-ctx.Done():
			return Check{Name: "workspace claim", Detail: fmt.Sprintf("still %s after %s: check that the class has a provisioner",
				lower(string(live.Status.Phase), "pending"), d.timeout())}
		case <-time.After(tokenPoll):
		}
	}
}

// pullSecret is whether the registry's login reached the namespace.
func (d Doctor) pullSecret(ctx context.Context) Check {
	if d.PullSecret == "" {
		return Check{Name: "registry", Skipped: true, Detail: "no registry: the image is imported, not pulled"}
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	secret, err := d.Cluster.Client().CoreV1().Secrets(d.Cluster.Namespace()).Get(ctx, d.PullSecret, metav1.GetOptions{})
	if err != nil {
		return Check{Name: "registry", Detail: fmt.Sprintf("%s: %v", d.PullSecret, err)}
	}
	if secret.Type != corev1.SecretTypeDockerConfigJson || len(secret.Data[corev1.DockerConfigJsonKey]) == 0 {
		return Check{Name: "registry", Detail: fmt.Sprintf("%s is not a docker config: run agentbox cluster add again", d.PullSecret)}
	}
	return Check{Name: "registry", OK: true, Detail: d.PullSecret}
}

// image starts the pod an agent would, waits for it to run, and deletes it. It
// is the check that catches a private image with no pull secret, or one from a
// registry the nodes can't reach.
func (d Doctor) image(ctx context.Context) Check {
	if d.Image == "" {
		return Check{Name: "image", Skipped: true, Detail: "no image is configured: set one with agentbox cluster add"}
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	pods := d.Cluster.Client().CoreV1().Pods(d.Cluster.Namespace())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: d.canary("image")},
		Spec: corev1.PodSpec{
			// The agent image's entrypoint is the agent itself; a canary only
			// has to stay up long enough to prove it was pulled.
			Containers: []corev1.Container{{
				Name:    "canary",
				Image:   d.Image,
				Command: []string{"sleep", "60"},
			}},
			RestartPolicy:    corev1.RestartPolicyNever,
			ImagePullSecrets: pullSecrets(d.PullSecret),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr(true),
				RunAsUser:      ptr(int64(1000)),
				RunAsGroup:     ptr(int64(1000)),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
		},
	}
	created, err := pods.Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return Check{Name: "image", Detail: fmt.Sprintf("creating a pod: %v", err)}
	}
	defer func() {
		_ = pods.Delete(context.WithoutCancel(ctx), created.Name, metav1.DeleteOptions{})
	}()
	for {
		live, err := pods.Get(ctx, created.Name, metav1.GetOptions{})
		if err != nil {
			return Check{Name: "image", Detail: fmt.Sprintf("reading the pod back: %v", err)}
		}
		switch live.Status.Phase {
		case corev1.PodRunning:
			return Check{Name: "image", OK: true, Detail: d.Image}
		case corev1.PodSucceeded, corev1.PodFailed:
			return Check{Name: "image", Detail: fmt.Sprintf("the image started and exited at once (%s)", live.Status.Phase)}
		}
		if reason := waitingReason(live); reason != "" {
			return Check{Name: "image", Detail: fmt.Sprintf("%s: %s", d.Image, reason)}
		}
		select {
		case <-ctx.Done():
			return Check{Name: "image", Detail: fmt.Sprintf("%s was still %s after %s", d.Image,
				lower(string(live.Status.Phase), "pending"), d.timeout())}
		case <-time.After(tokenPoll):
		}
	}
}

// waitingReason is why a container that hasn't started is waiting, for the ones
// that will never start on their own.
func waitingReason(pod *corev1.Pod) string {
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting == nil {
			continue
		}
		waiting := status.State.Waiting
		switch waiting.Reason {
		case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "CreateContainerConfigError":
			if waiting.Message == "" {
				return waiting.Reason
			}
			return waiting.Reason + ": " + waiting.Message
		}
	}
	return ""
}

// lower is a phase for a sentence, with a phase the cluster hasn't set yet
// reading as what it will be.
func lower(phase, unset string) string {
	if phase == "" {
		return unset
	}
	return strings.ToLower(phase)
}

func nodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// storageClass is the claim's class, or nil for the cluster's default.
func storageClass(name string) *string {
	if name == "" {
		return nil
	}
	return &name
}

func pullSecrets(name string) []corev1.LocalObjectReference {
	if name == "" {
		return nil
	}
	return []corev1.LocalObjectReference{{Name: name}}
}

// cpuText is millicores as Kubernetes writes them: 4 or 500m.
func cpuText(milli int64) string {
	if milli%1000 == 0 {
		return fmt.Sprintf("%d", milli/1000)
	}
	return fmt.Sprintf("%dm", milli)
}

// bytesText is a byte count in the units Kubernetes quantities use.
func bytesText(bytes int64) string {
	quantity := resource.NewQuantity(bytes, resource.BinarySI)
	return quantity.String()
}

func ptr[T any](v T) *T { return &v }
