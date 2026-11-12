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

	"agentbox/internal/incus"
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
	before, err := m.sampleUsage(ctx)
	if err != nil {
		return HostUsage{}, nil, err
	}
	select {
	case <-ctx.Done():
		return HostUsage{}, nil, ctx.Err()
	case <-time.After(interval):
	}
	host, usage, _, err := m.UsageSince(ctx, before)
	return host, usage, err
}

// UsageSample is what one look at the host and its agents read: the
// counters a rate is measured between, and Incus's listing of every instance,
// which ListFrom reads the agents' states from as well.
type UsageSample struct {
	Instances []incus.Instance
	agents    []state.Agent
	at        time.Time
	cpu       cpuTimes
	disk      ioBytes
	diskErr   error
	io        map[string]ioBytes
}

// UsageSince measures use from since to now, with one more look, which it
// returns for the next call to measure from. Called on a timer, that is one
// listing of Incus's instances a tick, where Usage takes two and waits
// between them. A nil since is Usage over a second.
func (m *Manager) UsageSince(ctx context.Context, since *UsageSample) (HostUsage, []AgentUsage, *UsageSample, error) {
	if since == nil {
		before, err := m.sampleUsage(ctx)
		if err != nil {
			return HostUsage{}, nil, nil, err
		}
		select {
		case <-ctx.Done():
			return HostUsage{}, nil, nil, ctx.Err()
		case <-time.After(time.Second):
		}
		since = before
	}
	now, err := m.sampleUsage(ctx)
	if err != nil {
		return HostUsage{}, nil, nil, err
	}
	host, usage, err := m.usageBetween(ctx, since, now)
	return host, usage, now, err
}

// sampleUsage reads every counter a rate is measured between, and lists
// Incus's instances.
func (m *Manager) sampleUsage(ctx context.Context) (*UsageSample, error) {
	agents, err := m.Store.Agents(ctx, "")
	if err != nil {
		return nil, err
	}
	smp := &UsageSample{agents: agents, io: map[string]ioBytes{}}
	if smp.cpu, err = hostCPU(); err != nil {
		return nil, err
	}
	// Disk IO is a rate the same way CPU is: counters read on both sides of
	// the interval. A host or agent whose counters can't be read shows none.
	smp.disk, smp.diskErr = hostDiskIO("/proc/diskstats", sysRoot)
	for _, a := range agents {
		if io, ok := cgroupIO(agentCgroup(cgroupRoot, a.Instance), sysRoot); ok {
			smp.io[a.Instance] = io
		}
	}
	if smp.Instances, err = m.Incus.Instances(ctx); err != nil {
		return nil, err
	}
	smp.at = time.Now()
	return smp, nil
}

// usageBetween is the host's and each agent's use between two samples, the
// agents being after's.
func (m *Manager) usageBetween(ctx context.Context, before, after *UsageSample) (HostUsage, []AgentUsage, error) {
	elapsed := after.at.Sub(before.at)
	host := HostUsage{Cores: HostCores()}
	if total := after.cpu.total - before.cpu.total; total > 0 && after.cpu.total > before.cpu.total {
		host.CPU = 100 * (1 - float64(after.cpu.idle-before.cpu.idle)/float64(total))
	}
	if before.diskErr == nil && after.diskErr == nil && elapsed > 0 {
		host.DiskRead, host.DiskWrite = ioRate(before.disk, after.disk, elapsed.Seconds())
	}
	if p, ok := hostPressure("/proc/pressure"); ok {
		host.Pressure = &p
	}
	var err error
	if host.MemTotal, host.MemUsed, err = hostMemory(); err != nil {
		return HostUsage{}, nil, err
	}
	if host.PoolUsed, host.PoolTotal, err = m.Incus.PoolSpace(ctx, "default"); err != nil {
		return HostUsage{}, nil, err
	}

	cpuBefore := map[string]int64{}
	for _, inst := range before.Instances {
		if inst.State != nil {
			cpuBefore[inst.Name] = inst.State.CPU.Usage
		}
	}
	current := map[string]int{}
	for i, inst := range after.Instances {
		current[inst.Name] = i
	}
	usage := make([]AgentUsage, 0, len(after.agents))
	for _, a := range after.agents {
		u := AgentUsage{Agent: a, State: "missing"}
		if i, ok := current[a.Instance]; ok {
			inst := after.Instances[i]
			u.State = displayState(inst.Status)
			if inst.State != nil {
				u.Memory = agentMemory(cgroupRoot, a.Instance, inst.State.Memory.Usage)
				u.Processes = inst.State.Processes
				if prev, ok := cpuBefore[a.Instance]; ok && inst.State.CPU.Usage >= prev && elapsed > 0 {
					u.CPU = 100 * float64(inst.State.CPU.Usage-prev) / float64(elapsed.Nanoseconds())
				}
				if prev, ok := before.io[a.Instance]; ok && elapsed > 0 {
					if io, ok := after.io[a.Instance]; ok {
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
	held := guestBalloon()
	return max(total-held, 0), max(total-available-held, 0), s.Err()
}

// guestBalloon is what a memory balloon holds of this machine's memory, when
// the machine is a VM that has one (the vz driver's).
func guestBalloon() int64 {
	features, _ := filepath.Glob("/sys/bus/virtio/drivers/virtio_balloon/virtio*/features")
	if len(features) == 0 {
		return 0
	}
	f, _ := os.ReadFile(features[0])
	vmstat, _ := os.ReadFile("/proc/vmstat")
	return balloonHeld(string(vmstat), strings.TrimSpace(string(f)), int64(os.Getpagesize()))
}

// balloonHeld is what the balloon holds that MemTotal still counts, and
// MemAvailable doesn't: only a balloon that deflates on OOM (virtio feature
// bit 2) leaves MemTotal as it booted. One that doesn't takes its pages off
// MemTotal itself.
func balloonHeld(vmstat, features string, pageSize int64) int64 {
	if len(features) < 3 || features[2] != '1' {
		return 0
	}
	var inflated, deflated int64
	for line := range strings.Lines(vmstat) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		n, _ := strconv.ParseInt(value, 10, 64)
		switch key {
		case "balloon_inflate":
			inflated = n
		case "balloon_deflate":
			deflated = n
		}
	}
	return max(inflated-deflated, 0) * pageSize
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
