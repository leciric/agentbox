package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agentbox/internal/hostos"
	"agentbox/internal/hostsetup"
	"agentbox/internal/state"
)

// The shared budget: every agent's machine in one parent cgroup, with one
// memory, swap and CPU budget for all of them together.
//
// Per-agent limits (limits.go) are a ceiling each: six agents at 8 GiB on a
// 30 GB host can still add up to 48, and an agent that is idle keeps a share
// nobody else can have. A parent cgroup shares instantly instead — the kernel
// hands whatever the idle agents aren't using to the busy ones — where
// rebalancing each agent's own limits from the daemon lags behind and
// OOM-kills whatever it lowers too far.
//
// Its memory protects the host's own apps rather than fencing the agents in.
// The user picks what agents may use, 20 GiB of a 30 GiB host say, and the
// rest, 10 GiB, is reserved for the host: memory.low on user.slice and
// system.slice (reserve.go). The agents' cgroup gets no memory.high, and a
// memory.max only as a safety margin near the host's memory. So while the
// host's apps don't need their reserve, agents use it as page cache; when the
// apps want it back, the kernel reclaims from the agents first. A fence did
// the opposite: agents held at 16 GiB with 14 GiB free around them kept
// evicting their own file cache and reading it back from the SSD at 2 GB/s,
// and that IO is what froze the desktop.
//
// The disk isn't part of it. io.cost's latency QoS and an io.max write
// ceiling on the budget were tried, and made things worse on the user's host
// (LUKS over btrfs on a budget NVMe): btrfs transaction commits waited on the
// agents' throttled writeback, up to 11.5 s, with user.slice stalled on IO
// 45–50% of the time, and io.cost slowed the whole device for everyone.
// legacy.go puts back what that left behind.
//
// The parent is a plain top-level cgroup, /sys/fs/cgroup/agentbox, not a
// systemd slice: Incus' liblxc places a container wherever raw.lxc's
// lxc.cgroup.dir.container says, relative to the cgroup root, and systemd
// leaves a cgroup it didn't make alone. Making it needs root, once, which
// is what `agentbox host budget` does (package hostsetup): it installs a
// oneshot unit that makes the cgroup at boot and gives the files the budget
// is written to — memory.max, memory.swap.max and cpu.max, and the slices'
// memory.low — to the user the daemon runs as. Everything else, the cgroup's
// children included, stays root's and Incus'.
//
// An agent moves in when its machine starts: raw.lxc is read at start, so
// turning the budget on sets it on every agent, and those already running
// stay where they are until they restart. Its own limits keep working inside
// the budget — they are the agent's cgroup's, a level below.

// BudgetCgroup is the parent cgroup's name, under the cgroup root.
const BudgetCgroup = "agentbox"

// budgetFiles are the files of the parent cgroup the daemon writes, and so the
// ones `agentbox host budget` gives to the user.
var budgetFiles = hostsetup.BudgetFiles

// BudgetDir is the parent cgroup's directory. A variable for tests, which
// point it at a directory of their own; nothing else changes it.
var BudgetDir = filepath.Join(cgroupRoot, BudgetCgroup)

// cpuPeriod is the period cpu.max is written with, in microseconds: the
// kernel's own default, so a quota of N periods is N cores' worth.
const cpuPeriod = 100000

// safetyMargin is what the agents' memory.max keeps free of the host's
// memory: not their budget, which the reserve (reserve.go) holds, but a floor
// under which even the host's own reclaim couldn't keep up.
const safetyMargin = int64(2) << 30

// Budget is the shared budget's size. Memory and Swap are sizes Incus would
// take (ParseBytes), CPU a count of cores. Memory is what agents may use
// while the host's apps need their share: the host's memory less it is
// reserved for those apps.
type Budget struct {
	Memory string
	Swap   string
	CPU    int
}

// HostResources is what the budget is worked out from.
type HostResources struct {
	Memory   int64  // MemTotal, in bytes
	Swap     int64  // SwapTotal, in bytes
	SwapKind string // "zram", "disk", or "" with no swap
	Cores    int
}

