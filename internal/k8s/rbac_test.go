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
