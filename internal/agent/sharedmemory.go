package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agentbox/internal/hostos"
)

// Two ceilings over the VM's memory, beside each agent's own memory.max
// (memorylimit.go): one on every agent's machine together, one on every
// project's lead chat together. Each agent under its own cap could still add
// up to the whole VM with a few others, and a lead chat (its ACP adapter, the
// AI tool and whatever its shell runs, on the VM beside the daemon) had no cap
// at all: either way the daemon, Incus and every chat went down with them.
//
//   - agentbox.agents, under the cgroup root, holds every agent's machine:
//     Incus's liblxc starts a container where raw.lxc's
//     lxc.cgroup.dir.container says (AgentPlacement), creating the path and
//     enabling the controllers down it. Its memory.high and memory.max are
//     AgentsLimits, after T3 Code's agent slice: the agents together are
//     throttled with about 6 GiB of the VM left and killed with 4 GiB left,
//     and on a small VM at half and three quarters of it. Its cpu.weight and
//     io.weight are one agent's (AgentCPUWeight, AgentIOWeight), now against
//     the VM's services and the leads as one, and its pids.max a quarter of
//     what the VM's kernel allows, as T3 Code's TasksMax=25%.
//   - agentbox.leads holds each lead chat's process tree in a cgroup of its
//     own (leadchat.go), under LeadsLimits' memory.max: when a chat's tree
//     outgrows what the leads may have together, the kernel kills that tree
//     whole (memory.oom.group) and the daemon ends its turn saying so,
//     rather than the VM's OOM killer choosing among everything.
//
// Both are written live, every few seconds, from the VM's memory as it is now
// (internal/daemon/pressure.go), so they follow the VM as it grows or shrinks:
// on Linux's Cloud Hypervisor VM and on the Mac's Lima VM alike, as both run
// this daemon on Linux. Outside AgentBox's VM (an earlier release's host
// install, CI) the machine is its owner's and nothing is placed or written.
//
// A machine moves into agentbox.agents when it next starts: raw.lxc is read
// then, and a cgroup can't move to another parent. Until then the daemon finds
// it at the root (agentCgroup), and the shared ceiling doesn't cover it.

const (
	// AgentsCgroup is the agents' parent cgroup, under the cgroup root.
	AgentsCgroup = "agentbox.agents"
	// LeadsCgroup is the lead chats' parent cgroup, under the cgroup root.
	LeadsCgroup = "agentbox.leads"
)

// AgentsDir and LeadsDir are those cgroups' directories. Variables for tests,
// which point them at directories of their own; nothing else changes them.
var (
	AgentsDir = filepath.Join(cgroupRoot, AgentsCgroup)
	LeadsDir  = filepath.Join(cgroupRoot, LeadsCgroup)
)

// SharedCgroups reports whether this daemon puts agents and lead chats under
// the shared ceilings: in AgentBox's VM, which is AgentBox's to divide. A
// variable for tests.
var SharedCgroups = hostos.InVM

// GroupLimits is what a parent cgroup is given. A zero memory field is no
// limit ("max"); a zero weight or PidsMax is left as it is.
type GroupLimits struct {
	High      int64 // memory.high, bytes
	Max       int64 // memory.max, bytes
	CPUWeight int   // cpu.weight
	IOWeight  int   // io.weight and io.bfq.weight
	PidsMax   int   // pids.max
}

// AgentsLimits is agentbox.agents' limits in a VM of total bytes whose kernel
// allows tasks processes and threads (0 when unknown): memory.high leaves the
// rest of the VM 6 GiB, or half of a VM of 12 GiB or less, and memory.max
// leaves it 4 GiB, or a quarter of a VM of 16 GiB or less. No memory limit
// when total isn't known.
func AgentsLimits(total int64, tasks int) GroupLimits {
	const gib = int64(1) << 30
	l := GroupLimits{CPUWeight: AgentCPUWeight, IOWeight: AgentIOWeight, PidsMax: tasks / 4}
	if total > 0 {
		l.High = max(total-6*gib, total/2)
		l.Max = max(total-4*gib, total/4*3)
	}
	return l
}

// LeadsLimits is agentbox.leads' limits in a VM of total bytes: a memory.max
// of a quarter of the VM, and at least the smaller of half of it and 4 GiB.
// A lead chat is about half a GiB, and a dozen projects' fit in 8 GiB of a 32
// GiB VM, while one whose shell runs away is killed long before the daemon
// and the agents feel it. No memory.high: a throttled leads' cgroup would
// slow every project's chat at once, where a kill ends one turn.
func LeadsLimits(total int64) GroupLimits {
	const gib = int64(1) << 30
	if total <= 0 {
		return GroupLimits{}
	}
	return GroupLimits{Max: max(total/4, min(total/2, 4*gib))}
}

// SetAgentsGroup makes agentbox.agents if it isn't there and writes its
// limits for a VM of total bytes.
func SetAgentsGroup(total int64) error {
	return setGroup(AgentsDir, AgentsLimits(total, vmTasks("/proc/sys/kernel")), []string{"cpu", "io", "memory", "pids"}, writeCgroup)
}

