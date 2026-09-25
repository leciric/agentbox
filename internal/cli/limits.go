package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// newLimitsCmd shows or changes what one agent's machine is capped at, the way
// `agentbox autonomy` shows or changes a project's: no flags asks, any flag
// sets. Incus applies all three to a running instance, so nothing restarts.
func newLimitsCmd(a *app) *cobra.Command {
	var cpu, memory, allowance string
	cmd := &cobra.Command{
		Use:   "limits <project/agent> [--cpu N] [--memory X] [--cpu-allowance P]",
		Short: "Show or change what an agent's machine is capped at",
		Long: `Shows what an agent's machine is capped at, or changes it while the agent runs.

  --cpu             how many cores it gets, like 4. It sees exactly that many,
                    so make -j$(nproc) and vitest size themselves to it. Incus
                    picks which cores and re-balances them; nothing is pinned.
  --memory          a ceiling, like 8GiB. The kernel enforces it by killing
                    processes inside the agent, so a ceiling below what a
                    running agent is already using is refused.
  --cpu-allowance   its share of the CPUs: a percentage like 50%, which only
                    counts when the host is busy, or a chunk like 25ms/100ms,
                    a hard ceiling that counts even on an idle host.

Pass "" to remove one: --memory "" gives the agent all the host's memory.
New agents start at whatever the overview's "Resources for new agents" says.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			if !f.Changed("cpu") && !f.Changed("memory") && !f.Changed("cpu-allowance") {
				ag, err := c.Agent(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", ag.Ref, limitWords(ag.Limits))
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
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", ag.Ref, limitWords(ag.Limits))
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
	return cmd
}

// limitWords says what a set of limits means in one line. The wording is
// agent.Limits.Describe's, so the command line, the daemon's log and the
// message that refuses a memory limit all say it the same way.
func limitWords(l api.Limits) string {
	return agent.Limits{CPU: l.CPU, Allowance: l.Allowance, Memory: l.Memory}.Describe()
}
