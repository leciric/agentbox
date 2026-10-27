package k8s_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"agentbox/internal/k8s"
)

// TestClaimsReadsTheAgentsLabelsAndSize is what a cluster's page lists as PVCs:
// every claim AgentBox made, whose it is, and how big.
func TestClaimsReadsTheAgentsLabelsAndSize(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	cluster := k8s.FromClient("k3d", "", client)

	claim, err := k8s.BuildPVC(agentSpec())
	if err != nil {
		t.Fatal(err)
	}
	claim.Namespace = "agentbox"
	if _, err := client.CoreV1().PersistentVolumeClaims("agentbox").Create(ctx, claim, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	// A bound claim whose size is only under its status, which is what a
	// volume provisioned by something else reports.
	bound := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ab-pawly-agent-02-workspace",
			Namespace: "agentbox",
			Labels: map[string]string{
				k8s.LabelManaged: "true",
				k8s.LabelProject: "pawly",
				k8s.LabelAgent:   "agent-02",
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
		},
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("agentbox").Create(ctx, bound, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	// Something else's claim in the same namespace is not AgentBox's.
	other := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "agentbox"}}
	if _, err := client.CoreV1().PersistentVolumeClaims("agentbox").Create(ctx, other, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	claims, err := cluster.Claims(ctx)
	if err != nil {
		t.Fatalf("Claims() = %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("Claims() = %+v, want the two AgentBox made", claims)
	}
	byName := map[string]k8s.Claim{}
	for _, claim := range claims {
		byName[claim.Name] = claim
	}
	ours := byName[claim.Name]
	if ours.Project != "pawly" || ours.Agent != "agent-01" {
		t.Errorf("the claim's owner = %q/%q, want pawly/agent-01", ours.Project, ours.Agent)
	}
	if ours.Storage != k8s.DefaultWorkspaceSize {
		t.Errorf("the claim's size = %q, want %s", ours.Storage, k8s.DefaultWorkspaceSize)
	}
	if got := byName[bound.Name].Storage; got != "5Gi" {
		t.Errorf("a bound claim's size = %q, want its status capacity", got)
	}
}
