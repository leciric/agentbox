package k8s

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	// EgressProxyGroup is the ProxyGroup the egress Services name. The proxies in
	// it are the pods that dial the tailnet, so one group serves every host of
	// every project in this namespace.
	EgressProxyGroup = "agentbox-egress"
	// LabelTailnetEgress marks the Services AgentBox declares to the operator,
	// which is how they are found again without a record of their names.
	LabelTailnetEgress = "agentbox/tailnet-egress"
	// AnnotationTailnetFQDN is the tailnet name an egress Service stands for.
	// The operator resolves it and the pods never see it.
	AnnotationTailnetFQDN = "tailscale.com/tailnet-fqdn"
	// AnnotationProxyGroup is the ProxyGroup whose proxies dial it.
	AnnotationProxyGroup = "tailscale.com/proxy-group"
	// EgressMasterService is the Service a cluster's master URL is declared
	// under, and the name an agent pod dials.
	EgressMasterService = "agentbox-egress-master"
)

// tailscaleDNSConfig is the operator's DNSConfig CR, where the split DNS that
// sends a tailnet name to the tailnet is declared.
var tailscaleDNSConfig = schema.GroupVersionResource{Group: "tailscale.com", Version: "v1alpha1", Resource: "dnsconfigs"}

// EgressHost is one tailnet name a cluster's pods reach through the operator's
// egress proxy.
//
// A pod never speaks to the tailnet itself. It dials an ExternalName Service in
// its own namespace, the operator's proxy pods dial the tailnet name, and the
// operator carries the connection between them. That Service is the whole
// declaration: its external name is the MagicDNS name, and two annotations tell
// the operator which proxy group serves it and that the name is a tailnet one
// rather than a DNS record it should look up.
//
// The Services alone are not enough. The cluster's resolver has to be told to
// send those names to the operator's DNS proxy, which is what a DNSConfig CR
// does: without it a pod resolves the name against the cluster's own DNS and
// gets nothing.
type EgressHost struct {
	// Name is the Service name, DNS-safe: agentbox-egress-master.
	Name string
	// Host is the MagicDNS name: monsterdev.example.ts.net.
	Host string
	// Ports are the ports the pods use: 443, and 22 for git over SSH.
	Ports []int32
}

// EgressService is the ExternalName Service that declares one host and its ports
// to the operator. It is a declaration, not a listener: nothing in the namespace
// serves those ports, and the proxy pods are created from it.
func EgressService(host EgressHost, namespace string) *corev1.Service {
	ports := make([]corev1.ServicePort, 0, len(host.Ports))
	for _, port := range host.Ports {
		ports = append(ports, corev1.ServicePort{
			Name:     egressPortName(port),
			Port:     port,
			Protocol: corev1.ProtocolTCP,
		})
	}
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      host.Name,
			Namespace: namespace,
			Labels: map[string]string{
				LabelManaged:       "true",
				LabelTailnetEgress: "true",
			},
			Annotations: map[string]string{
				AnnotationTailnetFQDN: host.Host,
				AnnotationProxyGroup:  EgressProxyGroup,
			},
		},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: host.Host,
			Ports:        ports,
		},
	}
}

// egressPortName is the name a port is declared under. The two the agents use
// get the name a URL reads well in, and anything else is named after its number,
// because a Service port needs a name and two hosts don't share one.
func egressPortName(port int32) string {
	switch port {
	case 443:
		return "https"
	case 22:
		return "ssh"
	}
	return fmt.Sprintf("p-%d", port)
}

