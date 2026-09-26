package agent

import (
	"context"
	"sort"
	"time"
)

// CPUUsageAgent is one agent's share of the host's CPU: its current use, and
// the cores it's carved out of — ConfiguredCores is what was chosen,
// EffectiveCores what actually applies right now, which "never freeze my
// CPU" (RecomputeCPUCaps, D95) may hold below it when the host is busy.
type CPUUsageAgent struct {
	Ref             string
	Title           string
	State           string
	CPU             float64 // percent; 100 is one full core
	ConfiguredCores string  // "" is every core
	EffectiveCores  string  // "" is every core
}

// CPUUsage is the host's CPU, broken down the way the "Host CPU" popover
// shows it: every agent, largest first, and what the host itself is using
// outside them.
type CPUUsage struct {
	HostCPU   float64 // percent of all cores
	HostCores int
	OtherCPU  float64 // percent used outside every agent
	Agents    []CPUUsageAgent
}

// CPUUsage samples the host and every agent over interval, the same way
// Usage does — it is Usage's own figures, reshaped for the popover.
func (m *Manager) CPUUsage(ctx context.Context, interval time.Duration) (CPUUsage, error) {
	host, agents, err := m.Usage(ctx, interval)
	if err != nil {
		return CPUUsage{}, err
	}
	rows := make([]CPUUsageAgent, 0, len(agents))
	var agentsCPU float64
	for _, a := range agents {
		rows = append(rows, CPUUsageAgent{
			Ref: a.Ref(), Title: a.Title, State: a.State, CPU: a.CPU,
			ConfiguredCores: a.Limits.ConfiguredCPU, EffectiveCores: a.Limits.CPU,
		})
		agentsCPU += a.CPU
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CPU > rows[j].CPU })
	return CPUUsage{HostCPU: host.CPU, HostCores: host.Cores, OtherCPU: max(host.CPU-agentsCPU, 0), Agents: rows}, nil
}
