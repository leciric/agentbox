package k8s_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"agentbox/internal/k8s"
)

// masterHost is the tailnet name an agent uses to reach this machine, and the
// git remote on it: https on 443, and git over SSH on 22.
func masterHost() k8s.EgressHost {
	return k8s.EgressHost{
		Name:  "agentbox-egress-master",
		Host:  "monsterdev.example.ts.net",
		Ports: []int32{443, 22},
	}
}

// egressClient is a fake cluster with the client its calls are recorded on.
func egressClient() (*fake.Clientset, *k8s.Cluster) {
	client := fake.NewClientset()
	return client, k8s.FromClient("k3d", "", client)
}

// egressWrites is the create and update calls a fake cluster recorded, so a test
// can say a Service was left alone rather than only that it looks right.
func egressWrites(client *fake.Clientset) []string {
	var writes []string
	for _, action := range client.Actions() {
		switch action.GetVerb() {
		case "create", "update":
			writes = append(writes, action.GetVerb()+" "+action.GetResource().Resource+" "+action.GetNamespace())
		}
	}
	return writes
}

func TestTailnetServiceDeclaresTheHost(t *testing.T) {
	service := k8s.EgressService(masterHost(), "agentbox")
	if service.Name != "agentbox-egress-master" || service.Namespace != "agentbox" {
		t.Errorf("the Service = %s/%s, want agentbox-egress-master/agentbox", service.Namespace, service.Name)
	}
	if service.Spec.Type != corev1.ServiceTypeExternalName {
		t.Errorf("type = %q, want ExternalName: nothing in the namespace listens", service.Spec.Type)
	}
	if service.Spec.ExternalName != "monsterdev.example.ts.net" {
		t.Errorf("external name = %q, want the MagicDNS name", service.Spec.ExternalName)
	}
	// The two annotations are what the operator reads: which name this is, and
	// whose proxies dial it.
	if got := service.Annotations[k8s.AnnotationTailnetFQDN]; got != "monsterdev.example.ts.net" {
		t.Errorf("%s = %q, want the MagicDNS name", k8s.AnnotationTailnetFQDN, got)
	}
	if got := service.Annotations[k8s.AnnotationProxyGroup]; got != k8s.EgressProxyGroup {
		t.Errorf("%s = %q, want %s", k8s.AnnotationProxyGroup, got, k8s.EgressProxyGroup)
	}
	if service.Labels[k8s.LabelManaged] != "true" || service.Labels[k8s.LabelTailnetEgress] != "true" {
		t.Errorf("labels = %v, want the managed and tailnet-egress ones", service.Labels)
	}
	want := []corev1.ServicePort{
		{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP},
		{Name: "ssh", Port: 22, Protocol: corev1.ProtocolTCP},
	}
	if !reflect.DeepEqual(service.Spec.Ports, want) {
		t.Errorf("ports = %+v, want %+v", service.Spec.Ports, want)
	}
}

func TestTailnetServiceNamesAnotherPortAfterItsNumber(t *testing.T) {
	host := k8s.EgressHost{Name: "agentbox-egress-db", Host: "db.example.ts.net", Ports: []int32{5432}}
	service := k8s.EgressService(host, "agentbox")
	if got := service.Spec.Ports[0].Name; got != "p-5432" {
		t.Errorf("the port is named %q, want p-5432", got)
	}
}

func TestTailnetEgressCreatesTheServices(t *testing.T) {
	client, cluster := egressClient()
	if err := cluster.EnsureEgress(context.Background(), []k8s.EgressHost{masterHost()}); err != nil {
		t.Fatalf("EnsureEgress() = %v", err)
	}
	service, err := client.CoreV1().Services("agentbox").Get(context.Background(), "agentbox-egress-master", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the egress Service wasn't made: %v", err)
	}
	if service.Spec.ExternalName != "monsterdev.example.ts.net" || len(service.Spec.Ports) != 2 {
		t.Errorf("the Service = %+v, want the host declared on two ports", service.Spec)
	}
}

