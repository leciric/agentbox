package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"agentbox/internal/state"
)

// Resource limits on an agent's machine.
//
// An agent running a build or a test suite will take every core it can see and
// every page of memory the kernel will give it, and the host it shares with
// its user has a desktop on it. AgentBox caps agents by default so that never
// costs the user their machine.
//
// Three Incus keys do three different jobs, and which one you reach for
// depends on what you are protecting (see D56 for the whole argument):
//
//   - limits.cpu=N, a plain count, exposes N *floating* cores. Incus spreads
//     them over the host's cores itself and re-balances when instances start
//     and stop, so nothing is pinned and two agents don't fight over the same
//     core. It is the cap that holds even on an idle host: `nproc` inside the
//     agent says N, so `make -j$(nproc)` and vitest size themselves to it.
//     A *set* or a *range* ("0-3", "1,2,3") pins instead, which is the wrong
//     shape here — every agent would land on the same cores — so AgentBox
//     takes only a count.
//   - limits.cpu.allowance is a share, not a cap. A percentage drives
//     cpu.weight in cgroup v2, which only decides who wins when the CPUs are
//     contended; an idle host still lets the agent have everything. A time
//     chunk ("25ms/100ms") drives cpu.max instead, a hard ceiling that applies
//     even on an idle host, and is counted against the *total*, not per core.
//   - limits.memory is a hard ceiling on the cgroup's memory.max. It caps
//     RAM, not swap: on its own, an agent that reaches it is pushed into the
//     host's swap rather than stopped, which is what limits.memory.swap is
//     for (see MemorySwap).
//
// Empty means no limit, for all three.
type Limits struct {
	CPU       string // limits.cpu: a count like "4"; "" is every core
	Allowance string // limits.cpu.allowance: "50%" or "25ms/100ms"; "" is all of it
	Memory    string // limits.memory: "8GiB"; "" is all the host's memory
	// ConfiguredCPU is what CPU was actually chosen (ConfiguredCPU, the
	// function): CPU itself, unless the "never freeze my CPU" budget
	// (RecomputeCPUCaps) is holding it below that. Only ever read, on a
	// Limits LimitsOf built from an instance's own configuration — Resolve,
	// Validate and SetLimits know nothing about it.
	ConfiguredCPU string
}

// LimitKeys are the Incus keys AgentBox owns on an agent's instance. A project
// base must carry none of them (SaveBase), so a new agent gets the
// installation's defaults rather than whatever the agent the base was saved
// from happened to be capped at.
var LimitKeys = []string{limitCPU, limitCPUAllowance, limitMemory, limitCPUPriority, limitMemorySwap, limitCPUConfigured, limitCPUUnlimited}

const (
	limitCPU          = "limits.cpu"
	limitCPUAllowance = "limits.cpu.allowance"
	limitMemory       = "limits.memory"
	limitCPUPriority  = "limits.cpu.priority"
	limitMemorySwap   = "limits.memory.swap"
	// limitCPUConfigured and limitCPUUnlimited mirror what an agent's CPU
	// limit was actually chosen to be — by SetLimits, or at Create — kept
	// apart from limits.cpu itself, which the "never freeze my CPU" budget
	// (RecomputeCPUCaps) may drive below it. Without this mirror, lifting the
	// budget's cap would have nothing to put back except whatever limits.cpu
	// happens to hold at the time, which is the cap, not the choice. Two keys
	// rather than one because limits.cpu itself can't be set to "" (Incus
	// refuses it) to mean "no limit chosen" — user.* keys have no such
	// restriction, but distinguishing "chosen: no limit" from "never chosen at
	// all" still needs a key whose mere presence is the answer.
	limitCPUConfigured = "user.agentbox.cpu.configured"
	limitCPUUnlimited  = "user.agentbox.cpu.unlimited"
)

