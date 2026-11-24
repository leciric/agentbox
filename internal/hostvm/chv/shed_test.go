package chv

import (
	"testing"

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

// The daemon hears the ceiling and whether boots wait once the memory loop
// has a sample.
func TestSupervisorHostLoadForMemory(t *testing.T) {
	var s supervisor
	if l := s.withMemory(api.HostLoad{}); l.VMCeiling != 0 || l.HoldBoots {
		t.Errorf("before a sample: %+v", l)
	}
	s.sample = &memSample{}
	s.mem.Ceiling, s.mem.HoldBoots = 12*GiB, true
	if l := s.withMemory(api.HostLoad{}); l.VMCeiling != 12*GiB || !l.HoldBoots {
		t.Errorf("with a sample: %+v", l)
	}
}
