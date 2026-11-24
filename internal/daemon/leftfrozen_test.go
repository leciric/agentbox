package daemon

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"agentbox/internal/state"
)

// fakeMachineCgroups is the agents' machines' cgroups as a test sees them:
// every machine running, frozen while in frozen, and its heavy commands'
// cgroups frozen as many as runs says.
type fakeMachineCgroups struct {
	mu     sync.Mutex
	frozen map[string]bool
	runs   map[string]int
	thawed []string
}

func (f *fakeMachineCgroups) Running(instance string) (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return true, f.frozen[instance]
}

func (f *fakeMachineCgroups) Thaw(instance string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.frozen, instance)
	f.thawed = append(f.thawed, instance)
	return nil
}

func (f *fakeMachineCgroups) ThawRuns(instance string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.runs[instance]
	delete(f.runs, instance)
	return n, nil
}

// An install stuck with agents frozen for memory, by the experimental "Pause
// whole agents for memory" that is gone, recovers by upgrading: the daemon
// thaws, as it starts, the machines a daemon before it froze and the heavy
// commands it paused, whatever agent they are in, and forgets the list.
func TestTheDaemonThawsWhatWasLeftFrozenForMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mc := &fakeMachineCgroups{
		frozen: map[string]bool{"ab-p-agent-01": true},
		runs:   map[string]int{"ab-p-agent-02": 2},
	}
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{setup: func(s *Server) {
		s.machineCgroups = mc
		if err := s.store.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"agent-01", "agent-02"} {
			a := state.Agent{Project: "p", Name: name, Instance: "ab-p-" + name, AI: "none", Status: state.AgentReady, CreatedAt: time.Now()}
			if err := s.store.AddAgent(ctx, a); err != nil {
				t.Fatal(err)
			}
		}
		// What the old daemon saved as it stopped: one of the two, and one
		// since removed.
		if err := s.store.SetSetting(ctx, state.SettingAgentFreezeFrozen, `["p/agent-01","p/agent-09"]`); err != nil {
			t.Fatal(err)
		}
	}})
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, kept, err := d.srv.store.SettingValue(ctx, state.SettingAgentFreezeFrozen)
		if err != nil {
			t.Fatal(err)
		}
		if !kept {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the agents frozen for memory are still listed after the daemon started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if !slices.Equal(mc.thawed, []string{"ab-p-agent-01"}) {
		t.Errorf("thawed %q, want agent-01's machine", mc.thawed)
	}
	if len(mc.frozen) != 0 || len(mc.runs) != 0 {
		t.Errorf("still frozen: machines %v, heavy commands %v", mc.frozen, mc.runs)
	}
}
