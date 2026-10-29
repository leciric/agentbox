package k8s_test

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"

	"agentbox/internal/k8s"
)

// netPolWrites is the create and update calls a fake cluster recorded for the
// NetworkPolicy, so a test can say it was written or left alone.
func netPolWrites(client *fake.Clientset) []string {
	var writes []string
	for _, action := range client.Actions() {
		if action.GetResource().Resource != "networkpolicies" {
			continue
		}
		switch action.GetVerb() {
		case "create", "update":
			writes = append(writes, action.GetVerb()+" "+action.GetResource().Resource+" "+action.GetNamespace())
		}
	}
	return writes
}

// selectorNames reports whether a pod selector selects every one of the
// application label's values it is given.
func selectorNames(selector metav1.LabelSelector, values ...string) bool {
	sel, err := metav1.LabelSelectorAsSelector(&selector)
	if err != nil {
		return false
	}
	for _, value := range values {
		if !sel.Matches(labels.Set{k8s.LabelApp: value}) {
			return false
		}
	}
	return true
}

// policyPort is one port of a rule, for a comparison a reader can follow.
func policyPort(t *testing.T, port networkingv1.NetworkPolicyPort) (corev1.Protocol, int32) {
	t.Helper()
	if port.Protocol == nil || port.Port == nil {
		t.Fatalf("the port = %+v, want a protocol and a number", port)
	}
	return *port.Protocol, port.Port.IntVal
}

func TestNetPolSelectsAgentPodsAndDeniesIngress(t *testing.T) {
	policy := k8s.NetPol("agentbox", "https://10.8.8.1:443")
	if policy.Name != k8s.NetPolName || policy.Namespace != "agentbox" {
		t.Errorf("the policy = %s/%s, want %s/agentbox", policy.Namespace, policy.Name, k8s.NetPolName)
	}
	if policy.Labels[k8s.LabelManaged] != "true" || policy.Labels[k8s.LabelNetPol] != "true" {
		t.Errorf("labels = %v, want the managed and netpol ones", policy.Labels)
	}
	if !selectorNames(policy.Spec.PodSelector, k8s.AppAgent, k8s.AppArchive) {
		t.Errorf("the selector = %+v, want it to name both an agent and a helper pod", policy.Spec.PodSelector)
	}
	want := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}
	if !reflect.DeepEqual(policy.Spec.PolicyTypes, want) {
		t.Errorf("policy types = %v, want %v: a pod is dialled out to, never in to", policy.Spec.PolicyTypes, want)
	}
	// No ingress rule at all, with Ingress in the policy types: that is the
	// deny-all rule, and it is what the API server gives back unaltered.
	if len(policy.Spec.Ingress) != 0 {
		t.Errorf("ingress rules = %+v, want none", policy.Spec.Ingress)
	}
}

func TestNetPolAllowsDNSTheMasterAndEveryAddress(t *testing.T) {
	policy := k8s.NetPol("agentbox", "https://10.8.8.1:8443")
	if len(policy.Spec.Egress) != 3 {
		t.Fatalf("egress rules = %+v, want DNS, the master and every address", policy.Spec.Egress)
	}
	dns := policy.Spec.Egress[0]
	if len(dns.To) != 2 {
		t.Fatalf("the DNS peers = %+v, want both names a cluster labels its resolver with", dns.To)
	}
	for i, app := range []string{"kube-dns", "coredns"} {
		peer := dns.To[i]
		if peer.NamespaceSelector == nil || peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "kube-system" {
			t.Errorf("peer %d = %+v, want it in kube-system", i, peer)
		}
		if peer.PodSelector == nil || peer.PodSelector.MatchLabels["k8s-app"] != app {
			t.Errorf("peer %d = %+v, want k8s-app=%s", i, peer, app)
		}
	}
	if len(dns.Ports) != 2 {
		t.Fatalf("the DNS ports = %+v, want UDP and TCP", dns.Ports)
	}
	for i, want := range []corev1.Protocol{corev1.ProtocolUDP, corev1.ProtocolTCP} {
		protocol, port := policyPort(t, dns.Ports[i])
		if protocol != want || port != 53 {
			t.Errorf("DNS port %d = %s/%d, want %s/53", i, protocol, port, want)
		}
	}

	master := policy.Spec.Egress[1]
	if len(master.To) != 1 || master.To[0].IPBlock == nil || master.To[0].IPBlock.CIDR != "10.8.8.1/32" {
		t.Errorf("the master peer = %+v, want 10.8.8.1/32", master.To)
	}
	if len(master.Ports) != 1 {
		t.Fatalf("the master ports = %+v, want the port it is dialled on", master.Ports)
	}
	if protocol, port := policyPort(t, master.Ports[0]); protocol != corev1.ProtocolTCP || port != 8443 {
		t.Errorf("the master port = %s/%d, want TCP/8443", protocol, port)
	}

	allow := policy.Spec.Egress[2]
	if len(allow.Ports) != 0 {
		t.Errorf("the allow-all rule names ports: %+v", allow.Ports)
	}
	if len(allow.To) != 2 {
		t.Fatalf("the allow-all peers = %+v, want both address families", allow.To)
	}
	var cidrs []string
	for _, peer := range allow.To {
		if peer.IPBlock == nil {
			t.Fatalf("the allow-all peer = %+v, want a CIDR", peer)
		}
		cidrs = append(cidrs, peer.IPBlock.CIDR)
	}
	if want := []string{"0.0.0.0/0", "::/0"}; !reflect.DeepEqual(cidrs, want) {
		t.Errorf("the allow-all addresses = %v, want %v", cidrs, want)
	}
	// The cloud metadata services stay out, so a pod can't fetch the node's
	// credentials on EKS, GKE or AKS.
	excepts := [][]string{allow.To[0].IPBlock.Except, allow.To[1].IPBlock.Except}
	if want := [][]string{{"169.254.0.0/16"}, {"fd00:ec2::254/128"}}; !reflect.DeepEqual(excepts, want) {
		t.Errorf("the allow-all exceptions = %v, want %v", excepts, want)
	}
}