// ReadHostResources reads this host's memory, swap and cores.
func ReadHostResources() HostResources {
	h := HostResources{Memory: HostMemory(), Cores: HostCores()}
	if total, _, err := hostSwap(); err == nil {
		h.Swap = total
	}
	if h.Swap > 0 {
		h.SwapKind = "disk"
		if _, ok := zramTotal(zramRoot); ok {
			h.SwapKind = "zram"
		}
	}
	return h
}

// SuggestBudget works out a budget from what the host has, and says why in
// one line. The host's apps keep a third of its memory, and never less than
// 6 GiB — a desktop, a browser and an editor — and a quarter of its cores, at
// least one; agents may use half the host's swap, at most half their memory.
// A host too small to leave 6 GiB reserves half its memory.
func SuggestBudget(h HostResources) (Budget, string) {
	const gib = int64(1) << 30
	reserve := max(h.Memory/3, 6*gib)
	memory := h.Memory - reserve
	if memory < gib {
		memory = h.Memory / 2
	}
	keep := max(h.Cores/4, 1)
	cores := max(h.Cores-keep, 1)
	b := Budget{Memory: roundSize(memory), CPU: cores}
	why := fmt.Sprintf("Reserves %s of this host's %s of memory for your own apps, and keeps %d of its %d cores free", HumanBytes(h.Memory-sizeOf(b.Memory)), HumanBytes(h.Memory), h.Cores-cores, h.Cores)
	if h.Swap > 0 {
		b.Swap = roundSize(min(h.Swap/2, sizeOf(b.Memory)/2))
		kind := "swap"
		if h.SwapKind == "zram" {
			kind = "zram swap"
		}
		why += fmt.Sprintf("; agents may use %s of its %s of %s.", HumanBytes(sizeOf(b.Swap)), HumanBytes(h.Swap), kind)
	} else {
		why += "."
	}
	why += " Agents may borrow the reserved memory while your apps aren't using it, and give it back first when they are."
	return b, why
}

// roundSize renders n as whole GiB, rounded down, or as 256 MiB steps below
// one GiB.
func roundSize(n int64) string {
	const gib, step = int64(1) << 30, int64(256) << 20
	if n >= gib {
		return strconv.FormatInt(n/gib, 10) + "GiB"
	}
	return strconv.FormatInt(max(n/step, 1)*256, 10) + "MiB"
}

// sizeOf is ParseBytes for a size already known to be good, or 0.
func sizeOf(size string) int64 {
	n, _ := ParseBytes(size)
	return n
}

// Validate checks a budget against the host it is for.
func (b Budget) Validate(h HostResources) error {
	memory, err := ParseBytes(b.Memory)
	if err != nil || strings.HasSuffix(b.Memory, "%") {
		return fmt.Errorf("the shared budget's memory is a size like 20GiB; got %q", b.Memory)
	}
	if h.Memory > 0 && memory > h.Memory {
		return fmt.Errorf("the shared budget's memory, %s, is more than this host has (%s)", b.Memory, HumanBytes(h.Memory))
	}
	if memory < 512<<20 {
		return fmt.Errorf("the shared budget's memory is at least 512MiB; %s is too little for even one agent", b.Memory)
	}
	if b.Swap != "" {
		if _, err := ParseBytes(b.Swap); err != nil {
			return fmt.Errorf("the shared budget's swap is a size like 4GiB, never 0: it is where the kernel puts agents' memory when your apps need theirs back; got %q", b.Swap)
		}
	} else if h.Swap > 0 {
		return errors.New("the shared budget's swap is a size like 4GiB, never 0: it is where the kernel puts agents' memory when your apps need theirs back")
	}
	if b.CPU < 1 || (h.Cores > 0 && b.CPU > h.Cores) {
		return fmt.Errorf("the shared budget's CPU is a whole number of cores between 1 and %d; got %d", h.Cores, b.CPU)
	}
	return nil
}

// Describe says what a budget is, in one line.
func (b Budget) Describe() string {
	cores := "cores"
	if b.CPU == 1 {
		cores = "core"
	}
	swap := "no swap"
	if b.Swap != "" {
		swap = b.Swap + " of swap"
	}
	return fmt.Sprintf("%s of memory, %s and %d %s", b.Memory, swap, b.CPU, cores)
}

