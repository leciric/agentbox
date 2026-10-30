package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Claim is one agent's workspace claim as the cluster has it: the volume its
// clone and home live on.
type Claim struct {
	// Name, Project and Agent name it: the name comes from the claim, the two
	// others from its labels, which every object AgentBox makes carries.
	Name    string
	Project string
	Agent   string
	// Storage is what the claim asks for, as the cluster reports it: the
	// request, or what the volume actually holds once it is bound.
	Storage string
	Created time.Time
}

// Claims is every workspace claim AgentBox made in the namespace, which is what
// a cluster's page lists as its PVCs. A claim carries the same project and agent
// labels its pod does, so the page can say whose it is even while the pod is
// stopped.
func (c *Cluster) Claims(ctx context.Context) ([]Claim, error) {
	claims, err := c.client.CoreV1().PersistentVolumeClaims(c.namespace).List(ctx, metav1.ListOptions{LabelSelector: ManagedSelector()})
	if err != nil {
		return nil, fmt.Errorf("listing the claims of cluster %s: %w", c.name, err)
	}
	out := make([]Claim, 0, len(claims.Items))
	for i := range claims.Items {
		out = append(out, claimFrom(&claims.Items[i]))
	}
	return out, nil
}

// claimFrom is what the page and the daemon read of one claim: whose it is, how
// big, and when it was made.
func claimFrom(claim *corev1.PersistentVolumeClaim) Claim {
	storage := claim.Spec.Resources.Requests[corev1.ResourceStorage]
	if storage.IsZero() {
		// A claim that is already bound reports what it got under its status,
		// which is the only size it has when its request was written by
		// something else.
		storage = claim.Status.Capacity[corev1.ResourceStorage]
	}
	size := ""
	if !storage.IsZero() {
		size = storage.String()
	}
	return Claim{
		Name:    claim.Name,
		Project: claim.Labels[LabelProject],
		Agent:   claim.Labels[LabelAgent],
		Storage: size,
		Created: claim.CreationTimestamp.Time,
	}
}
