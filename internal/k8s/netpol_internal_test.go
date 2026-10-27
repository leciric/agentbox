package k8s

import (
	"context"
	"errors"
	"net"
	"testing"
)

// TestNetPolNamesAResolvedMaster is the branch a cluster whose master URL is a
// name takes: the daemon resolves it and the policy names the address. The
// resolver is stubbed because the point is what the policy does with an answer,
// not whether this machine has DNS.
func TestNetPolNamesAResolvedMaster(t *testing.T) {
	previous := lookupIP
	lookupIP = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.8.8.1")}, nil
	}
	t.Cleanup(func() { lookupIP = previous })

	policy := NetPol("agentbox", "https://master.example.test:8443")
	if len(policy.Spec.Egress) != 3 {
		t.Fatalf("egress rules = %+v, want DNS, the master and every address", policy.Spec.Egress)
	}
	master := policy.Spec.Egress[1]
	if len(master.To) != 1 || master.To[0].IPBlock == nil || master.To[0].IPBlock.CIDR != "10.8.8.1/32" {
		t.Errorf("the master peer = %+v, want the address it resolved to", master.To)
	}
	if len(master.Ports) != 1 || master.Ports[0].Port == nil || master.Ports[0].Port.IntVal != 8443 {
		t.Errorf("the master ports = %+v, want 8443", master.Ports)
	}
}

// TestNetPolLeavesOutAMasterTheResolverRefuses is the other side of that
// branch: an error from the resolver is not a failure to apply a policy.
func TestNetPolLeavesOutAMasterTheResolverRefuses(t *testing.T) {
	previous := lookupIP
	lookupIP = func(context.Context, string) ([]net.IP, error) {
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupIP = previous })

	policy := NetPol("agentbox", "https://master.example.test:8443")
	if len(policy.Spec.Egress) != 2 {
		t.Fatalf("egress rules = %+v, want DNS and every address", policy.Spec.Egress)
	}
}
