package daemon

import (
	"context"
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