// MemorySwap is what AgentBox sets limits.memory.swap to on an agent whose
// memory is capped: "false", which Incus turns into memory.swap.max=0, so the
// agent's own pages never go to the host's swap.
//
// A memory ceiling that lets the agent swap isn't one. memory.max only counts
// RAM, so an agent at its limit goes on allocating into swap, and on a host
// with zram — the default on Fedora, and on the machine that froze with six
// agents each running a kind cluster — swap *is* RAM, compressed, paid for in
// the CPU the desktop needs to stay responsive. With swap off, an agent past
// its ceiling is OOM-killed inside its own machine: its build dies, and says
// so, instead of the whole host thrashing. An agent with no memory limit is
// left to swap like any other process; pinning an uncapped agent's pages in
// RAM would only make the host's own programs the ones that get swapped out.
const MemorySwap = "false"

// CPUPriority is what AgentBox sets limits.cpu.priority to on every agent,
// below Incus' default of 10. It is a tiebreak rather than a cap: Incus turns
// (allowance, priority) into one cgroup v2 cpu.weight, where a plain default
// instance weighs 100 and each point of priority below 10 takes one point off.
// So an agent at priority 5 weighs 95 against the desktop's own 100 — it
// loses, but only just. The allowance and the core count are the real levers;
// this is what makes the host win a tie for free.
const CPUPriority = "5"

// LimitChoice is a limit that may or may not have been chosen, per field.
// nil is "nobody chose", which falls back to the installation's default;
// a non-nil "" is a real choice, and means no limit at all. The distinction
// matters here more than anywhere else in AgentBox: "" is the value that
// removes the cap, so treating an absent field as "" would quietly uncap every
// agent created by a caller that didn't know about limits yet.
type LimitChoice struct {
	CPU       *string
	Allowance *string
	Memory    *string
}

// Resolve applies what was chosen on top of the installation's defaults.
func (c LimitChoice) Resolve(defaults Limits) (Limits, error) {
	out := defaults
	if c.CPU != nil {
		out.CPU = strings.TrimSpace(*c.CPU)
	}
	if c.Allowance != nil {
		out.Allowance = strings.TrimSpace(*c.Allowance)
	}
	if c.Memory != nil {
		out.Memory = strings.TrimSpace(*c.Memory)
	}
	return out, out.Validate()
}

// Validate checks each limit is one Incus takes, and one that means what
// AgentBox says it means.
func (l Limits) Validate() error {
	if err := ValidateCPU(l.CPU); err != nil {
		return err
	}
	if err := ValidateAllowance(l.Allowance); err != nil {
		return err
	}
	return ValidateMemory(l.Memory)
}

// ValidateCPU takes a count of cores, or "" for every core. A set or a range
// is refused on purpose: Incus reads "0-3" as a pin, and pinning every agent
// to the same four cores is the opposite of what a count does.
func ValidateCPU(cpu string) error {
	if cpu == "" {
		return nil
	}
	if strings.ContainsAny(cpu, "-,") {
		return fmt.Errorf("a CPU limit is a number of cores, like 4, not %q: %q would pin the agent to those exact cores, and every agent would end up on the same ones", cpu, cpu)
	}
	n, err := strconv.Atoi(cpu)
	if err != nil || n < 1 {
		return fmt.Errorf("a CPU limit is a whole number of cores, at least 1, or empty for every core; got %q", cpu)
	}
	return nil
}

// ValidateAllowance takes a percentage ("50%", a share of the CPUs when the
// host is busy) or a time chunk ("25ms/100ms", a hard ceiling counted against
// the total), or "" for all of it.
func ValidateAllowance(allowance string) error {
	if allowance == "" {
		return nil
	}
	if percent, ok := strings.CutSuffix(allowance, "%"); ok {
		n, err := strconv.Atoi(strings.TrimSpace(percent))
		if err != nil || n < 1 || n > 100 {
			return fmt.Errorf("a CPU share is a percentage between 1%% and 100%%, or empty for all of it; got %q", allowance)
		}
		return nil
	}
	quota, period, ok := strings.Cut(allowance, "/")
	if !ok {
		return fmt.Errorf("a CPU share is a percentage like 50%%, or a time chunk like 25ms/100ms; got %q", allowance)
	}
	for _, part := range []string{quota, period} {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(part), "ms"))
		if err != nil || n < 1 {
			return fmt.Errorf("a CPU share's time chunk is two millisecond values, like 25ms/100ms; got %q", allowance)
		}
	}
	return nil
}

