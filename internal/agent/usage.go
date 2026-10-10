package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/state"
)

type AgentUsage struct {
	state.Agent
	State     string
	CPU       float64 // percent; 100 is one full core
	Memory    int64   // bytes in use, without caches the kernel frees when it needs memory
	Processes int64
	// DiskRead and DiskWrite are what it read from and wrote to the host's
	// disks over the sample, in bytes a second, from its cgroup's io.stat.
	DiskRead  int64
	DiskWrite int64
}

type HostUsage struct {
	CPU       float64 // percent of all cores
	Cores     int
	MemUsed   int64
	MemTotal  int64
	PoolUsed  int64
	PoolTotal int64
	// DiskRead and DiskWrite are what the host's physical disks read and
	// wrote over the sample, in bytes a second.
	DiskRead  int64
	DiskWrite int64
	// Pressure is nil when the kernel doesn't keep PSI.
	Pressure *Pressure
}

// Usage samples the host and every agent twice, interval apart, to measure CPU use.
func (m *Manager) Usage(ctx context.Context, interval time.Duration) (HostUsage, []AgentUsage, error) {
	agents, err := m.Store.Agents(ctx, "")
	if err != nil {
		return HostUsage{}, nil, err
	}
	hostBefore, err := hostCPU()
	if err != nil {
		return HostUsage{}, nil, err
	}
	// Disk IO is a rate the same way CPU is: counters read on both sides of
	// the interval. A host or agent whose counters can't be read shows none.
	diskBefore, diskErr := hostDiskIO("/proc/diskstats", sysRoot)
	ioBefore := map[string]ioBytes{}
	for _, a := range agents {
		if io, ok := cgroupIO(agentCgroup(cgroupRoot, a.Instance), sysRoot); ok {
			ioBefore[a.Instance] = io
		}
	}
	before, err := m.Incus.Instances(ctx)
	if err != nil {
		return HostUsage{}, nil, err
	}
	start := time.Now()
	select {
	case <-ctx.Done():
		return HostUsage{}, nil, ctx.Err()
	case <-time.After(interval):
	}
	after, err := m.Incus.Instances(ctx)
	if err != nil {
		return HostUsage{}, nil, err
	}
	hostAfter, err := hostCPU()
	if err != nil {
		return HostUsage{}, nil, err
	}
	diskAfter, diskAfterErr := hostDiskIO("/proc/diskstats", sysRoot)
	elapsed := time.Since(start)

	host := HostUsage{Cores: HostCores()}
	if total := hostAfter.total - hostBefore.total; total > 0 {
		host.CPU = 100 * (1 - float64(hostAfter.idle-hostBefore.idle)/float64(total))
	}
	if diskErr == nil && diskAfterErr == nil {
		host.DiskRead, host.DiskWrite = ioRate(diskBefore, diskAfter, elapsed.Seconds())
	}
	if p, ok := hostPressure("/proc/pressure"); ok {
		host.Pressure = &p
	}
	if host.MemTotal, host.MemUsed, err = hostMemory(); err != nil {
		return HostUsage{}, nil, err
	}
	if host.PoolUsed, host.PoolTotal, err = m.Incus.PoolSpace(ctx, "default"); err != nil {
		return HostUsage{}, nil, err
	}

	cpuBefore := map[string]int64{}
	for _, inst := range before {
		if inst.State != nil {
			cpuBefore[inst.Name] = inst.State.CPU.Usage
		}
	}
	current := map[string]int{}
	for i, inst := range after {
		current[inst.Name] = i
	}
	usage := make([]AgentUsage, 0, len(agents))
	for _, a := range agents {
		u := AgentUsage{Agent: a, State: "missing"}
		if i, ok := current[a.Instance]; ok {
			inst := after[i]
			u.State = displayState(inst.Status)
			if inst.State != nil {
				u.Memory = agentMemory(cgroupRoot, a.Instance, inst.State.Memory.Usage)
				u.Processes = inst.State.Processes
				if prev, ok := cpuBefore[a.Instance]; ok && inst.State.CPU.Usage >= prev {
					u.CPU = 100 * float64(inst.State.CPU.Usage-prev) / float64(elapsed.Nanoseconds())
				}
				if prev, ok := ioBefore[a.Instance]; ok {
					if io, ok := cgroupIO(agentCgroup(cgroupRoot, a.Instance), sysRoot); ok {
						u.DiskRead, u.DiskWrite = ioRate(prev, io, elapsed.Seconds())
					}
				}
			}
		}
		usage = append(usage, u)
	}
	return host, usage, nil
}

