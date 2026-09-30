package agent

import (
	"os"
	"path/filepath"
	"time"
)

// CPUTime is how much CPU time an agent's machine has used since it started,
// read from its cgroup's cpu.stat rather than asked of Incus: it answers when
// incusd doesn't, which is when the daemon's stall watch needs it most. ok is
// false when the cgroup can't be read: the machine isn't running, or this
// isn't cgroup v2.
func CPUTime(instance string) (time.Duration, bool) {
	return cgroupCPU(agentCgroup(cgroupRoot, instance))
}

// cgroupCPU reads usage_usec out of dir's cpu.stat. A frozen cgroup's stops
// moving, which is how a locked-up machine shows.
func cgroupCPU(dir string) (time.Duration, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return 0, false
	}
	return time.Duration(statValue(string(b), "usage_usec")) * time.Microsecond, true
}