// ValidateMemory takes a size Incus understands, or "" for all of it.
func ValidateMemory(memory string) error {
	if memory == "" {
		return nil
	}
	if percent, ok := strings.CutSuffix(memory, "%"); ok {
		n, err := strconv.Atoi(strings.TrimSpace(percent))
		if err != nil || n < 1 || n > 100 {
			return fmt.Errorf("a memory limit given as a percentage of the host is between 1%% and 100%%; got %q", memory)
		}
		return nil
	}
	if _, err := ParseBytes(memory); err != nil {
		return err
	}
	return nil
}

// byteUnits are the suffixes Incus takes on a size, binary and decimal both.
var byteUnits = []struct {
	suffix string
	factor int64
}{
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40}, {"PiB", 1 << 50}, {"EiB", 1 << 60},
	{"kB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12}, {"PB", 1e15}, {"EB", 1e18},
	{"B", 1},
}

// ParseBytes reads a size the way Incus does: a number, optionally suffixed
// with a binary (GiB) or decimal (GB) unit. A bare number is bytes.
func ParseBytes(size string) (int64, error) {
	size = strings.TrimSpace(size)
	bad := fmt.Errorf("a memory limit is a size like 8GiB or 4096MiB, or empty for no limit; got %q", size)
	for _, unit := range byteUnits {
		rest, ok := strings.CutSuffix(size, unit.suffix)
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil || n <= 0 {
			return 0, bad
		}
		return int64(n * float64(unit.factor)), nil
	}
	n, err := strconv.ParseInt(size, 10, 64)
	if err != nil || n <= 0 {
		return 0, bad
	}
	return n, nil
}

// DefaultLimits is what an installation that has never chosen caps new agents
// at, given the host's core count and memory in bytes (0 when unknown).
//
// Two cores, the least that doesn't make an ordinary `npm ci` painful, and
// never more than the host actually has: that stops a `make -j` taking every
// core and freezing the host.
//
// And DefaultMemory of memory. A core count doesn't stop an agent that runs a
// kind cluster, or a leaky test runner, from taking every page the host has —
// six such agents on a 30 GB host pushed 24 GB into swap and froze the
// desktop. A memory ceiling the kernel enforces by killing processes does
// turn "this build needs more RAM than I thought" into a dead test run, which
// is why it is generous; a frozen desktop is worse.
//
// The share starts empty: a share below 100% slows an agent down on an *idle*
// host for no one's benefit.
func DefaultLimits(hostCores int, hostMemory int64) Limits {
	cores := 2
	if hostCores > 0 {
		cores = min(cores, hostCores) // a one-core host keeps what it has
	}
	return Limits{CPU: strconv.Itoa(cores), Memory: DefaultMemory(hostMemory)}
}

// DefaultMemory is the memory ceiling a new agent starts with: 8GiB, and
// never more than half of the host's memory, so one agent can't take the
// desktop's half. Half is rounded down to whole GiB — the host's MemTotal is
// never a round number, and "15GiB" reads better than "15.3GiB" — or, on a
// host with less than 2 GiB, to 256 MiB steps. A host whose memory can't be
// read gets 8GiB.
func DefaultMemory(hostMemory int64) string {
	const gib, step = int64(1) << 30, int64(256) << 20
	if hostMemory <= 0 {
		return "8GiB"
	}
	half := hostMemory / 2
	if half >= gib {
		return strconv.FormatInt(min(half/gib, 8), 10) + "GiB"
	}
	return strconv.FormatInt(max(half/step, 1)*256, 10) + "MiB"
}

// Describe says what a set of limits means, in one line, for the command line
// and for error messages.
func (l Limits) Describe() string {
	parts := []string{"every core", "no memory limit"}
	if l.CPU != "" {
		cores := "cores"
		if l.CPU == "1" {
			cores = "core"
		}
		parts[0] = l.CPU + " " + cores
	}
	if l.Memory != "" {
		parts[1] = l.Memory + " of memory"
	}
	if l.Allowance != "" {
		parts = append(parts, "a CPU share of "+l.Allowance)
	}
	return strings.Join(parts, ", ")
}