type cpuTimes struct{ idle, total uint64 }

// HostCores is how many cores the host has.
func HostCores() int { return runtime.NumCPU() }

// VMMemoryCapEnv is set by a Linux front end (package hostvm) on everything
// it runs in its Cloud Hypervisor VM: the most memory, in bytes, the VM may be
// given. The VM boots small and is granted memory with virtio-mem as agents
// need it, so its /proc/meminfo only says what it has been granted so far.
const VMMemoryCapEnv = "AGENTBOX_VM_MEMORY_CAP"

// HostMemory is how much memory the host has for agents, in bytes: what the
// limits and budgets computed from it are shares of. It is /proc/meminfo's
// total, or in a Cloud Hypervisor VM the cap it may grow to
// (VMMemoryCapEnv); 0 when neither can be read. What is in use right now
// (Usage, MemoryUsage) reads /proc/meminfo itself: the memory granted so far.
func HostMemory() int64 {
	if n, err := strconv.ParseInt(os.Getenv(VMMemoryCapEnv), 10, 64); err == nil && n > 0 {
		return n
	}
	total, _, err := hostMemory()
	if err != nil {
		return 0
	}
	return total
}

// MemAvailable is the guest's real spare memory right now: /proc/meminfo's
// MemAvailable, what could be given out without swapping, the same figure
// the host's Cloud Hypervisor supervisor grows the VM ahead of (package
// hostvm/chv). 0 when /proc/meminfo can't be read.
func MemAvailable() int64 {
	total, used, err := hostMemory()
	if err != nil {
		return 0
	}
	return total - used
}

// hostCPU reads the aggregate CPU times from /proc/stat.
func hostCPU() (cpuTimes, error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, err
	}
	line, _, _ := strings.Cut(string(b), "\n")
	fields := strings.Fields(line) // cpu user nice system idle iowait irq softirq steal ...
	if len(fields) < 9 || fields[0] != "cpu" {
		return cpuTimes{}, fmt.Errorf("unexpected /proc/stat line %q", line)
	}
	var t cpuTimes
	for i, f := range fields[1:9] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return cpuTimes{}, err
		}
		t.total += v
		if i == 3 || i == 4 { // idle, iowait
			t.idle += v
		}
	}
	return t, nil
}

// cgroupRoot is where cgroup2 is mounted. Incus runs each container in
// lxc.payload.<instance> under it, or, until it restarts, in
// agentbox/<instance> inside an earlier release's shared budget (agentCgroup).
const cgroupRoot = "/sys/fs/cgroup"

// agentCgroup is the directory of an agent's cgroup: lxc.payload.<instance>
// at the root, or inside the shared budget's parent for a machine an earlier
// release started there and nothing has restarted since.
func agentCgroup(root, instance string) string {
	inside := filepath.Join(root, oldBudgetCgroup, instance)
	if _, err := os.Stat(inside); err == nil {
		return inside
	}
	return filepath.Join(root, "lxc.payload."+instance)
}

// agentMemory returns the memory an agent uses the way the host's figure
// counts it, and `free` inside the agent: without the page cache and the
// kernel's reclaimable caches. Incus reports the cgroup's memory.current, which
// includes them, so an agent that had built a project or read a large
// repository showed about twice the memory it needed. It returns fallback when
// the cgroup can't be read (cgroup v1, or another layout).
func agentMemory(root, instance string, fallback int64) int64 {
	dir := agentCgroup(root, instance)
	current, err := os.ReadFile(filepath.Join(dir, "memory.current"))
	if err != nil {
		return fallback
	}
	used, err := strconv.ParseInt(strings.TrimSpace(string(current)), 10, 64)
	if err != nil {
		return fallback
	}
	stat, err := os.ReadFile(filepath.Join(dir, "memory.stat"))
	if err != nil {
		return fallback
	}
	for _, line := range strings.Split(string(stat), "\n") {
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "active_file", "inactive_file", "slab_reclaimable":
			n, _ := strconv.ParseInt(value, 10, 64)
			used -= n
		}
	}
	return max(used, 0)
}

func hostMemory() (total, used int64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	var available int64
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			available = kb * 1024
		}
	}
	return total, total - available, s.Err()
}

// statValue reads one "key value" line out of a flat-keyed cgroup file such
// as memory.stat or cpu.stat.
func statValue(file, key string) int64 {
	for _, line := range strings.Split(file, "\n") {
		k, v, ok := strings.Cut(line, " ")
		if ok && k == key {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}