// SetLeadsGroup makes agentbox.leads if it isn't there and writes its limits
// for a VM of total bytes.
func SetLeadsGroup(total int64) error {
	return setGroup(LeadsDir, LeadsLimits(total), []string{"memory", "pids"}, writeCgroup)
}

// setGroup makes the parent cgroup dir, enables controllers for its children
// (the ones the kernel offers it), and writes l to it, each file only when it
// changes.
func setGroup(dir string, l GroupLimits, controllers []string, write func(path, value string) error) error {
	if err := mkdirCgroup(dir); err != nil {
		return err
	}
	if err := enableControllers(dir, controllers, write); err != nil {
		return err
	}
	errs := []error{
		setCgroupLimit(dir, "memory.high", l.High, write),
		setCgroupLimit(dir, "memory.max", l.Max, write),
	}
	if l.CPUWeight > 0 {
		errs = append(errs, setCgroupValue(dir, "cpu.weight", strconv.Itoa(l.CPUWeight), write))
	}
	if l.IOWeight > 0 {
		errs = append(errs, setIOWeight(dir, l.IOWeight, write))
	}
	if l.PidsMax > 0 {
		errs = append(errs, setCgroupValue(dir, "pids.max", strconv.Itoa(l.PidsMax), write))
	}
	return errors.Join(errs...)
}

// enableControllers turns on, in dir's cgroup.subtree_control, each of want
// that its cgroup.controllers offers and isn't on yet. A directory without
// those files (not cgroup2) is left alone.
func enableControllers(dir string, want []string, write func(path, value string) error) error {
	offered, err := os.ReadFile(filepath.Join(dir, "cgroup.controllers"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	on, err := os.ReadFile(filepath.Join(dir, "cgroup.subtree_control"))
	if err != nil {
		return err
	}
	var add []string
	for _, c := range want {
		if hasField(offered, c) && !hasField(on, c) {
			add = append(add, "+"+c)
		}
	}
	if len(add) == 0 {
		return nil
	}
	return write(filepath.Join(dir, "cgroup.subtree_control"), strings.Join(add, " "))
}

func hasField(b []byte, f string) bool {
	for _, have := range strings.Fields(string(b)) {
		if have == f {
			return true
		}
	}
	return false
}

// vmTasks is the most tasks the VM's kernel allows, as systemd counts it for
// a TasksMax percentage: the smaller of pid_max and threads-max; 0 when
// neither can be read.
func vmTasks(sysctl string) int {
	n := 0
	for _, name := range []string{"pid_max", "threads-max"} {
		b, err := os.ReadFile(filepath.Join(sysctl, name))
		if err != nil {
			continue
		}
		if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && v > 0 && (n == 0 || v < n) {
			n = v
		}
	}
	return n
}

// GroupEvents reads a parent cgroup's MemoryCounts: OwnLimit is how many times
// all of its children together reached its memory.max. ok is false when it
// isn't there.
func GroupEvents(dir string) (MemoryCounts, bool) {
	c, ok := memoryEvents(dir)
	c.Nested = nil
	return c, ok
}

// AgentPlacement is the raw.lxc lines that start an instance's machine inside
// agentbox.agents. liblxc takes the container's cgroup only with its
// monitor's, which stays at the root where it would have been, outside the
// agents' ceiling.
func AgentPlacement(instance string) []string {
	return []string{
		"lxc.cgroup.dir.container=" + AgentsCgroup + "/lxc.payload." + instance,
		"lxc.cgroup.dir.monitor=lxc.monitor." + instance,
	}
}

// placedRawLXC is raw.lxc with its cgroup placement, if any, replaced: by
// AgentPlacement when place, else by none (an earlier release's shared
// budget's lines go either way).
func placedRawLXC(rawLXC, instance string, place bool) string {
	var lines []string
	for _, line := range strings.Split(rawLXC, "\n") {
		key, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		if strings.HasPrefix(strings.TrimSpace(key), "lxc.cgroup.dir") || strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	if place {
		lines = append(lines, AgentPlacement(instance)...)
	}
	return strings.Join(lines, "\n")
}

// ClearStaleCgroups removes what a stopped machine's last run left of its
// cgroups in agentbox.agents and its monitor's at the root, when they are
// empty: liblxc refuses to start a container in a cgroup it didn't make
// itself. Nothing is removed that has a process in it.
func ClearStaleCgroups(instance string) {
	if !SharedCgroups() {
		return
	}
	clearStale(filepath.Join(AgentsDir, "lxc.payload."+instance))
	clearStale(filepath.Join(filepath.Dir(AgentsDir), "lxc.monitor."+instance))
}

// clearStale removes dir and every directory below it, deepest first, while
// they are empty cgroups; it stops at the first that isn't.
func clearStale(dir string) {
	if _, err := os.Stat(dir); err != nil {
		return
	}
	var dirs []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		if removeCgroup(dirs[i]) != nil {
			return
		}
	}
}
