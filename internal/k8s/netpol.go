package k8s

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// NetPolName is the one NetworkPolicy a cluster's namespace carries. One
	// policy for every agent, because every agent's pod is selected the same
	// way and there is nothing per-agent to say about it.
	NetPolName = "agentbox-agents"
	// LabelNetPol marks it as AgentBox's, next to the managed label every
	// object carries. It names what the object is so that one listing the
	// namespace can tell it apart from a policy an operator wrote.
	LabelNetPol = "agentbox/netpol"
	// dnsNamespace and dnsApp are the cluster's own resolver: kube-dns in most
	// clusters, coredns in the ones that label it that way (k3s among them).
	dnsNamespace = "kube-system"
	dnsApp       = "k8s-app"
	// masterLookupTimeout bounds resolving the master's name. It is a courtesy
	// to a daemon whose resolver is slow, not a dependency: a name that doesn't
	// answer in time only leaves the rule out.
	masterLookupTimeout = 2 * time.Second
)

// lookupIP resolves the master's name from this machine, which is what turns a
// name into the address a policy names. It is replaced in tests.
var lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// NetPol is the namespace's NetworkPolicy for agent pods: nothing in the
// cluster may open a connection to one, and the pod itself dials out for its
// session, its git and the AI APIs.
//
// The ingress half is the point. An agent's pod listens for nothing, so a
// policy that denies all ingress takes away no working path, and it keeps
// everything else in the cluster (another namespace, a compromised workload, a
// Service someone points at it) from reaching a pod that carries a token and a
// checkout of the project.
//
// Egress is allowed in full, and deliberately so. The pod dials the public AI
// APIs, whose addresses are neither fixed nor knowable here, so a narrower set
// would be a list that goes stale and breaks a chat. What makes a narrow set
// possible is a proxy the pod dials instead, and that is a change to the pod,
// not to this policy: the DNS and master rules below are the ones such a policy
// would keep, and the last rule allows every other address until there is a
// proxy to narrow it to.
//
// masterURL is the address the cluster's pods dial this daemon at. It is
// resolved here when it is a name, because a NetworkPolicy names addresses.
func NetPol(namespace, masterURL string) *networkingv1.NetworkPolicy {
	egress := []networkingv1.NetworkPolicyEgressRule{dnsEgress()}
	if peer, port, ok := masterEgress(masterURL); ok {
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{
			To:    []networkingv1.NetworkPolicyPeer{peer},
			Ports: []networkingv1.NetworkPolicyPort{tcpPort(port)},
		})
	}
	// Last, and on purpose: see the comment on NetPol. The only addresses left
	// out are the cloud metadata services. On EKS, GKE and AKS a pod can
	// otherwise fetch the node's credentials from 169.254.169.254 (and its
	// IPv6 endpoint), and no chat needs them. The private ranges stay in: a
	// cluster whose master or registry lives in one must still be reachable.
	egress = append(egress, networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{
			{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{"169.254.0.0/16"}}},
			{IPBlock: &networkingv1.IPBlock{CIDR: "::/0", Except: []string{"fd00:ec2::254/128"}}},
		},
	})
	return &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      NetPolName,
			Namespace: namespace,
			Labels: map[string]string{
				LabelManaged: "true",
				LabelNetPol:  "true",
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			// Only agent pods: the namespace's other objects are not this
			// policy's business.
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{LabelApp: AppAgent}},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			// No ingress rule, and Ingress in PolicyTypes: that is what denies
			// every connection in. Absent rather than an empty list, because
			// the API server drops an empty one, and the object read back with
			// nil would then look changed on every apply.
			Egress: egress,
		},
	}
}