// DescribeOn is Describe, with what stays reserved for the apps of a host
// with hostMemory: "20GiB of memory, 8GiB of swap and 12 cores; 10.0 GiB of
// memory stays reserved for your apps".
func (b Budget) DescribeOn(hostMemory int64) string {
	out := b.Describe()
	if reserve := hostMemory - sizeOf(b.Memory); hostMemory > 0 && reserve > 0 {
		out += fmt.Sprintf("; %s of memory stays reserved for your apps", HumanBytes(reserve))
	}
	return out
}

// BudgetSupport says why this machine can't have a shared budget, or "" when
// it can. In a VM on a Mac or on Windows, the VM's own size is the budget
// already — every agent is inside it — and WSL2's cgroups are the distro's,
// set up by WSL rather than by a unit AgentBox could install.
func BudgetSupport() string {
	switch hostos.OS() {
	case "":
	case hostos.Windows:
		return "On Windows every agent runs in AgentBox's WSL distro, whose memory and cores are already their shared budget: set them in .wslconfig."
	default:
		return fmt.Sprintf("On %s every agent runs in AgentBox's VM, whose memory and cores are already their shared budget: resize the VM instead.", hostos.Name())
	}
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers")); err != nil {
		return "This host doesn't use cgroup v2, which the shared budget needs."
	}
	return ""
}

// ErrBudgetNotReady is BudgetReady's answer when the parent cgroup hasn't been
// made, or isn't the user's to write.
var ErrBudgetNotReady = errors.New("the shared budget's cgroup isn't set up")

// BudgetReady checks that the parent cgroup is there and that its budget is
// this user's to write, the host's reserve (reserve.go) included.
func BudgetReady() error {
	if _, err := os.Stat(BudgetDir); err != nil {
		return fmt.Errorf("%w: %s doesn't exist", ErrBudgetNotReady, BudgetDir)
	}
	var paths []string
	for _, name := range budgetFiles {
		paths = append(paths, filepath.Join(BudgetDir, name))
	}
	for _, path := range append(paths, reservePaths()...) {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: %s has no %s, so the memory and CPU controllers aren't enabled for it", ErrBudgetNotReady, filepath.Dir(path), filepath.Base(path))
			}
			return fmt.Errorf("%w: %s isn't yours to write", ErrBudgetNotReady, path)
		}
		_ = f.Close()
	}
	return nil
}

// BudgetPaths are every file the budget is written to: the parent cgroup's,
// and the host's slices' memory.low. For tests, which make them.
func BudgetPaths() []string {
	var out []string
	for _, name := range budgetFiles {
		out = append(out, filepath.Join(BudgetDir, name))
	}
	return append(out, reservePaths()...)
}

// budgetWrite is one cgroup file and what it's written with.
type budgetWrite struct{ path, value string }

// budgetValues are what the budget's files are written with, in the order
// they're written: the reserve first, so a host whose slices aren't this
// user's yet fails before the agents' ceiling is lifted, and nothing is left
// holding agents at the safety margin with nothing reserved for the host.
// The agents' memory.max is the host's memory less safetyMargin, or their
// budget where that's more. Off, every limit is "max" and the reserve 0: the
// agents still inside, until they restart, share the host again with nothing
// holding them.
func budgetValues(on bool, b Budget, hostMemory int64) []budgetWrite {
	memory, swap, cpu := "max", "max", "max "+strconv.Itoa(cpuPeriod)
	reserve := map[string]int64{}
	if on {
		agents := sizeOf(b.Memory)
		ceiling := agents
		if hostMemory > 0 {
			ceiling = max(agents, hostMemory-safetyMargin)
			reserve = reserveSplit(max(hostMemory-agents, 0))
		}
		memory = strconv.FormatInt(ceiling, 10)
		swap = strconv.FormatInt(sizeOf(b.Swap), 10)
		cpu = fmt.Sprintf("%d %d", b.CPU*cpuPeriod, cpuPeriod)
	}
	var out []budgetWrite
	for _, slice := range reserveSlices {
		out = append(out, budgetWrite{reservePath(slice), strconv.FormatInt(reserve[slice], 10)})
	}
	return append(out,
		budgetWrite{filepath.Join(BudgetDir, "memory.max"), memory},
		budgetWrite{filepath.Join(BudgetDir, "memory.swap.max"), swap},
		budgetWrite{filepath.Join(BudgetDir, "cpu.max"), cpu})
}

