package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// newLimitsCmd shows or changes what one agent's machine is capped at, the way
// `agentbox autonomy` shows or changes a project's: no flags asks, any flag
// sets. Incus applies all three to a running instance, so nothing restarts.
// With no agent at all, it is the installation's "never freeze my CPU"
// budget instead — every running agent's own cap, host-wide.
func newLimitsCmd(a *app) *cobra.Command {
	var cpu, memory, allowance string
	var neverFreezeCPU string
	var keepFreeCPU int
	var autoStopIdle, idleTime string
	cmd := &cobra.Command{
		Use:   "limits [project/agent] [--cpu N] [--memory X] [--cpu-allowance P]",
		Short: "Show or change what an agent's machine is capped at, or the host's own budget",
		Long: `Shows what an agent's machine is capped at, or changes it while the agent runs.

  --cpu             how many cores it gets, like 4. It sees exactly that many,
                    so make -j$(nproc) and vitest size themselves to it. Incus
                    picks which cores and re-balances them; nothing is pinned.
  --memory          a ceiling, like 8GiB. The kernel enforces it by killing
                    processes inside the agent, so a ceiling below what a
                    running agent is already using is refused. A capped
                    agent is kept out of the host's swap, so past its
                    ceiling it is killed rather than slowing the host down.
  --cpu-allowance   its share of the CPUs: a percentage like 50%, which only
                    counts when the host is busy, or a chunk like 25ms/100ms,
                    a hard ceiling that counts even on an idle host.

Pass "" to remove one: --memory "" gives the agent all the host's memory.
New agents start at whatever the overview's "Resources for new agents" says.

With no agent at all, this is "never freeze my CPU" instead: the setting
that keeps every running agent's own cap adding up to at most the host's
cores minus --keep-free, recomputed live as agents start, stop, are paused,
resumed, created or destroyed. An agent's own cap, above, is never raised
past by this — it only ever holds the sum of them down further.

  --never-freeze-cpu   on or off (true or false)
  --keep-free          how many cores stay outside every agent's cap; 1 by default

With no agent at all, --auto-stop-idle and --idle-time change "auto-stop idle
agents" instead: the daemon stops a running or paused agent once it has gone
--idle-time with nothing happening on it, keeping its worktree and branch.

  --auto-stop-idle   on or off (true or false); off by default
  --idle-time        how long an agent may go idle first, like 2h; 2h by default`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			if len(args) == 0 {
				return runHostLimits(cmd, c, f, neverFreezeCPU, keepFreeCPU, autoStopIdle, idleTime)
			}
			if !f.Changed("cpu") && !f.Changed("memory") && !f.Changed("cpu-allowance") {
				ag, err := c.Agent(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				printAgentLimits(cmd, ag)
				return nil
			}
			var req api.UpdateAgentRequest
			// Typed or not, rather than empty or not: "" is what removes a cap.
			if f.Changed("cpu") {
				req.CPU = &cpu
			}
			if f.Changed("memory") {
				req.Memory = &memory
			}
			if f.Changed("cpu-allowance") {
				req.CPUAllowance = &allowance
			}
			ag, err := c.UpdateAgent(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			printAgentLimits(cmd, ag)
			if ag.State != "running" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "It is %s: the new limits apply when it starts.\n", ag.State)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&cpu, "cpu", "", `how many cores the agent gets, like 4 ("" for every core)`)
	f.StringVar(&memory, "memory", "", `how much memory the agent gets, like 8GiB ("" for all of it)`)
	f.StringVar(&allowance, "cpu-allowance", "", `the agent's share of the CPUs, like 50% or 25ms/100ms ("" for all of it)`)
	f.StringVar(&neverFreezeCPU, "never-freeze-cpu", "", "turn the host's own CPU budget on or off (true or false)")
	f.IntVar(&keepFreeCPU, "keep-free", -1, "how many cores the host's own budget keeps free (at least 0)")
	f.StringVar(&autoStopIdle, "auto-stop-idle", "", "turn \"auto-stop idle agents\" on or off (true or false)")
	f.StringVar(&idleTime, "idle-time", "", "how long an agent may go idle before it is stopped, like 2h")
	return cmd
}

// runHostLimits is `agentbox limits` with no agent: the installation's own
// "never freeze my CPU" budget, shown or changed the same no-flags-asks,
// any-flag-sets way as one agent's.
func runHostLimits(cmd *cobra.Command, c *api.Client, f *pflag.FlagSet, neverFreezeCPU string, keepFreeCPU int, autoStopIdle, idleTime string) error {
	if f.Changed("never-freeze-cpu") || f.Changed("keep-free") || f.Changed("auto-stop-idle") || f.Changed("idle-time") {
		var req api.UpdateSettingsRequest
		if f.Changed("never-freeze-cpu") {
			on, err := strconv.ParseBool(neverFreezeCPU)
			if err != nil {
				return fmt.Errorf("--never-freeze-cpu is true or false; %q isn't", neverFreezeCPU)
			}
			req.NeverFreezeCPU = &on
		}
		if f.Changed("keep-free") {
			if keepFreeCPU < 0 {
				return fmt.Errorf("--keep-free is a whole number of cores, at least 0; %d isn't", keepFreeCPU)
			}
			req.KeepFreeCPU = &keepFreeCPU
		}
		if f.Changed("auto-stop-idle") {
			on, err := strconv.ParseBool(autoStopIdle)
			if err != nil {
				return fmt.Errorf("--auto-stop-idle is true or false; %q isn't", autoStopIdle)
			}
			req.AutoStopIdle = &on
		}
		if f.Changed("idle-time") {
			d, err := time.ParseDuration(idleTime)
			if err != nil || d < 60*time.Second {
				return fmt.Errorf("--idle-time is a duration of at least 60s, like 2h; %q isn't", idleTime)
			}
			secs := int(d / time.Second)
			req.IdleTimeSeconds = &secs
		}
		settings, err := c.UpdateSettings(cmd.Context(), req)
		if err != nil {
			return err
		}
		return printHostLimits(cmd, settings)
	}
	settings, err := c.Settings(cmd.Context())
	if err != nil {
		return err
	}
	return printHostLimits(cmd, settings)
}

func printHostLimits(cmd *cobra.Command, settings api.Settings) error {
	state := "off"
	if settings.NeverFreezeCPU {
		state = fmt.Sprintf("on, keeping %d core(s) free of this host's %d", settings.KeepFreeCPU, settings.HostCores)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "never freeze my CPU: %s\n", state); err != nil {
		return err
	}
	idleState := "off"
	if settings.AutoStopIdle {
		idleState = fmt.Sprintf("on, stopping an agent idle for %s", time.Duration(settings.IdleTimeSeconds)*time.Second)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "auto-stop idle agents: %s\n", idleState)
	return err
}

// limitWords says what a set of limits means in one line. The wording is
// agent.Limits.Describe's, so the command line, the daemon's log and the
// message that refuses a memory limit all say it the same way.
func limitWords(l api.Limits) string {
	return agent.Limits{CPU: l.CPU, Allowance: l.Allowance, Memory: l.Memory}.Describe()
}

// printAgentLimits shows an agent's limits, and, when "never freeze my CPU"
// is holding its CPU cap below what it was actually chosen to be, says so —
// the choice, in ConfiguredCPU, is never what changed.
func printAgentLimits(cmd *cobra.Command, ag api.Agent) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", ag.Ref, limitWords(ag.Limits))
	if l := ag.Limits; l.ConfiguredCPU != "" && l.ConfiguredCPU != l.CPU {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  held to %s cores by \"never freeze my CPU\": chosen at %s\n", l.CPU, l.ConfiguredCPU)
	}
}
