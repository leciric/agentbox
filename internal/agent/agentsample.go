package agent

import (
	"time"
)

// AgentCounters is one reading of an agent's cgroup, for the daemon's live
// resource sampler (internal/daemon/agentresources.go): CPU time and bytes
// read and written are counters it takes deltas of between two readings,
// memory and swap are what the cgroup holds now. Reading it costs a handful
// of small cgroup files and no call to Incus, so it can be taken every few
// seconds while someone watches the agent.
type AgentCounters struct {
	At        time.Time
	CPU       time.Duration // cpu.stat's usage_usec
	Memory    int64         // counted the way agentMemory does, without caches
	Swap      int64
	DiskRead  uint64 // bytes, physical disks only (cgroupIO)
	DiskWrite uint64
}

// ReadAgentCounters reads an agent's machine's cgroup. ok is false when it
// can't be read: the machine isn't running, or this isn't cgroup v2.
func ReadAgentCounters(instance string) (AgentCounters, bool) {
	return readAgentCounters(cgroupRoot, sysRoot, instance, time.Now())
}

func readAgentCounters(root, sys, instance string, now time.Time) (AgentCounters, bool) {
	dir := agentCgroup(root, instance)
	cpu, ok := cgroupCPU(dir)
	if !ok {
		return AgentCounters{}, false
	}
	c := AgentCounters{At: now, CPU: cpu, Memory: agentMemory(root, instance, 0), Swap: agentSwap(root, instance)}
	if io, ok := cgroupIO(dir, sys); ok {
		c.DiskRead, c.DiskWrite = io.read, io.write
	}
	return c, true
}

// AgentRates is what an agent used between two readings: CPU in percent of
// one core (100 is a full core), disk in bytes a second, and memory and swap
// as of the later reading.
type AgentRates struct {
	CPU       float64
	Memory    int64
	Swap      int64
	DiskRead  int64
	DiskWrite int64
}

// Rates is what an agent used from before to after. A counter that went
// backwards (the machine restarted, its cgroup recreated) gives 0 for that
// interval rather than a negative or huge number.
func Rates(before, after AgentCounters) AgentRates {
	out := AgentRates{Memory: after.Memory, Swap: after.Swap}
	seconds := after.At.Sub(before.At).Seconds()
	if seconds <= 0 {
		return out
	}
	if after.CPU >= before.CPU {
		out.CPU = float64(after.CPU-before.CPU) / float64(time.Second) / seconds * 100
	}
	out.DiskRead, out.DiskWrite = ioRate(ioBytes{before.DiskRead, before.DiskWrite}, ioBytes{after.DiskRead, after.DiskWrite}, seconds)
	return out
}