// ApplyBudget writes the budget, live: the kernel applies each file the
// moment it is written. Written again while the budget is on, it also puts
// back a reserve systemd reset — it writes the slices' memory.low itself when
// it reloads.
func ApplyBudget(on bool, b Budget) error {
	for _, w := range budgetValues(on, b, HostMemory()) {
		if err := writeBudgetFile(w.path, w.value); err != nil {
			return err
		}
	}
	return nil
}

func writeBudgetFile(path, value string) error {
	current, err := os.ReadFile(path)
	if err == nil && sameBudgetValue(strings.TrimSpace(string(current)), value) {
		return nil
	}
	if err := os.WriteFile(path, []byte(value), 0); err != nil {
		return fmt.Errorf("setting the shared budget's %s: %w", path, err)
	}
	return nil
}

// sameBudgetValue compares what a cgroup file says with what would be
// written: the kernel reads sizes back rounded to pages.
func sameBudgetValue(have, want string) bool {
	if have == want {
		return true
	}
	h, err1 := strconv.ParseInt(have, 10, 64)
	w, err2 := strconv.ParseInt(want, 10, 64)
	return err1 == nil && err2 == nil && h/4096 == w/4096
}

// SharedBudget reads the installation's shared budget: whether it is on, and
// its size — what was chosen, field by field, and SuggestBudget for this host
// where nothing was.
func (m *Manager) SharedBudget(ctx context.Context) (on bool, b Budget, err error) {
	on, err = m.Store.Flag(ctx, state.SettingSharedBudget)
	if err != nil {
		return false, Budget{}, err
	}
	b, _ = SuggestBudget(ReadHostResources())
	for key, into := range map[string]*string{
		state.SettingSharedBudgetMemory: &b.Memory,
		state.SettingSharedBudgetSwap:   &b.Swap,
	} {
		v, err := m.Store.Setting(ctx, key)
		if err != nil {
			return false, Budget{}, err
		}
		if v != "" {
			*into = v
		}
	}
	v, err := m.Store.Setting(ctx, state.SettingSharedBudgetCPU)
	if err != nil {
		return false, Budget{}, err
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		b.CPU = n
	}
	return on, b, nil
}

// budgetOn is SharedBudget's switch alone, for the steps that only need to
// know which cgroup an agent belongs in.
func (m *Manager) budgetOn(ctx context.Context) bool {
	on, err := m.Store.Flag(ctx, state.SettingSharedBudget)
	return err == nil && on
}

// ApplySharedBudget brings everything in line with the setting: the parent
// cgroup's files, and every agent's raw.lxc and swap. It returns how many
// running agents aren't where the setting puts them yet, which they will be
// when they restart.
func (m *Manager) ApplySharedBudget(ctx context.Context) (pending int, err error) {
	on, b, err := m.SharedBudget(ctx)
	if err != nil {
		return 0, err
	}
	var applyErr error
	if on || BudgetReady() == nil {
		// Off and never set up, there is nothing to put back.
		applyErr = ApplyBudget(on, b)
	}
	agents, err := m.Store.Agents(ctx, "")
	if err != nil {
		return 0, err
	}
	if len(agents) == 0 {
		return 0, applyErr
	}
	// The setting is stored by now, and every agent is put where it says at
	// its next start anyway (ensureBudgetPlacement): a failure here says so.
	placing := func(err error) error {
		return fmt.Errorf("the shared budget is saved, but putting agents in it failed (each moves in at its next start anyway): %w", err)
	}
	instances, err := m.Incus.Instances(ctx)
	if err != nil {
		return 0, placing(err)
	}
	byName := make(map[string]int, len(instances))
	for i, inst := range instances {
		byName[inst.Name] = i
	}
	for _, a := range agents {
		i, ok := byName[a.Instance]
		if !ok {
			continue
		}
		inst := instances[i]
		steps := budgetSteps(a.Instance, on, inst.Config)
		if swap := agentSwapValue(LimitsOf(inst.ExpandedConfig).Memory, on); swap != inst.Config[limitMemorySwap] && inst.Config[limitMemory] != "" {
			steps = append(steps, setConfig(a.Instance, limitMemorySwap+"="+swap))
		}
		for _, step := range steps {
			if err := step(ctx, m.Incus); err != nil {
				return pending, placing(err)
			}
		}
		if displayState(inst.Status) != "stopped" && InBudget(a.Instance) != on {
			pending++
		}
	}
	return pending, applyErr
}