// Defaults is what new agents are capped at on this installation: what was
// chosen in the settings, falling back to DefaultLimits for a setting that has
// never been touched. An empty *stored* value is a real choice — no limit —
// which is why each key is read for whether it is there at all.
func (m *Manager) Defaults(ctx context.Context) (Limits, error) {
	fallback := DefaultLimits(HostCores(), HostMemory())
	out := Limits{}
	for _, field := range []struct {
		key      string
		value    *string
		fallback string
	}{
		{state.SettingDefaultCPU, &out.CPU, fallback.CPU},
		{state.SettingDefaultCPUAllowance, &out.Allowance, fallback.Allowance},
		{state.SettingDefaultMemory, &out.Memory, fallback.Memory},
	} {
		value, set, err := m.Store.SettingValue(ctx, field.key)
		if err != nil {
			return Limits{}, err
		}
		if !set {
			value = field.fallback
		}
		*field.value = value
	}
	return out, nil
}

// Limits reports what Incus applies to an agent's machine, profiles included.
func (m *Manager) Limits(ctx context.Context, a state.Agent) (Limits, error) {
	d, err := m.Incus.Details(ctx, a.Instance)
	if err != nil {
		return Limits{}, err
	}
	return LimitsOf(d.ExpandedConfig), nil
}

// LimitsOf reads the limits out of an instance's configuration.
func LimitsOf(config map[string]string) Limits {
	configured, known := ConfiguredCPU(config)
	if !known {
		configured = config[limitCPU]
	}
	return Limits{
		CPU: config[limitCPU], Allowance: config[limitCPUAllowance], Memory: config[limitMemory],
		ConfiguredCPU: configured,
	}
}

// ConfiguredCPU reads what an agent's CPU limit was actually chosen to be,
// out of the mirror SetLimits and Create keep — not limits.cpu itself, which
// RecomputeCPUCaps may hold below it. known is false for an agent from before
// this mirror existed, which has neither key: its current limits.cpu is the
// best answer then, since that is the last real choice anyone made of it.
func ConfiguredCPU(config map[string]string) (cpu string, known bool) {
	if config[limitCPUUnlimited] == "1" {
		return "", true
	}
	if v, ok := config[limitCPUConfigured]; ok && v != "" {
		return v, true
	}
	return "", false
}

// configuredCPUSteps keeps the ConfiguredCPU mirror in step with cpu, the CPU
// limit that was just really chosen. have is the instance's own
// configuration, so a mirror that already agrees is left alone.
func configuredCPUSteps(instance, cpu string, have map[string]string) [][]string {
	var steps [][]string
	if cpu == "" {
		if have[limitCPUUnlimited] != "1" {
			steps = append(steps, []string{"config", "set", instance, limitCPUUnlimited + "=1"})
		}
		if _, ok := have[limitCPUConfigured]; ok {
			steps = append(steps, []string{"config", "unset", instance, limitCPUConfigured})
		}
		return steps
	}
	if have[limitCPUUnlimited] == "1" {
		steps = append(steps, []string{"config", "unset", instance, limitCPUUnlimited})
	}
	if have[limitCPUConfigured] != cpu {
		steps = append(steps, []string{"config", "set", instance, limitCPUConfigured + "=" + cpu})
	}
	return steps
}

