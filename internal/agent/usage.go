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
	// Limits are the caps on this agent's machine, and Cores is how many cores
	// its CPU figure can add up to: its limits.cpu, or the host's cores when
	// it has none. A percentage of the host says whether the machine is busy;
	// a percentage of the agent's own limit says whether *this* agent is the
	// one hitting a wall, which is the question a capped agent raises.
	Limits Limits
	Cores  float64
}

type HostUsage struct {
	CPU       float64 // percent of all cores
	Cores     int
	MemUsed   int64
	MemTotal  int64
	PoolUsed  int64
	PoolTotal int64
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
	elapsed := time.Since(start)

	host := HostUsage{Cores: HostCores()}
	if total := hostAfter.total - hostBefore.total; total > 0 {
		host.CPU = 100 * (1 - float64(hostAfter.idle-hostBefore.idle)/float64(total))
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
		u := AgentUsage{Agent: a, State: "missing", Cores: float64(host.Cores)}
		if i, ok := current[a.Instance]; ok {
			inst := after[i]
			u.State = displayState(inst.Status)
			// `incus list --format json` already carries the configuration, so
			// every agent's limits come back with its usage, not a query each.
			u.Limits = LimitsOf(inst.ExpandedConfig)
			if cores, err := strconv.ParseFloat(u.Limits.CPU, 64); err == nil && cores > 0 {
				u.Cores = cores
			}
			if inst.State != nil {
				u.Memory = agentMemory(cgroupRoot, a.Instance, inst.State.Memory.Usage)
				u.Processes = inst.State.Processes
				if prev, ok := cpuBefore[a.Instance]; ok && inst.State.CPU.Usage >= prev {
					u.CPU = 100 * float64(inst.State.CPU.Usage-prev) / float64(elapsed.Nanoseconds())
				}
			}
		}
		usage = append(usage, u)
	}
	return host, usage, nil
}

type cpuTimes struct{ idle, total uint64 }

// HostCores is how many cores the host has, the number an agent's limits.cpu
// is carved out of.
func HostCores() int { return runtime.NumCPU() }

// HostMemory is how much memory the host has, in bytes; 0 when /proc/meminfo
// can't be read.
func HostMemory() int64 {
	total, _, err := hostMemory()
	if err != nil {
		return 0
	}
	return total
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
// lxc.payload.<instance> under it.
const cgroupRoot = "/sys/fs/cgroup"

// agentMemory returns the memory an agent uses the way the host's figure
// counts it, and `free` inside the agent: without the page cache and the
// kernel's reclaimable caches. Incus reports the cgroup's memory.current, which
// includes them, so an agent that had built a project or read a large
// repository showed about twice the memory it needed. It returns fallback when
// the cgroup can't be read (cgroup v1, or another layout).
func agentMemory(root, instance string, fallback int64) int64 {
	dir := filepath.Join(root, "lxc.payload."+instance)
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
