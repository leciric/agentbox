package agent

import (
	"context"
	"strconv"

	"agentbox/internal/state"
)

// NeverFreezeCPU reads the installation's "never freeze my CPU" setting
// (D95): whether RecomputeCPUCaps keeps every running agent's limits.cpu
// adding up to at most the host's cores minus keepFree.
func (m *Manager) NeverFreezeCPU(ctx context.Context) (on bool, keepFree int, err error) {
	on, err = m.Store.Flag(ctx, state.SettingNeverFreezeCPU)
	if err != nil {
		return false, 0, err
	}
	raw, err := m.Store.Setting(ctx, state.SettingKeepFreeCPU)
	if err != nil {
		return false, 0, err
	}
	keepFree = state.DefaultKeepFreeCPU
	if raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			keepFree = n
		}
	}
	return on, keepFree, nil
}

// RecomputeCPUCaps applies the installation's CPU budget to every running
// agent, host-wide, fresh each time: it is never told what changed, only
// asked to bring every running agent's limits.cpu to what the setting and
// each agent's own ConfiguredCPU say it should be right now. That is what
// lets it be called from as many places as an agent's CPU use can change —
// created, started, stopped, paused, resumed, destroyed, or the setting
// itself — without any of them having to know what the others last left
// behind.
//
// Turned off, it puts back exactly what ConfiguredCPU remembers, agent by
// agent — never the host's own defaults, and never another agent's limit.
func (m *Manager) RecomputeCPUCaps(ctx context.Context) error {
	on, keepFree, err := m.NeverFreezeCPU(ctx)
	if err != nil {
		return err
	}
	agents, err := m.Store.Agents(ctx, "")
	if err != nil {
		return err
	}
	instances, err := m.Incus.Instances(ctx)
	if err != nil {
		return err
	}
	byName := make(map[string]int, len(instances))
	for i, inst := range instances {
		byName[inst.Name] = i
	}

	type liveAgent struct {
		instance   string
		have       map[string]string
		current    Limits // its allowance and memory, left alone; only CPU moves
		configured string // "" is no limit, whether chosen or unknown
	}
	var live []liveAgent
	for _, a := range agents {
		i, ok := byName[a.Instance]
		if !ok || displayState(instances[i].Status) != "running" {
			continue
		}
		current := LimitsOf(instances[i].ExpandedConfig)
		live = append(live, liveAgent{
			instance:   a.Instance,
			have:       instances[i].Config,
			current:    current,
			configured: current.ConfiguredCPU,
		})
	}

	apply := func(instance, want string, have map[string]string, current Limits) error {
		current.CPU = want
		for _, args := range limitSteps(instance, current, have) {
			if _, err := m.Incus.Run(ctx, args...); err != nil {
				return err
			}
		}
		return nil
	}

	if !on {
		for _, r := range live {
			if err := apply(r.instance, r.configured, r.have, r.current); err != nil {
				return err
			}
		}
		return nil
	}

	budget := max(0, HostCores()-keepFree)
	ceilings := make([]int, len(live))
	for i, r := range live {
		if r.configured == "" {
			ceilings[i] = budget
			continue
		}
		n, err := strconv.Atoi(r.configured)
		if err != nil {
			n = budget
		}
		ceilings[i] = n
	}
	alloc := AllocateCPU(budget, ceilings)
	for i, r := range live {
		if err := apply(r.instance, strconv.Itoa(alloc[i]), r.have, r.current); err != nil {
			return err
		}
	}
	return nil
}

// AllocateCPU shares a CPU budget out among running agents, so the host never
// gets starved by the sum of what they're capped at (D95: "Never freeze my
// CPU").
//
// ceilings is each agent's own ceiling — its configured limits.cpu, or the
// whole budget when it has none — in any order; the result lines up with it
// index for index. If the ceilings all fit in the budget, each agent keeps
// its ceiling: this only takes CPUs away when there genuinely isn't enough to
// go round. Otherwise the budget is shared as evenly as possible: starting
// from each agent's ceiling, one CPU at a time is taken from whoever
// currently has the most, until the total fits — so agents with a smaller
// ceiling are never taken below it to spare one with a larger one, and every
// agent keeps at least 1 (the kernel sorts out the rest when there are more
// agents than the budget has room for).
func AllocateCPU(budget int, ceilings []int) []int {
	alloc := make([]int, len(ceilings))
	copy(alloc, ceilings)

	total := 0
	for _, c := range alloc {
		total += c
	}
	if total <= budget {
		return alloc
	}

	for total > budget {
		max := -1
		for i, c := range alloc {
			if c > 1 && (max < 0 || c > alloc[max]) {
				max = i
			}
		}
		if max < 0 {
			break // everyone is already at the floor
		}
		alloc[max]--
		total--
	}
	return alloc
}
