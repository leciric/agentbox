package chv

import (
	"testing"
	"time"

	"agentbox/internal/api"
)

// The agent stopped for the host is the running or paused one holding the
// most; stopped ones hold nothing.
func TestBiggestAgent(t *testing.T) {
	agents := []api.MemoryUsageAgent{
		{Ref: "p/small", State: "running", Memory: GiB},
		{Ref: "p/stopped", State: "stopped", Memory: 9 * GiB},
		{Ref: "p/paused", State: "paused", Memory: 3 * GiB},
		{Ref: "q/big", State: "running", Memory: 2 * GiB},
	}
	if a, ok := biggestAgent(agents); !ok || a.Ref != "p/paused" {
		t.Errorf("biggest = %+v, %v; want p/paused", a, ok)
	}
	if _, ok := biggestAgent([]api.MemoryUsageAgent{{Ref: "p/a", State: "stopped", Memory: GiB}}); ok {
		t.Error("picked a stopped agent")
	}
}

// What hostload judged reaches the memory loop while fresh, for memory and
// the disk only, through its calm-down; and the daemon hears the ceiling and
// whether boots wait once the loop has a sample.
func TestSupervisorHostLoadForMemory(t *testing.T) {
	var s supervisor
	now := time.Now()
	s.noteHostLoad(api.HostLoad{Busy: true, Why: "memory", At: now})
	if got := s.hostBusyFor(now); got != "memory" {
		t.Errorf("busy for memory: %q", got)
	}
	// Calming down: Why is "", the reason holds.
	s.noteHostLoad(api.HostLoad{Busy: true, At: now})
	if got := s.hostBusyFor(now); got != "memory" {
		t.Errorf("calming down: %q", got)
	}
	if got := s.hostBusyFor(now.Add(time.Minute)); got != "" {
		t.Errorf("a stale reading: %q", got)
	}
	s.noteHostLoad(api.HostLoad{Busy: true, Why: "cpu", At: now})
	if got := s.hostBusyFor(now); got != "" {
		t.Errorf("busy for the CPU: %q, want nothing", got)
	}
	s.noteHostLoad(api.HostLoad{Busy: false, At: now})
	if got := s.hostBusyFor(now); got != "" {
		t.Errorf("calm: %q", got)
	}

	if l := s.withMemory(api.HostLoad{}); l.VMCeiling != 0 || l.HoldBoots {
		t.Errorf("before a sample: %+v", l)
	}
	s.sample = &memSample{}
	s.mem.Ceiling, s.mem.HoldBoots = 12*GiB, true
	if l := s.withMemory(api.HostLoad{}); l.VMCeiling != 12*GiB || !l.HoldBoots {
		t.Errorf("with a sample: %+v", l)
	}
}
