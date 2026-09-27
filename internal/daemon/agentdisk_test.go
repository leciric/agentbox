package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// The cache measures an agent once per TTL, and once for any number of
// requests that arrive while it is measuring.
func TestAgentDiskCache(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	release := make(chan struct{})
	c := newAgentDiskCache(func(ctx context.Context, a state.Agent) agent.AgentDisk {
		<-release
		return agent.AgentDisk{Machine: int64(calls.Add(1)) * 100, Worktree: 7}
	})
	now := time.Now()
	var mu sync.Mutex
	c.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	a := state.Agent{Project: "p", Name: "agent-01"}
	ctx := context.Background()

	// Five cards hovered at once: one measurement.
	var wg sync.WaitGroup
	got := make([]int64, 5)
	for i := range got {
		wg.Go(func() {
			d, _ := c.get(ctx, a)
			got[i] = d.Machine
		})
	}
	waitFor(t, "the first measurement to start", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.entries[a.Ref()] != nil
	})
	close(release)
	wg.Wait()
	for i, m := range got {
		if m != 100 {
			t.Errorf("request %d got machine %d, want 100 (the one shared measurement)", i, m)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("measured %d times for concurrent requests, want 1", n)
	}

	// Within the TTL, the cached answer...
	mu.Lock()
	now = now.Add(agentDiskTTL - time.Second)
	mu.Unlock()
	if d, _ := c.get(ctx, a); d.Machine != 100 {
		t.Errorf("within the TTL, machine = %d, want the cached 100", d.Machine)
	}
	// ...and a fresh one after it.
	mu.Lock()
	now = now.Add(time.Second)
	mu.Unlock()
	if d, _ := c.get(ctx, a); d.Machine != 200 {
		t.Errorf("after the TTL, machine = %d, want a new measurement (200)", d.Machine)
	}

	c.forget(a.Ref())
	if d, _ := c.get(ctx, a); d.Machine != 300 {
		t.Errorf("after forget, machine = %d, want a new measurement (300)", d.Machine)
	}
}

// TestAgentDiskAPI checks the route is wired up: the test agent's worktree
// doesn't exist, so its size is left out rather than failing the request.
func TestAgentDiskAPI(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	addTestAgent(t, d)

	disk, err := d.client.AgentDisk(context.Background(), "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Worktree != nil {
		t.Errorf("Worktree = %d for a worktree that doesn't exist, want it left out", *disk.Worktree)
	}
	if disk.MeasuredAt.IsZero() {
		t.Error("MeasuredAt is zero")
	}
}