func TestTailnetEgressLeavesARightServiceAlone(t *testing.T) {
	client, cluster := egressClient()
	ctx := context.Background()
	hosts := []k8s.EgressHost{masterHost()}
	if err := cluster.EnsureEgress(ctx, hosts); err != nil {
		t.Fatalf("EnsureEgress() = %v", err)
	}
	before, err := client.CoreV1().Services("agentbox").Get(ctx, "agentbox-egress-master", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	if err := cluster.EnsureEgress(ctx, hosts); err != nil {
		t.Fatalf("the second EnsureEgress() = %v", err)
	}
	if writes := egressWrites(client); len(writes) != 0 {
		t.Errorf("a Service that was already right was written again: %v", writes)
	}
	after, err := client.CoreV1().Services("agentbox").Get(ctx, "agentbox-egress-master", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("the Service changed at all: %+v then %+v", before, after)
	}
}

func TestTailnetEgressReplacesAChangedHost(t *testing.T) {
	client, cluster := egressClient()
	ctx := context.Background()
	if err := cluster.EnsureEgress(ctx, []k8s.EgressHost{masterHost()}); err != nil {
		t.Fatalf("EnsureEgress() = %v", err)
	}
	// The same Service, now for a renamed machine and with one port instead of
	// two.
	moved := k8s.EgressHost{
		Name:  "agentbox-egress-master",
		Host:  "nas.example.ts.net",
		Ports: []int32{443},
	}
	client.ClearActions()
	if err := cluster.EnsureEgress(ctx, []k8s.EgressHost{moved}); err != nil {
		t.Fatalf("EnsureEgress() = %v", err)
	}
	if writes := egressWrites(client); !reflect.DeepEqual(writes, []string{"update services agentbox"}) {
		t.Errorf("writes = %v, want one update in place", writes)
	}
	service, err := client.CoreV1().Services("agentbox").Get(ctx, "agentbox-egress-master", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if service.Spec.ExternalName != "nas.example.ts.net" {
		t.Errorf("external name = %q, want the one it was moved to", service.Spec.ExternalName)
	}
	if got := service.Annotations[k8s.AnnotationTailnetFQDN]; got != "nas.example.ts.net" {
		t.Errorf("%s = %q, want the one it was moved to", k8s.AnnotationTailnetFQDN, got)
	}
	if len(service.Spec.Ports) != 1 || service.Spec.Ports[0].Port != 443 {
		t.Errorf("ports = %+v, want only 443", service.Spec.Ports)
	}
}

func TestTailnetEgressKeepsTheResourceVersion(t *testing.T) {
	// A Service the cluster already had, with a resource version an update has
	// to carry.
	live := k8s.EgressService(masterHost(), "agentbox")
	live.Spec.ExternalName = "old.example.ts.net"
	live.ResourceVersion = "7"
	client := fake.NewClientset(live)
	cluster := k8s.FromClient("k3d", "", client)
	if err := cluster.EnsureEgress(context.Background(), []k8s.EgressHost{masterHost()}); err != nil {
		t.Fatalf("EnsureEgress() = %v", err)
	}
	service, err := client.CoreV1().Services("agentbox").Get(context.Background(), "agentbox-egress-master", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if service.ResourceVersion != "7" {
		t.Errorf("resource version = %q, want the one it read", service.ResourceVersion)
	}
}

func TestTailnetEgressKeepsWhatTheOperatorPutOnTheService(t *testing.T) {
	// The operator reconciles the Service and marks it as its own: an update
	// that started from a fresh object would drop that, and losing the owner
	// reference can orphan the proxies it made from the Service.
	live := k8s.EgressService(masterHost(), "agentbox")
	live.Annotations["tailscale.com/last-applied"] = "keep me"
	live.Labels["tailscale.com/managed"] = "true"
	live.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: "tailscale.com/v1alpha1",
		Kind:       "ProxyGroup",
		Name:       k8s.EgressProxyGroup,
		UID:        "proxy-group-uid",
	}}
	live.Finalizers = []string{"tailscale.com/cleanup"}
	client := fake.NewClientset(live)
	cluster := k8s.FromClient("k3d", "", client)
	moved := k8s.EgressHost{Name: "agentbox-egress-master", Host: "nas.example.ts.net", Ports: []int32{443}}
	if err := cluster.EnsureEgress(context.Background(), []k8s.EgressHost{moved}); err != nil {
		t.Fatalf("EnsureEgress() = %v", err)
	}
	service, err := client.CoreV1().Services("agentbox").Get(context.Background(), "agentbox-egress-master", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if service.Spec.ExternalName != "nas.example.ts.net" {
		t.Errorf("external name = %q, want the host it moved to", service.Spec.ExternalName)
	}
	if got := service.Annotations[k8s.AnnotationTailnetFQDN]; got != "nas.example.ts.net" {
		t.Errorf("%s = %q, want the host it moved to", k8s.AnnotationTailnetFQDN, got)
	}
	if got := service.Annotations["tailscale.com/last-applied"]; got != "keep me" {
		t.Errorf("the operator's annotation = %q, want it kept", got)
	}
	if got := service.Labels["tailscale.com/managed"]; got != "true" {
		t.Errorf("the operator's label = %q, want it kept", got)
	}
	if len(service.OwnerReferences) != 1 || service.OwnerReferences[0].Name != k8s.EgressProxyGroup {
		t.Errorf("owner references = %+v, want the operator's kept", service.OwnerReferences)
	}
	if len(service.Finalizers) != 1 || service.Finalizers[0] != "tailscale.com/cleanup" {
		t.Errorf("finalizers = %v, want the operator's kept", service.Finalizers)
	}
}

func TestTailnetCheckPassesForARightService(t *testing.T) {
	_, cluster := egressClient()
	ctx := context.Background()
	hosts := []k8s.EgressHost{masterHost()}
	if err := cluster.EnsureEgress(ctx, hosts); err != nil {
		t.Fatalf("EnsureEgress() = %v", err)
	}
	checks := cluster.EgressCheck(ctx, hosts, true)
	if len(checks) != 2 {
		t.Fatalf("%d checks, want the host and the split DNS note: %+v", len(checks), checks)
	}
	host := checks[0]
	if host.Name != "tailnet monsterdev.example.ts.net" || !host.OK {
		t.Errorf("the host check = %+v, want it passing", host)
	}
	// A cluster over a clientset has no REST config, so the split DNS is skipped
	// rather than failed, and the Service is still checked.
	dns := checks[1]
	if dns.Name != "tailnet dns" || !dns.Skipped || dns.Detail == "" {
		t.Errorf("the split DNS check = %+v, want it skipped with a reason", dns)
	}
}

func TestTailnetCheckFailsForAMissingService(t *testing.T) {
	_, cluster := egressClient()
	checks := cluster.EgressCheck(context.Background(), []k8s.EgressHost{masterHost()}, true)
	if len(checks) == 0 {
		t.Fatal("no checks at all")
	}
	host := checks[0]
	if host.OK || !strings.Contains(host.Detail, "agentbox-egress-master") {
		t.Errorf("the check = %+v, want a failure naming the missing Service", host)
	}
}

func TestTailnetCheckFailsWithoutTheOperator(t *testing.T) {
	_, cluster := egressClient()
	checks := cluster.EgressCheck(context.Background(), []k8s.EgressHost{masterHost()}, false)
	if len(checks) != 1 {
		t.Fatalf("%d checks, want the one that says the operator is missing: %+v", len(checks), checks)
	}
	check := checks[0]
	if check.Name != "tailnet" || check.OK || check.Skipped {
		t.Errorf("the check = %+v, want tailnet failing", check)
	}
	if !strings.Contains(check.Detail, "operator") || !strings.Contains(check.Detail, "--tailnet operator") {
		t.Errorf("the detail = %q, want it to say the operator is what --tailnet operator needs", check.Detail)
	}
}

func TestEgressMasterHostIsTheURLsName(t *testing.T) {
	for _, tc := range []struct {
		url  string
		host string
		ok   bool
	}{
		{"https://monsterdev.example.ts.net", "monsterdev.example.ts.net", true},
		{"https://monsterdev.example.ts.net:8443", "monsterdev.example.ts.net", true},
		{"http://host.k3d.internal:7782", "host.k3d.internal", true},
		{"", "", false},
		{"https://", "", false},
	} {
		host, ok := k8s.MasterEgressHost(tc.url)
		if ok != tc.ok || host.Host != tc.host {
			t.Errorf("%q = %+v, %v, want %q, %v", tc.url, host, ok, tc.host, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if host.Name != k8s.EgressMasterService || !reflect.DeepEqual(host.Ports, []int32{443, 22}) {
			t.Errorf("%q names %s on %v, want %s on 443 and 22", tc.url, host.Name, host.Ports, k8s.EgressMasterService)
		}
	}
}

// dnsCluster is a cluster whose API server is a stub: one egress Service, the
// operator's DNSConfig CRD, and the DNSConfigs a test hands it. The split-DNS
// check builds a dynamic client from the REST config, which a clientset alone
// can't give it, so this goes through k8s.New with a kubeconfig for the stub.
func dnsCluster(t *testing.T, service *corev1.Service, dnsConfigs ...map[string]any) *k8s.Cluster {
	t.Helper()
	items := make([]map[string]any, 0, len(dnsConfigs))
	items = append(items, dnsConfigs...)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/namespaces/agentbox/services/agentbox-egress-master", func(w http.ResponseWriter, _ *http.Request) {
		writeStub(w, service)
	})
	mux.HandleFunc("/apis/tailscale.com/v1alpha1", func(w http.ResponseWriter, _ *http.Request) {
		writeStub(w, metav1.APIResourceList{
			TypeMeta:     metav1.TypeMeta{APIVersion: "v1", Kind: "APIResourceList"},
			GroupVersion: "tailscale.com/v1alpha1",
			APIResources: []metav1.APIResource{{Name: "dnsconfigs", Kind: "DNSConfig"}},
		})
	})
	mux.HandleFunc("/apis/tailscale.com/v1alpha1/dnsconfigs", func(w http.ResponseWriter, _ *http.Request) {
		writeStub(w, map[string]any{
			"apiVersion": "tailscale.com/v1alpha1",
			"kind":       "DNSConfigList",
			"items":      items,
		})
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	cluster, err := k8s.New(k8s.Config{Name: "k3d", Namespace: "agentbox", Kubeconfig: kubeconfig(server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	return cluster
}

// dnsConfig is a DNSConfig with one split DNS declaration for domains. No
// domains is the DNSConfig scripts/k3d-up.sh --operator writes: the operator's
// nameserver, with the names forwarded by a CoreDNS stub instead.
func dnsConfig(domains ...string) map[string]any {
	return map[string]any{
		"apiVersion": "tailscale.com/v1alpha1",
		"kind":       "DNSConfig",
		"metadata":   map[string]any{"name": "ts-dns"},
		"spec": map[string]any{"splitDNS": []any{
			map[string]any{"domains": domains},
		}},
	}
}

func writeStub(w http.ResponseWriter, object any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(object); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func TestTailnetCheckMatchesTheSplitDNSDomains(t *testing.T) {
	cases := []struct {
		name    string
		domains []string
		host    string
		ok      bool
		skipped bool
		detail  string
	}{
		{
			// A wildcard is how a tailnet's own domain is declared: it covers
			// every machine in it.
			name:    "a wildcard covers a child",
			domains: []string{"*.example.ts.net"},
			host:    "monsterdev.example.ts.net",
			ok:      true,
			detail:  "split DNS",
		},
		{
			name:    "a wildcard covers a child of its own domain",
			domains: []string{"*.example.com"},
			host:    "a.example.com",
			ok:      true,
			detail:  "split DNS",
		},
		{
			// `*.example.com` names the children, not example.com itself.
			name:    "a wildcard leaves its own domain out",
			domains: []string{"*.example.com"},
			host:    "example.com",
			detail:  "can't resolve",
		},
		{
			// The documented setup: the script's DNSConfig has no split DNS,
			// because CoreDNS forwards the tailnet names instead.
			name:    "no split DNS is skipped, not failed",
			domains: nil,
			host:    "monsterdev.example.ts.net",
			skipped: true,
			detail:  "scripts/k3d-up.sh --operator",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := masterHost()
			host.Host = tc.host
			cluster := dnsCluster(t, k8s.EgressService(host, "agentbox"), dnsConfig(tc.domains...))
			checks := cluster.EgressCheck(context.Background(), []k8s.EgressHost{host}, true)
			if len(checks) != 2 {
				t.Fatalf("%d checks, want the host and the split DNS: %+v", len(checks), checks)
			}
			got := checks[0]
			if got.OK != tc.ok || got.Skipped != tc.skipped {
				t.Errorf("the host check = %+v, want ok=%v skipped=%v", got, tc.ok, tc.skipped)
			}
			if !strings.Contains(got.Detail, tc.detail) {
				t.Errorf("the host detail = %q, want it to say %q", got.Detail, tc.detail)
			}
			dns := checks[1]
			if tc.skipped {
				if !dns.Skipped || dns.OK {
					t.Errorf("the split DNS check = %+v, want it skipped too", dns)
				}
				if !strings.Contains(dns.Detail, "scripts/k3d-up.sh --operator") || !strings.Contains(dns.Detail, "CoreDNS") {
					t.Errorf("the split DNS detail = %q, want it to name the script and the stub", dns.Detail)
				}
				return
			}
			if !dns.OK {
				t.Errorf("the split DNS check = %+v, want the domains it read", dns)
			}
		})
	}
}