// InBudget reports whether an agent's machine is running inside the budget's
// cgroup right now.
func InBudget(instance string) bool {
	_, err := os.Stat(filepath.Join(BudgetDir, instance))
	return err == nil
}

// RunningOutsideBudget reports whether an agent's machine is running in its
// own cgroup at the root, outside the budget.
func RunningOutsideBudget(instance string) bool {
	_, err := os.Stat(filepath.Join(cgroupRoot, "lxc.payload."+instance))
	return err == nil
}

// agentSwapValue is limits.memory.swap for an agent with the given memory
// limit. Outside the budget, a capped agent is kept out of swap (MemorySwap).
// Inside it, it may swap: the budget's memory.swap.max caps what all agents
// swap together, and when the host's apps take their reserve back, swap is
// the only place the kernel can reclaim an agent's anonymous pages to.
func agentSwapValue(memory string, shared bool) string {
	switch {
	case memory == "":
		return ""
	case shared:
		return "true"
	default:
		return MemorySwap
	}
}

// budgetRawLXC is what raw.lxc should say for an instance: whatever it said,
// with AgentBox's cgroup placement added when the budget is on and taken out
// when it's off.
func budgetRawLXC(existing, instance string, on bool) string {
	var lines []string
	for _, line := range strings.Split(existing, "\n") {
		key, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		if strings.HasPrefix(strings.TrimSpace(key), "lxc.cgroup.dir.") || strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	if on {
		lines = append(lines,
			"lxc.cgroup.dir.container="+BudgetCgroup+"/"+instance,
			"lxc.cgroup.dir.monitor="+BudgetCgroup+"/"+instance+".monitor")
	}
	return strings.Join(lines, "\n")
}

// budgetSteps are the Incus changes that put an instance's raw.lxc where the
// budget wants it, from have, the instance's own configuration. A copy of
// another agent, or a project base saved from one, carries that agent's
// placement, which must never be kept: two machines in one cgroup directory
// can't both start.
func budgetSteps(instance string, on bool, have map[string]string) []incusStep {
	want := budgetRawLXC(have["raw.lxc"], instance, on)
	switch want {
	case have["raw.lxc"]:
		return nil
	case "":
		return []incusStep{unsetConfig(instance, "raw.lxc")}
	default:
		return []incusStep{setConfig(instance, "raw.lxc="+want)}
	}
}

// ensureBudgetPlacement puts a stopped agent's raw.lxc where the setting says
// before it starts, so an agent made before the budget was turned on — or
// restored from a snapshot taken before — moves in at this start.
func (m *Manager) ensureBudgetPlacement(ctx context.Context, instance string) error {
	d, err := m.Incus.Details(ctx, instance)
	if err != nil {
		return err
	}
	for _, step := range budgetSteps(instance, m.budgetOn(ctx), d.Config) {
		if err := step(ctx, m.Incus); err != nil {
			return err
		}
	}
	return nil
}

// agentCgroup is the directory of an agent's cgroup: inside the budget's
// parent when it runs there, lxc.payload.<instance> at the root otherwise.
func agentCgroup(root, instance string) string {
	inside := filepath.Join(root, BudgetCgroup, instance)
	if _, err := os.Stat(inside); err == nil {
		return inside
	}
	return filepath.Join(root, "lxc.payload."+instance)
}
