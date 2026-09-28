package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// TestMemoryShortageReachesTheAPI checks the daemon's side of the thrash
// warning: an agent whose cgroup ThrashWatch finds thrashing is described
// with a MemoryShortage, raise included, and its agent event says so. What
// counts as thrashing is agent.ThrashWatch's own tests.
func TestMemoryShortageReachesTheAPI(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}
	// Made, not Run: its own ticker would sample the same fake cgroup at real
	// times in between the test's.
	s, err := New(Config{Paths: p, Incus: incus.Client{Bin: "/bin/false"}, User: image.User{Name: "dev", UID: 1000, GID: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.store.Close() })
	cgroups := filepath.Join(root, "cgroup")
	s.thrash.Root = cgroups

	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01"}
	dir := filepath.Join(cgroups, "lxc.payload."+a.Instance)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const limit = 4 << 30
	ctx := context.Background()
	at := time.Now()
	for i := range 7 {
		n := int64(i) * 100_000
		for name, body := range map[string]string{
			"memory.pressure": "some avg10=50.00 avg60=45.00 avg300=10.00 total=1\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
			"memory.stat":     fmt.Sprintf("workingset_refault_file %d\n", n),
			"memory.events":   fmt.Sprintf("high 0\nmax %d\n", n),
			"io.stat":         fmt.Sprintf("259:4 rbytes=%d wbytes=0\n", n*4096),
			"memory.max":      fmt.Sprintf("%d\n", limit),
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		s.thrash.Sample(ctx, at)
		at = at.Add(agent.ThrashInterval)
	}

	info := s.agentInfo(agent.Status{Agent: a, State: "running"})
	short := info.MemoryShortage
	if short == nil {
		t.Fatal("no MemoryShortage for a thrashing agent")
	}
	if short.Limit != limit || short.Pressure != 45 || short.RefaultRate <= 0 || short.InBudget {
		t.Errorf("MemoryShortage = %+v", short)
	}
	if want := s.manager(nil).MemoryRaise(ctx, a.Instance, limit); short.RaiseTo != want {
		t.Errorf("RaiseTo = %q, want %q", short.RaiseTo, want)
	}

	events, unsubscribe := s.events.subscribe()
	defer unsubscribe()
	s.publishAgentChanges([]agent.Status{{Agent: a, State: "running"}})
	select {
	case ev := <-events:
		var change api.AgentChange
		if err := json.Unmarshal(ev.Data, &change); err != nil {
			t.Fatal(err)
		}
		if ev.Type != api.EventAgent || !change.ShortOfMemory {
			t.Errorf("event = %s %+v, want the agent short of memory", ev.Type, change)
		}
	case <-time.After(time.Second):
		t.Fatal("no agent event")
	}

	// Another agent, not thrashing, has none.
	if other := s.agentInfo(agent.Status{Agent: state.Agent{Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02"}}); other.MemoryShortage != nil {
		t.Errorf("an agent that isn't thrashing has %+v", other.MemoryShortage)
	}
}
