package k8s_test

import (
	"slices"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"

	"agentbox/internal/k8s"
)

// TestReaderRoleCanReadTheSplitDNS is the split-DNS half of `cluster doctor`: a
// DNSConfig is cluster-scoped, so the namespace's Role can't grant it and the
// read-only ClusterRole has to.
func TestRBACReaderRoleCanReadTheSplitDNS(t *testing.T) {
	access := k8s.NewAccess("agentbox")
	var found *rbacv1.PolicyRule
	for i, rule := range access.Reader.Rules {
		if slices.Contains(rule.APIGroups, "tailscale.com") && slices.Contains(rule.Resources, "dnsconfigs") {
			found = &access.Reader.Rules[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the read-only ClusterRole can't read DNSConfigs: %+v", access.Reader.Rules)
	}
	for _, verb := range []string{"get", "list", "watch"} {
		if !slices.Contains(found.Verbs, verb) {
			t.Errorf("the dnsconfigs rule = %+v, want it to allow %s", found, verb)
		}
	}
	for _, verb := range []string{"create", "update", "delete", "patch"} {
		if slices.Contains(found.Verbs, verb) {
			t.Errorf("the dnsconfigs rule = %+v, want it read only", found)
		}
	}
	// The namespace's Role is the runtime's, and the doctor's cluster-scoped
	// reads don't belong in it.
	for _, rule := range access.Role.Rules {
		if slices.Contains(rule.Resources, "dnsconfigs") {
			t.Errorf("the namespace Role names dnsconfigs, which is cluster-scoped: %+v", rule)
		}
	}
}

// TestRoleCanApplyTheAgentsNetworkPolicy is the policy that keeps anything in
// the namespace from reaching an agent's pod: the runtime applies it, so the
// namespace's Role has to allow it, and only what applying it needs.
func TestRoleCanApplyTheAgentsNetworkPolicy(t *testing.T) {
	access := k8s.NewAccess("agentbox")
	var found *rbacv1.PolicyRule
	for i, rule := range access.Role.Rules {
		if slices.Contains(rule.APIGroups, "networking.k8s.io") && slices.Contains(rule.Resources, "networkpolicies") {
			found = &access.Role.Rules[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the namespace Role can't apply network policies: %+v", access.Role.Rules)
	}
	if !slices.Equal(found.Resources, []string{"networkpolicies"}) {
		t.Errorf("the rule = %+v, want networkpolicies and nothing else", found)
	}
	want := []string{"create", "get", "update"}
	got := slices.Clone(found.Verbs)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the rule = %+v, want exactly %v: nothing removes a policy, and nothing lists one", found, want)
	}
}

// TestRoleCanReadTheNamespacesEvents is the cluster page's half: it lists the
// events that explain a pod that didn't start, so the Role has to allow reading
// them as well as writing them.
func TestRoleCanReadTheNamespacesEvents(t *testing.T) {
	access := k8s.NewAccess("agentbox")
	var found *rbacv1.PolicyRule
	for i, rule := range access.Role.Rules {
		if slices.Contains(rule.APIGroups, "") && slices.Contains(rule.Resources, "events") {
			found = &access.Role.Rules[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the namespace Role doesn't name events: %+v", access.Role.Rules)
	}
	for _, verb := range []string{"get", "list", "create", "patch"} {
		if !slices.Contains(found.Verbs, verb) {
			t.Errorf("the events rule = %+v, want it to allow %s", found, verb)
		}
	}
}
