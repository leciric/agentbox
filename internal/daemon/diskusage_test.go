package daemon

import (
	"context"
	"testing"
)

// TestDiskUsageAPI checks that GET /v1/usage/disk is wired up and answers
// with valid JSON; the categories themselves are agent.Manager.DiskUsage's
// own tests.
func TestDiskUsageAPI(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	addTestAgent(t, d)

	usage, err := d.client.DiskUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Total < 0 {
		t.Errorf("Total = %d, want >= 0", usage.Total)
	}
	wantLabels := map[string]bool{
		"Agent machines": false, "Base images and saved bases": false,
		"Worktrees": false, "Media": false, "state.db and logs": false,
	}
	for _, c := range usage.Categories {
		if _, ok := wantLabels[c.Label]; !ok {
			t.Errorf("unexpected category %q", c.Label)
			continue
		}
		wantLabels[c.Label] = true
	}
	for label, seen := range wantLabels {
		if !seen {
			t.Errorf("missing category %q", label)
		}
	}
}