// dnsEgress is the rule a pod needs to resolve anything at all: the cluster's
// own resolver, on 53 over UDP and TCP. Both names are named because a cluster
// labels its resolver one way or the other, and both selectors are in one peer
// each so that the namespace and the label have to match together.
func dnsEgress() networkingv1.NetworkPolicyEgressRule {
	peers := make([]networkingv1.NetworkPolicyPeer, 0, 2)
	for _, app := range []string{"kube-dns", "coredns"} {
		peers = append(peers, networkingv1.NetworkPolicyPeer{
			// The label the API server puts on every namespace, so this
			// needs no name a user chose.
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": dnsNamespace}},
			PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{dnsApp: app}},
		})
	}
	return networkingv1.NetworkPolicyEgressRule{
		To: peers,
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: ptr(corev1.ProtocolUDP), Port: &intstr.IntOrString{Type: intstr.Int, IntVal: 53}},
			{Protocol: ptr(corev1.ProtocolTCP), Port: &intstr.IntOrString{Type: intstr.Int, IntVal: 53}},
		},
	}
}

// masterEgress is the rule that names this daemon, for a cluster whose
// NetworkPolicy is later narrowed: the master's address as a single host, on
// the port the master URL names. ok is false when there is no address to name,
// which is a master URL that is empty, unparseable, or a name this machine
// can't resolve. Leaving the rule out costs nothing, because the allow-all rule
// already covers the master.
func masterEgress(masterURL string) (peer networkingv1.NetworkPolicyPeer, port int32, ok bool) {
	parsed, err := url.Parse(strings.TrimSpace(masterURL))
	if err != nil || parsed.Hostname() == "" {
		return networkingv1.NetworkPolicyPeer{}, 0, false
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil {
		ctx, cancel := context.WithTimeout(context.Background(), masterLookupTimeout)
		defer cancel()
		addrs, err := lookupIP(ctx, parsed.Hostname())
		if err != nil || len(addrs) == 0 {
			return networkingv1.NetworkPolicyPeer{}, 0, false
		}
		// The first address is the one the daemon dials, and a policy can't
		// hold the set: a name with several addresses is covered by the rule
		// that allows every address anyway.
		ip = addrs[0]
	}
	if v4 := ip.To4(); v4 != nil {
		return networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: v4.String() + "/32"}}, masterPort(parsed), true
	}
	return networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: ip.String() + "/128"}}, masterPort(parsed), true
}

// masterPort is the port the master URL names, and the scheme's default when it
// names none.
func masterPort(parsed *url.URL) int32 {
	if text := parsed.Port(); text != "" {
		if port, err := strconv.Atoi(text); err == nil && port > 0 && port < 65536 {
			return int32(port)
		}
	}
	if parsed.Scheme == "http" {
		return 80
	}
	return 443
}

// tcpPort is one port, as a policy names it.
func tcpPort(port int32) networkingv1.NetworkPolicyPort {
	return networkingv1.NetworkPolicyPort{
		Protocol: ptr(corev1.ProtocolTCP),
		Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: port},
	}
}

// EnsureNetPol creates the namespace's NetworkPolicy, or corrects the one that
// is there. It is what `cluster add`, `cluster set` and `cluster rotate` apply:
// a policy that already says the right thing is left alone, so a change to a
// cluster's address only rewrites it when the address matters.
//
// An existing policy keeps its object: only the fields AgentBox owns are
// written, so anything else put on it stays.
func (c *Cluster) EnsureNetPol(ctx context.Context, masterURL string) error {
	policies := c.client.NetworkingV1().NetworkPolicies(c.namespace)
	want := NetPol(c.namespace, masterURL)
	live, err := policies.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := policies.Create(ctx, want, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating the NetworkPolicy %s: %w", want.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the NetworkPolicy %s: %w", want.Name, err)
	}
	if sameNetPol(live, want) {
		return nil
	}
	updated := live.DeepCopy()
	updated.Spec = want.Spec
	updated.Labels = withOwned(updated.Labels, want.Labels)
	if _, err := policies.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating the NetworkPolicy %s: %w", want.Name, err)
	}
	return nil
}

// sameNetPol is whether a live policy already declares what want does. Only the
// spec and the labels AgentBox writes are compared: a label, annotation or
// finalizer another tool added is not a reason to rewrite it.
func sameNetPol(live, want *networkingv1.NetworkPolicy) bool {
	if !equality.Semantic.DeepEqual(live.Spec, want.Spec) {
		return false
	}
	for key, value := range want.Labels {
		if live.Labels[key] != value {
			return false
		}
	}
	return true
}
