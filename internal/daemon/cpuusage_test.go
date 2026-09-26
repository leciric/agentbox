package daemon

import (
	"context"
	"testing"
	"time"
)

// TestCPUUsageAPI checks that GET /v1/usage/cpu is wired up and answers with
// valid JSON, one row per agent; the figures themselves are
// agent.Manager.CPUUsage's own tests (which are Usage's, reshaped).
func TestCPUUsageAPI(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), `case "$1" in
  list) echo '[{"name": "ab-hello-stack-agent-01", "status": "Running", "state": {"network": {"eth0": {"addresses": [{"family": "inet", "address": "10.1.2.3"}]}}}}]' ;;
  query) echo '{"space":{"used":500,"total":1000}}' ;;
esac`)
	ctx := context.Background()
	a := addTestAgent(t, d)

	usage, err := d.client.CPUUsage(ctx, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if usage.HostCores <= 0 {
		t.Errorf("HostCores = %d, want > 0", usage.HostCores)
	}
	if len(usage.Agents) != 1 || usage.Agents[0].Ref != a.Ref() {
		t.Errorf("Agents = %+v, want one row for %s", usage.Agents, a.Ref())
	}
}
