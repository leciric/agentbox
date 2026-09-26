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
// hands whatever the idle agents aren't using to the busy ones, and holds the
// sum under one budget — where rebalancing each agent's own limits from the
// daemon lags behind and OOM-kills whatever it lowers too far.
//
// The parent is a plain top-level cgroup, /sys/fs/cgroup/agentbox, not a
// systemd slice: Incus' liblxc places a container wherever raw.lxc's
// lxc.cgroup.dir.container says, relative to the cgroup root, and systemd
// leaves a cgroup it didn't make alone. Making it needs root, once, which
// is what `agentbox host budget` does (package hostsetup): it installs a
// oneshot unit that makes the cgroup at boot and gives the four files the
// budget is written to — memory.high, memory.max, memory.swap.max and
// cpu.max — to the user the daemon runs as. Everything else, the cgroup's
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

// highShare is where memory.high sits, as a share of memory.max: past it,
// the kernel reclaims from the agents and pushes their pages to swap, so the
// budget bends before it breaks — memory.max is where it kills.
const highShare = 0.9

// swapFullShare is how full the budget's swap may get before memory.high is
// lifted to memory.max. memory.high never kills: when reclaim has nowhere to
// put anonymous pages — no swap on the host, or the budget's swap used up —
// the kernel throttles every process over it instead, indefinitely. On a
// test host, a 350 MB allocation under a 300 MB memory.high with no swap was
// still stalled after 30 s at 92% memory pressure; under memory.max alone it
// was killed at once. A stall is the freeze this budget exists to prevent,
// so the soft limit only holds while there is swap left to reclaim into.
const swapFullShare = 0.9

// Budget is the shared budget's size. Memory and Swap are sizes Incus would
// take (ParseBytes), CPU a count of cores.
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
// one line. The host keeps a third of its memory, and never less than 6 GiB —
// a desktop, a browser and an editor — and a quarter of its cores, at least
// one; agents may use half the host's swap, at most half their memory. A
// host too small to leave 6 GiB gets half its memory for agents.
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
	why := fmt.Sprintf("Leaves this host %s of its %s of memory and %d of its %d cores", HumanBytes(h.Memory-sizeOf(b.Memory)), HumanBytes(h.Memory), h.Cores-cores, h.Cores)
	if h.Swap > 0 {
		b.Swap = roundSize(min(h.Swap/2, sizeOf(b.Memory)/2))
		kind := "swap"
		if h.SwapKind == "zram" {
			kind = "zram swap"
		}
		why += fmt.Sprintf(", and lets agents use %s of its %s of %s.", HumanBytes(sizeOf(b.Swap)), HumanBytes(h.Swap), kind)
	} else {
		why += ". This host has no swap, so agents are only ever held at the hard limit: without swap, a soft one stalls them instead of freeing memory."
	}
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
			return fmt.Errorf("the shared budget's swap is a size like 4GiB, never 0: with no swap, the kernel stalls agents over the budget instead of freeing memory; got %q", b.Swap)
		}
	} else if h.Swap > 0 {
		return errors.New("the shared budget's swap is a size like 4GiB, never 0: with no swap, the kernel stalls agents over the budget instead of freeing memory")
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
// this user's to write.
func BudgetReady() error {
	if _, err := os.Stat(BudgetDir); err != nil {
		return fmt.Errorf("%w: %s doesn't exist", ErrBudgetNotReady, BudgetDir)
	}
	for _, name := range budgetFiles {
		f, err := os.OpenFile(filepath.Join(BudgetDir, name), os.O_WRONLY, 0)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: %s has no %s, so the memory and CPU controllers aren't enabled for it", ErrBudgetNotReady, BudgetDir, name)
			}
			return fmt.Errorf("%w: %s isn't yours to write", ErrBudgetNotReady, filepath.Join(BudgetDir, name))
		}
		_ = f.Close()
	}
	return nil
}

// budgetValues are what the parent cgroup's four files are written with.
// Off, every one is "max": the agents still inside, until they restart, share
// the host again with nothing holding them. memory.high is held below
// memory.max only while agents have swap to be reclaimed into: see
// swapFullShare.
func budgetValues(on bool, b Budget, hostSwap, swapUsed int64) map[string]string {
	out := map[string]string{"memory.high": "max", "memory.max": "max", "memory.swap.max": "max", "cpu.max": "max " + strconv.Itoa(cpuPeriod)}
	if !on {
		return out
	}
	memory := sizeOf(b.Memory)
	swap := sizeOf(b.Swap)
	out["memory.max"] = strconv.FormatInt(memory, 10)
	out["memory.swap.max"] = strconv.FormatInt(swap, 10)
	out["cpu.max"] = fmt.Sprintf("%d %d", b.CPU*cpuPeriod, cpuPeriod)
	if hostSwap > 0 && swap > 0 && float64(swapUsed) < float64(swap)*swapFullShare {
		out["memory.high"] = strconv.FormatInt(int64(float64(memory)*highShare), 10)
	}
	return out
}

// ApplyBudget writes the budget into the parent cgroup, live: the kernel
// applies each file the moment it is written, to every agent inside. Lowering
// memory.max under what the agents use makes the kernel reclaim, and kill
// past what it can't, which is the same as any other memory limit.
//
// memory.high is written last and memory.max first when tightening, so the
// two never cross: the kernel refuses nothing, but a high above max means
// nothing.
func ApplyBudget(on bool, b Budget) error {
	var hostTotal int64
	if total, _, err := hostSwap(); err == nil {
		hostTotal = total
	}
	values := budgetValues(on, b, hostTotal, budgetSwapUsed())
	for _, name := range []string{"memory.max", "memory.swap.max", "cpu.max", "memory.high"} {
		if err := writeBudgetFile(name, values[name]); err != nil {
			return err
		}
	}
	return nil
}

// budgetSwapUsed is how much swap the agents in the budget hold between them.
func budgetSwapUsed() int64 {
	b, err := os.ReadFile(filepath.Join(BudgetDir, "memory.swap.current"))
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func writeBudgetFile(name, value string) error {
	path := filepath.Join(BudgetDir, name)
	current, err := os.ReadFile(path)
	if err == nil && sameBudgetValue(strings.TrimSpace(string(current)), value) {
		return nil
	}
	if err := os.WriteFile(path, []byte(value), 0); err != nil {
		return fmt.Errorf("setting the shared budget's %s: %w", name, err)
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
			steps = append(steps, []string{"config", "set", a.Instance, limitMemorySwap + "=" + swap})
		}
		for _, args := range steps {
			if _, err := m.Incus.Run(ctx, args...); err != nil {
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
// swap together, and the budget's soft limit can only reclaim an agent's
// pages into swap — one whose own swap.max is 0 would stall at it instead.
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

// budgetSteps are the incus commands that put an instance's raw.lxc where the
// budget wants it, from have, the instance's own configuration. A copy of
// another agent, or a project base saved from one, carries that agent's
// placement, which must never be kept: two machines in one cgroup directory
// can't both start.
func budgetSteps(instance string, on bool, have map[string]string) [][]string {
	want := budgetRawLXC(have["raw.lxc"], instance, on)
	switch want {
	case have["raw.lxc"]:
		return nil
	case "":
		return [][]string{{"config", "unset", instance, "raw.lxc"}}
	default:
		return [][]string{{"config", "set", instance, "raw.lxc=" + want}}
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
	for _, args := range budgetSteps(instance, m.budgetOn(ctx), d.Config) {
		if _, err := m.Incus.Run(ctx, args...); err != nil {
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
