package daemon

import (
	"context"
	"runtime"
	"testing"
)

// TestMemoryUsageAPI checks that GET /v1/usage/memory is wired up and
// answers with valid JSON, one row per agent; the figures themselves are
// agent.Manager.MemoryUsage's own tests.
func TestMemoryUsageAPI(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)

	usage, err := d.client.MemoryUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.HostTotal <= 0 {
		t.Errorf("HostTotal = %d, want > 0", usage.HostTotal)
	}
	if len(usage.Agents) != 1 || usage.Agents[0].Ref != a.Ref() {
		t.Errorf("Agents = %+v, want one row for %s", usage.Agents, a.Ref())
	}
}

// TestHostMemoryAPI: GET /v1/usage/host is this machine's /proc/meminfo and
// CPUs, answered without Incus, which here fails every call.
func TestHostMemoryAPI(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), "exit 1\n")
	mem, err := d.client.HostMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mem.Total <= 0 || mem.Available <= 0 || mem.Available > mem.Total || mem.CPUs != runtime.NumCPU() {
		t.Fatalf("HostMemory = %+v", mem)
	}
}