// EnsureEgress creates or replaces the Services for hosts, in the namespace. It
// is what `cluster add --tailnet operator` and agent creation call; a Service
// that is already right is left alone, so starting an agent doesn't rewrite the
// cluster's objects on every start.
//
// An existing Service keeps its object, because its name is what a pod dials and
// what the operator has already reconciled. Only the fields AgentBox owns are
// written: everything the operator and kubectl put on it stays.
func (c *Cluster) EnsureEgress(ctx context.Context, hosts []EgressHost) error {
	services := c.client.CoreV1().Services(c.namespace)
	for _, host := range hosts {
		want := EgressService(host, c.namespace)
		live, err := services.Get(ctx, want.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if _, err := services.Create(ctx, want, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("creating the egress Service %s: %w", want.Name, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("reading the egress Service %s: %w", want.Name, err)
		}
		if sameEgress(live, want) {
			continue
		}
		// The live object is the base of the update: the operator adds owner
		// references, finalizers, labels and annotations of its own, and a fresh
		// object would erase them. Losing its owner reference can orphan the
		// proxies it made from this Service.
		updated := live.DeepCopy()
		updated.Spec.Type = want.Spec.Type
		updated.Spec.ExternalName = want.Spec.ExternalName
		updated.Spec.Ports = want.Spec.Ports
		updated.Annotations = withOwned(updated.Annotations, want.Annotations)
		updated.Labels = withOwned(updated.Labels, want.Labels)
		if _, err := services.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("updating the egress Service %s: %w", want.Name, err)
		}
	}
	return nil
}

// withOwned is live with the entries AgentBox writes set to what it wants, and
// every other entry left alone. It edits live in place and returns it.
func withOwned(live, owned map[string]string) map[string]string {
	if live == nil && len(owned) > 0 {
		live = map[string]string{}
	}
	for key, value := range owned {
		live[key] = value
	}
	return live
}

// sameEgress is whether a live Service already declares what want does. Only the
// fields AgentBox writes are compared: an annotation, label, owner reference or
// finalizer the operator added is not a reason to replace the Service.
func sameEgress(live, want *corev1.Service) bool {
	if live.Spec.Type != want.Spec.Type || live.Spec.ExternalName != want.Spec.ExternalName {
		return false
	}
	if !slices.EqualFunc(live.Spec.Ports, want.Spec.Ports, func(a, b corev1.ServicePort) bool {
		return a.Name == b.Name && a.Port == b.Port && a.Protocol == b.Protocol
	}) {
		return false
	}
	for key, value := range want.Annotations {
		if live.Annotations[key] != value {
			return false
		}
	}
	for key, value := range want.Labels {
		if live.Labels[key] != value {
			return false
		}
	}
	return true
}

// MasterEgressHost is the egress Service a cluster's master URL asks for: the
// name the URL reaches, on 443 and 22, the ports an agent uses for git over
// https and ssh. ok is false when there is no host to declare, which is a
// cluster whose pods have nowhere to dial anyway.
func MasterEgressHost(masterURL string) (host EgressHost, ok bool) {
	parsed, err := url.Parse(strings.TrimSpace(masterURL))
	if err != nil || parsed.Hostname() == "" {
		return EgressHost{}, false
	}
	return EgressHost{Name: EgressMasterService, Host: parsed.Hostname(), Ports: []int32{443, 22}}, true
}

// EgressCheck is what `cluster doctor` reports for one host: the Service exists,
// declares that external name, and (when the operator's DNSConfig CRD is
// installed) the host is declared in a DNSConfig's split DNS. A cluster without
// the operator fails this, because --tailnet operator is a promise that pods
// reach the tailnet.
//
// installed says the operator is meant to be there, which is what the cluster's
// --tailnet operator promises: this doesn't guess at it. The DNSConfig half
// needs the REST config the dynamic client is built from, so a Cluster over a
// clientset skips it and reports the Service on its own.
func (c *Cluster) EgressCheck(ctx context.Context, hosts []EgressHost, installed bool) []Check {
	if !installed {
		return []Check{{
			Name: "tailnet",
			Detail: "the Tailscale operator's DNSConfig CRD isn't installed, so --tailnet operator can't work: " +
				"pods reach the tailnet through the operator's egress proxy, and nothing else declares one. " +
				"Install the operator with an OAuth client, which is what lets it make the proxies' auth keys",
		}}
	}
	domains, read, dns := c.splitDNS(ctx)
	checks := make([]Check, 0, len(hosts)+1)
	for _, host := range hosts {
		checks = append(checks, c.egressCheck(ctx, host, domains, read))
	}
	return append(checks, *dns)
}

// splitDNSRead is how far the operator's split DNS could be read, which decides
// what a host's check can say about its name.
type splitDNSRead int

const (
	// splitDNSUnread is a cluster whose DNSConfigs couldn't be read: only the
	// Service is checked.
	splitDNSUnread splitDNSRead = iota
	// splitDNSUndeclared is the CRD being installed with no split DNS domain in
	// any DNSConfig, which is also how a cluster that forwards the tailnet names
	// with a CoreDNS stub looks.
	splitDNSUndeclared
	// splitDNSDeclared is at least one domain to compare a host against.
	splitDNSDeclared
)

// egressCheck is one host: its Service is there and declares it, and, when the
// split DNS could be read, the tailnet name is one a pod can resolve.
func (c *Cluster) egressCheck(ctx context.Context, host EgressHost, domains []string, read splitDNSRead) Check {
	name := "tailnet " + host.Host
	live, err := c.client.CoreV1().Services(c.namespace).Get(ctx, host.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Check{Name: name, Detail: fmt.Sprintf("the egress Service %s/%s is missing: add the cluster again with --tailnet operator",
			c.namespace, host.Name)}
	}
	if err != nil {
		return Check{Name: name, Detail: fmt.Sprintf("reading the egress Service %s/%s: %v", c.namespace, host.Name, err)}
	}
	if !sameEgress(live, EgressService(host, c.namespace)) {
		return Check{Name: name, Detail: fmt.Sprintf("the egress Service %s/%s doesn't declare %s with the operator's annotations: add the cluster again with --tailnet operator",
			c.namespace, host.Name, host.Host)}
	}
	switch read {
	case splitDNSUndeclared:
		// The documented setup, scripts/k3d-up.sh --operator, writes a DNSConfig
		// for the operator's nameserver and forwards the tailnet names with a
		// CoreDNS stub instead of split DNS. A correct cluster therefore has no
		// split DNS to compare against, and this is not a failure.
		return Check{Name: name, Skipped: true, Detail: fmt.Sprintf(
			"the egress Service %s/%s declares %s, but no DNSConfig's split DNS does: a cluster prepared by scripts/k3d-up.sh --operator forwards the name with a CoreDNS stub instead, so this wasn't checked",
			c.namespace, host.Name, host.Host)}
	case splitDNSDeclared:
		if !covers(host.Host, domains) {
			return Check{Name: name, Detail: fmt.Sprintf("no DNSConfig's split DNS sends %s to the tailnet, so a pod can't resolve it", host.Host)}
		}
		return Check{Name: name, OK: true, Detail: fmt.Sprintf("%s via %s, resolved by the operator's split DNS", host.Host, host.Name)}
	default:
		return Check{Name: name, OK: true, Detail: fmt.Sprintf("%s via %s", host.Host, host.Name)}
	}
}

// splitDNS is the domains the operator's DNSConfigs send to the tailnet, how far
// they could be read, and a Check saying why they weren't read when they weren't.
// A cluster whose operator came without the DNSConfig CRD, or one built from a
// clientset, has no split DNS to read: that is skipped rather than failed,
// because the Services are still right and saying so is the doctor's job here.
func (c *Cluster) splitDNS(ctx context.Context) ([]string, splitDNSRead, *Check) {
	restConfig, err := c.RESTConfig()
	if err != nil {
		return nil, splitDNSUnread, &Check{Name: "tailnet dns", Skipped: true, Detail: "the split DNS wasn't read: " + err.Error()}
	}
	resources, err := c.client.Discovery().ServerResourcesForGroupVersion(tailscaleDNSConfig.GroupVersion().String())
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, splitDNSUnread, &Check{Name: "tailnet dns", Skipped: true, Detail: "the operator's DNSConfig CRD isn't installed, so no CR declares the tailnet's split DNS"}
		}
		return nil, splitDNSUnread, &Check{Name: "tailnet dns", Detail: fmt.Sprintf("looking for the operator's DNSConfig CRD: %v", err)}
	}
	if !slices.ContainsFunc(resources.APIResources, func(resource metav1.APIResource) bool {
		return resource.Name == tailscaleDNSConfig.Resource
	}) {
		return nil, splitDNSUnread, &Check{Name: "tailnet dns", Skipped: true, Detail: "the operator's DNSConfig CRD isn't installed, so no CR declares the tailnet's split DNS"}
	}
	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, splitDNSUnread, &Check{Name: "tailnet dns", Detail: fmt.Sprintf("reading the operator's DNSConfigs: %v", err)}
	}
	list, err := client.Resource(tailscaleDNSConfig).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, splitDNSUnread, &Check{Name: "tailnet dns", Detail: fmt.Sprintf("listing the operator's DNSConfigs: %v", err)}
	}
	var domains []string
	for i := range list.Items {
		// A DNSConfig the operator itself would refuse is passed over: the field
		// it can't read isn't a domain anyone gets sent to the tailnet with.
		split, found, err := unstructured.NestedSlice(list.Items[i].Object, "spec", "splitDNS")
		if err != nil || !found {
			continue
		}
		for _, entry := range split {
			declaration, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			names, _, err := unstructured.NestedStringSlice(declaration, "domains")
			if err != nil {
				continue
			}
			domains = append(domains, names...)
		}
	}
	if len(domains) == 0 {
		return nil, splitDNSUndeclared, &Check{Name: "tailnet dns", Skipped: true, Detail: "no DNSConfig declares a split DNS domain: a cluster prepared by scripts/k3d-up.sh --operator forwards the tailnet names with a CoreDNS stub instead, so this wasn't checked"}
	}
	return domains, splitDNSDeclared, &Check{Name: "tailnet dns", OK: true, Detail: strings.Join(domains, ", ")}
}

// covers is whether one of the domains sent to the tailnet answers name: the
// name itself, a domain it is under, or a wildcard it is under. `*.example.com`
// covers a.example.com and names below it, and not example.com itself.
func covers(name string, domains []string) bool {
	for _, domain := range domains {
		domain = strings.TrimSuffix(strings.TrimSpace(domain), ".")
		if domain == "" {
			continue
		}
		if base, wildcard := strings.CutPrefix(domain, "*."); wildcard {
			if base != "" && name != base && strings.HasSuffix(name, "."+base) {
				return true
			}
			continue
		}
		if name == domain || strings.HasSuffix(name, "."+domain) {
			return true
		}
	}
	return false
}