func TestNetPolNamesAnIPv6Master(t *testing.T) {
	policy := k8s.NetPol("agentbox", "https://[fd00::1]:7782")
	master := policy.Spec.Egress[1]
	if len(master.To) != 1 || master.To[0].IPBlock == nil || master.To[0].IPBlock.CIDR != "fd00::1/128" {
		t.Errorf("the master peer = %+v, want fd00::1/128", master.To)
	}
	if len(master.Ports) != 1 {
		t.Fatalf("the master ports = %+v, want one", master.Ports)
	}
	if _, port := policyPort(t, master.Ports[0]); port != 7782 {
		t.Errorf("the master port = %d, want 7782", port)
	}
}

func TestNetPolSkipsAMasterThatDoesNotResolve(t *testing.T) {
	// A name this machine can't resolve is no reason to fail: the rule that
	// allows every address already covers the master.
	policy := k8s.NetPol("agentbox", "https://master.invalid:6443")
	if len(policy.Spec.Egress) != 2 {
		t.Fatalf("egress rules = %+v, want DNS and every address", policy.Spec.Egress)
	}
	for _, peer := range policy.Spec.Egress[1].To {
		if peer.IPBlock == nil {
			continue
		}
		switch peer.IPBlock.CIDR {
		case "0.0.0.0/0", "::/0":
		default:
			t.Errorf("the policy names %s, which is a master rule that shouldn't be there", peer.IPBlock.CIDR)
		}
	}
}

func TestNetPolIsCreatedWithTheCluster(t *testing.T) {
	client := fake.NewClientset()
	cluster := k8s.FromClient("k3d", "agentbox", client)
	ctx := context.Background()
	if err := cluster.EnsureNetPol(ctx, "https://10.8.8.1:443"); err != nil {
		t.Fatalf("EnsureNetPol() = %v", err)
	}
	if writes := netPolWrites(client); !reflect.DeepEqual(writes, []string{"create networkpolicies agentbox"}) {
		t.Errorf("writes = %v, want one create", writes)
	}
	policy, err := client.NetworkingV1().NetworkPolicies("agentbox").Get(ctx, k8s.NetPolName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the policy wasn't made: %v", err)
	}
	if len(policy.Spec.Ingress) != 0 {
		t.Errorf("ingress rules = %+v, want none", policy.Spec.Ingress)
	}
	if !selectorNames(policy.Spec.PodSelector, k8s.AppAgent, k8s.AppArchive) {
		t.Errorf("the selector = %+v, want the agent and helper pods", policy.Spec.PodSelector)
	}
}

func TestNetPolIsUpdatedInPlace(t *testing.T) {
	// A policy someone changed to allow ingress, with a resource version an
	// update has to carry and a label that isn't AgentBox's.
	live := k8s.NetPol("agentbox", "https://10.8.8.1:443")
	live.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{}}
	live.Labels["example.com/owner"] = "keep me"
	live.ResourceVersion = "7"
	client := fake.NewClientset(live)
	cluster := k8s.FromClient("k3d", "agentbox", client)
	ctx := context.Background()
	client.ClearActions()
	if err := cluster.EnsureNetPol(ctx, "https://10.8.8.1:443"); err != nil {
		t.Fatalf("EnsureNetPol() = %v", err)
	}
	if writes := netPolWrites(client); !reflect.DeepEqual(writes, []string{"update networkpolicies agentbox"}) {
		t.Errorf("writes = %v, want one update in place", writes)
	}
	policy, err := client.NetworkingV1().NetworkPolicies("agentbox").Get(ctx, k8s.NetPolName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Spec.Ingress) != 0 {
		t.Errorf("ingress rules = %+v, want the policy corrected", policy.Spec.Ingress)
	}
	if policy.ResourceVersion != "7" {
		t.Errorf("resource version = %q, want the one it read", policy.ResourceVersion)
	}
	if policy.Labels["example.com/owner"] != "keep me" {
		t.Errorf("labels = %v, want the other tool's label kept", policy.Labels)
	}
	if policy.Labels[k8s.LabelNetPol] != "true" {
		t.Errorf("labels = %v, want AgentBox's own kept", policy.Labels)
	}
}

func TestNetPolLeavesARightPolicyAlone(t *testing.T) {
	client := fake.NewClientset(k8s.NetPol("agentbox", "https://10.8.8.1:443"))
	cluster := k8s.FromClient("k3d", "agentbox", client)
	ctx := context.Background()
	before, err := client.NetworkingV1().NetworkPolicies("agentbox").Get(ctx, k8s.NetPolName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	if err := cluster.EnsureNetPol(ctx, "https://10.8.8.1:443"); err != nil {
		t.Fatalf("EnsureNetPol() = %v", err)
	}
	if writes := netPolWrites(client); len(writes) != 0 {
		t.Errorf("a policy that was already right was written again: %v", writes)
	}
	after, err := client.NetworkingV1().NetworkPolicies("agentbox").Get(ctx, k8s.NetPolName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("the policy changed at all: %+v then %+v", before, after)
	}
}
