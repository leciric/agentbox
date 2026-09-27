package agent

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// MemoryUsageAgent is one agent's share of the host's memory: what its own
// cgroup holds, running or paused — a paused agent (Incus "Frozen") still
// holds every page it had, which is why State is carried alongside Memory
// and Swap rather than left for the caller to look up.
type MemoryUsageAgent struct {
	Ref    string
	Title  string
	State  string
	Memory int64 // RAM in bytes, counted the way agentMemory does
	Swap   int64 // bytes in memory.swap.current; 0 for a stopped agent
	Limit  int64 // limits.memory in bytes; 0 is no limit
}

// MemoryUsage is the host's memory, broken down the way the "Host memory"
// popover shows it: every agent, largest first, and what's left once they're
// accounted for.
type MemoryUsage struct {
	HostTotal  int64
	AgentsUsed int64 // RAM held by every agent's cgroup, running or paused
	OtherUsed  int64 // the host's own share: everything else in use
	SwapTotal  int64
	SwapUsed   int64 // swap in use across the whole host
	AgentsSwap int64 // of that, what agents' own cgroups hold
	// Zram is set when the host's swap is zram: what its devices hold looks
	// like SwapUsed, but is compressed, so it costs less RAM than it appears
	// to. nil when there is no zram device, or none of them are sized.
	Zram   *ZramUsage
	Agents []MemoryUsageAgent
}

// ZramUsage is what a host's zram swap devices report: SwapBytes is the
// logical swap they hold — what a cgroup's memory.swap.current counts
// against — and RealBytes is what that actually costs in RAM once
// compressed, from mm_stat's mem_used_total.
type ZramUsage struct {
	SwapBytes int64
	RealBytes int64
}

// MemoryUsage measures the host's memory and every agent's share of it. It
// is meant to be called when asked, not kept warm on a poll, the same as
// DiskUsage: it reads a cgroup file per agent and every zram device.
func (m *Manager) MemoryUsage(ctx context.Context) (MemoryUsage, error) {
	agents, err := m.Store.Agents(ctx, "")
	if err != nil {
		return MemoryUsage{}, err
	}
	instances, err := m.Incus.Instances(ctx)
	if err != nil {
		return MemoryUsage{}, err
	}
	byName := make(map[string]int, len(instances))
	for i, inst := range instances {
		byName[inst.Name] = i
	}

	hostTotal, hostUsed, err := hostMemory()
	if err != nil {
		return MemoryUsage{}, err
	}
	swapTotal, swapUsed, err := hostSwap()
	if err != nil {
		return MemoryUsage{}, err
	}

	rows := make([]MemoryUsageAgent, 0, len(agents))
	var agentsUsed, agentsSwap int64
	for _, a := range agents {
		row := MemoryUsageAgent{Ref: a.Ref(), Title: a.Title, State: "stopped"}
		if i, ok := byName[a.Instance]; ok {
			inst := instances[i]
			row.State = displayState(inst.Status)
			row.Limit = limitBytes(LimitsOf(inst.ExpandedConfig).Memory, hostTotal)
			var fallback int64
			if inst.State != nil {
				fallback = inst.State.Memory.Usage
			}
			row.Memory = agentMemory(cgroupRoot, a.Instance, fallback)
			row.Swap = agentSwap(cgroupRoot, a.Instance)
		}
		agentsUsed += row.Memory
		agentsSwap += row.Swap
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Memory+rows[i].Swap > rows[j].Memory+rows[j].Swap })

	out := MemoryUsage{
		HostTotal:  hostTotal,
		AgentsUsed: agentsUsed,
		OtherUsed:  max(hostUsed-agentsUsed, 0),
		SwapTotal:  swapTotal,
		SwapUsed:   swapUsed,
		AgentsSwap: agentsSwap,
		Agents:     rows,
	}
	if z, ok := zramTotal(zramRoot); ok {
		out.Zram = &z
	}
	return out, nil
}

// limitBytes resolves a limits.memory value to bytes: a plain size through
// ParseBytes, a percentage against hostTotal, or 0 for "" (no limit) and for
// anything it can't parse.
func limitBytes(memory string, hostTotal int64) int64 {
	if memory == "" {
		return 0
	}
	if percent, ok := strings.CutSuffix(memory, "%"); ok {
		n, err := strconv.ParseFloat(strings.TrimSpace(percent), 64)
		if err != nil || hostTotal <= 0 {
			return 0
		}
		return int64(float64(hostTotal) * n / 100)
	}
	n, err := ParseBytes(memory)
	if err != nil {
		return 0
	}
	return n
}

// agentSwap reads how much swap an agent's cgroup holds, the way agentMemory
// reads memory.current: 0 for a stopped agent, whose cgroup no longer exists.
func agentSwap(root, instance string) int64 {
	b, err := os.ReadFile(filepath.Join(agentCgroup(root, instance), "memory.swap.current"))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// hostSwap reads the host's total and in-use swap from /proc/meminfo.
func hostSwap() (total, used int64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	var free int64
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "SwapTotal:":
			total = kb * 1024
		case "SwapFree:":
			free = kb * 1024
		}
	}
	return total, total - free, s.Err()
}

// zramRoot is where zram block devices appear, one directory per device
// (zram0, zram1, ...), when the kernel's zram module is loaded.
const zramRoot = "/sys/block"

// zramTotal reads every zram device under root and reports whether any is
// actually sized: a loaded zram module can carry devices with disksize 0,
// which zramctl skips too, since nothing backs swap on them. mm_stat's
// fields are space-separated integers: orig_data_size, compr_data_size,
// mem_used_total, and more this doesn't need.
func zramTotal(root string) (ZramUsage, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ZramUsage{}, false
	}
	var out ZramUsage
	found := false
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "zram") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		disksize, err := readInt(filepath.Join(dir, "disksize"))
		if err != nil || disksize <= 0 {
			continue
		}
		mmStat, err := os.ReadFile(filepath.Join(dir, "mm_stat"))
		if err != nil {
			continue
		}
		fields := strings.Fields(string(mmStat))
		if len(fields) < 3 {
			continue
		}
		orig, err1 := strconv.ParseInt(fields[0], 10, 64)
		memUsed, err2 := strconv.ParseInt(fields[2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		found = true
		out.SwapBytes += orig
		out.RealBytes += memUsed
	}
	return out, found
}

func readInt(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}
