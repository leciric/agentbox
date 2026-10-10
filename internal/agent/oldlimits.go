package agent

import (
	"context"
	"path/filepath"
	"strings"

	"agentbox/internal/incus"
)

// Earlier releases capped each agent's machine — its cores, its share of the
// CPUs, its memory, a CPU priority below the desktop's, and its swap — and
// could put every agent under one shared cgroup budget, all to keep agents
// from freezing the desktop of the host they ran on; and "GPU for agents"
// passed the host's GPU into each machine. AgentBox now runs in a VM of its
// own, whose size is what the agents share and which has no GPU to pass on,
// so it sets none of that.
//
// A machine made by an earlier release still carries those keys, and so does
// a snapshot of one, which brings them back when it is restored, or a copy of
// one, as a fork or a project base is. A capped machine would stay capped
// with nothing in the app saying so, so the daemon takes them off: from every
// agent when it starts (DropOldLimits), and from one machine whenever it is
// made, started or restored (oldLimitSteps).
//
// Except limits.cpu: it holds each agent's share of the VM's cores now
// (cpushare.go), which the daemon sets as agents start and stop. An earlier
// release's value is replaced by the share rather than taken off: as a machine
// starts (setStartShare), and on every running one as the daemon balances
// them, the first time just after it starts.

// oldLimitKeys are the Incus keys earlier releases set on an agent's instance.
var oldLimitKeys = []string{
	"limits.cpu.allowance", "limits.memory", "limits.cpu.priority", "limits.memory.swap",
	"user.agentbox.cpu.configured", "user.agentbox.cpu.unlimited",
	// "GPU for agents" ran an NVIDIA host's agents through its container
	// runtime, beside oldGPUDevice.
	"nvidia.runtime",
}

// oldGPUDevice is the device "GPU for agents" added to every agent's machine.
const oldGPUDevice = "agentbox-gpu"

// oldBudgetCgroup is the shared budget's parent cgroup, under the cgroup root:
// a machine that ran inside it keeps running there until it restarts.
const oldBudgetCgroup = "agentbox"

// OldBudgetDir is that cgroup's directory. A variable for tests, which point
// it at a directory of their own; nothing else changes it.
var OldBudgetDir = filepath.Join(cgroupRoot, oldBudgetCgroup)

// An incusStep is one change to an instance, made later: steps are worked out
// from what an instance has, then made one after another.
type incusStep func(context.Context, incus.Client) error

func setConfig(instance string, pairs ...string) incusStep {
	return func(ctx context.Context, c incus.Client) error { return c.SetConfig(ctx, instance, pairs...) }
}

func unsetConfig(instance, key string) incusStep {
	return func(ctx context.Context, c incus.Client) error { return c.UnsetConfig(ctx, instance, key) }
}

// oldLimitSteps are the changes that take what an earlier release set off an
// instance, from have and devices, its own configuration and devices (not the
// expanded ones: what a profile sets isn't AgentBox's to take off). The limits
// and the device come off live; the shared budget's placement in raw.lxc
// applies when the machine next starts.
func oldLimitSteps(instance string, have map[string]string, devices map[string]map[string]string) []incusStep {
	var steps []incusStep
	if _, ok := devices[oldGPUDevice]; ok {
		steps = append(steps, func(ctx context.Context, c incus.Client) error { return c.RemoveDevice(ctx, instance, oldGPUDevice) })
	}
	for _, key := range oldLimitKeys {
		if _, ok := have[key]; ok {
			steps = append(steps, unsetConfig(instance, key))
		}
	}
	switch want := withoutBudgetPlacement(have["raw.lxc"]); want {
	case have["raw.lxc"]:
	case "":
		steps = append(steps, unsetConfig(instance, "raw.lxc"))
	default:
		steps = append(steps, setConfig(instance, "raw.lxc="+want))
	}
	return steps
}

// withoutBudgetPlacement is raw.lxc without the lines that put a machine in
// the shared budget's cgroup.
func withoutBudgetPlacement(rawLXC string) string {
	var lines []string
	for _, line := range strings.Split(rawLXC, "\n") {
		key, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		if strings.HasPrefix(strings.TrimSpace(key), "lxc.cgroup.dir.") || strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// dropOldLimits takes what an earlier release set off one instance.
func (m *Manager) dropOldLimits(ctx context.Context, instance string) error {
	d, err := m.Incus.Details(ctx, instance)
	if err != nil {
		return err
	}
	for _, step := range oldLimitSteps(instance, d.Config, d.Devices) {
		if err := step(ctx, m.Incus); err != nil {
			return err
		}
	}
	return nil
}

// DropOldLimits takes what earlier releases set off every agent's machine,
// and reports how many it changed. A machine that already has none of it is
// left alone, so after the first start this is one `incus list`.
func (m *Manager) DropOldLimits(ctx context.Context) (changed int, err error) {
	agents, err := m.Store.Agents(ctx, "")
	if err != nil || len(agents) == 0 {
		return 0, err
	}
	instances, err := m.Incus.Instances(ctx)
	if err != nil {
		return 0, err
	}
	ours := make(map[string]bool, len(agents))
	for _, a := range agents {
		ours[a.Instance] = true
	}
	for _, inst := range instances {
		if !ours[inst.Name] {
			continue
		}
		steps := oldLimitSteps(inst.Name, inst.Config, inst.Devices)
		for _, step := range steps {
			if err := step(ctx, m.Incus); err != nil {
				return changed, err
			}
		}
		if len(steps) > 0 {
			changed++
		}
	}
	return changed, nil
}