// SetLimits changes an agent's limits while it runs. All three of Incus' keys
// are live-updatable, so nothing has to restart: the new core count triggers a
// re-balance, the share is rewritten into cpu.weight or cpu.max, and the
// memory ceiling becomes the cgroup's new memory.max.
//
// That last one is the dangerous one, and the reason for the guard below.
func (m *Manager) SetLimits(ctx context.Context, a state.Agent, want LimitChoice) (Limits, error) {
	details, err := m.Incus.Details(ctx, a.Instance)
	if err != nil {
		return Limits{}, err
	}
	current := LimitsOf(details.ExpandedConfig)
	next, err := want.Resolve(current)
	if err != nil {
		return Limits{}, err
	}
	if next.Memory != current.Memory {
		if err := m.checkMemoryFits(ctx, a, next.Memory); err != nil {
			return Limits{}, err
		}
	}

	steps := limitSteps(a.Instance, next, details.Config)
	if want.CPU != nil {
		// A choice made here is the new answer to ConfiguredCPU from now on,
		// not only the new limits.cpu — the two only differ once "never
		// freeze my CPU" starts holding the latter below the former.
		next.ConfiguredCPU = next.CPU
		steps = append(steps, configuredCPUSteps(a.Instance, next.CPU, details.Config)...)
	}
	for _, args := range steps {
		if _, err := m.Incus.Run(ctx, args...); err != nil {
			return Limits{}, err
		}
	}
	return next, nil
}

// limitSteps are the incus commands that take an instance from the keys it
// already has to the limits wanted. have is the instance's own configuration,
// so a key that was never there isn't unset for nothing; limits.cpu.priority
// is always brought to CPUPriority, including on a machine made before
// AgentBox set it, so editing an agent's limits is enough to put it behind the
// desktop. limits.memory.swap follows the memory limit the same way: off
// (MemorySwap) whenever there is one, and cleared when there isn't.
func limitSteps(instance string, want Limits, have map[string]string) [][]string {
	set := []string{"config", "set", instance}
	var unset [][]string
	swap := ""
	if want.Memory != "" {
		swap = MemorySwap
	}
	for _, field := range []struct{ key, value string }{
		{limitCPU, want.CPU},
		{limitCPUAllowance, want.Allowance},
		{limitMemory, want.Memory},
		{limitCPUPriority, CPUPriority},
		{limitMemorySwap, swap},
	} {
		switch {
		case field.value == "" && have[field.key] != "":
			// Cleared rather than set to empty: Incus refuses limits.cpu="".
			unset = append(unset, []string{"config", "unset", instance, field.key})
		case field.value != "" && have[field.key] != field.value:
			set = append(set, field.key+"="+field.value)
		}
	}
	var steps [][]string
	if len(set) > 3 {
		steps = append(steps, set)
	}
	return append(steps, unset...)
}

// checkMemoryFits refuses a memory limit below what a running agent is using.
//
// Incus would take it: limits.memory is live-updatable, and it writes the new
// value straight into the cgroup's memory.max. The kernel is what makes this
// unsafe — "if a cgroup's memory usage reaches this limit and can't be
// reduced, the OOM killer is invoked in the cgroup" (cgroup-v2 docs). So the
// command would succeed and the agent's build, or its AI tool, would be killed
// a moment later with nothing to connect the two. AgentBox refuses instead.
func (m *Manager) checkMemoryFits(ctx context.Context, a state.Agent, memory string) error {
	if memory == "" {
		return nil // no ceiling at all: nothing to fall below
	}
	limit, err := ParseBytes(memory)
	if err != nil {
		return nil // a percentage of the host, checked by Incus itself
	}
	inst, err := m.Incus.Instance(ctx, a.Instance)
	if err != nil || inst.State == nil || displayState(inst.Status) != "running" {
		// Stopped, or unreadable: the limit applies at its next start, with
		// nothing running to kill.
		return nil
	}
	// The figure `free` shows inside the agent, without the page cache the
	// kernel drops on its own — that is what it can't reclaim its way out of.
	used := agentMemory(cgroupRoot, a.Instance, inst.State.Memory.Usage)
	if used <= 0 || limit >= used {
		return nil
	}
	return fmt.Errorf("%s is using %s, more than the %s limit you asked for: Incus would apply it and the kernel would kill processes inside the agent to get under it. Choose a limit above %s, or stop the agent first (agentbox stop %s) — a limit set while it's stopped applies when it starts, with nothing running to lose",
		a.Ref(), HumanBytes(used), memory, HumanBytes(used), a.Ref())
}

// HumanBytes renders a size the way AgentBox shows memory everywhere else.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for rest := n / unit; rest >= unit; rest /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
