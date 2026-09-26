package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

func newTopCmd(a *app) *cobra.Command {
	var watch bool
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "top",
		Short: "Show CPU, memory and disk use for the host and every agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for {
				usage, err := c.Usage(cmd.Context(), interval)
				if err != nil {
					if watch && cmd.Context().Err() != nil {
						return nil
					}
					return err
				}
				if watch {
					_, _ = fmt.Fprint(out, "\x1b[H\x1b[2J")
				}
				if err := renderTop(out, usage); err != nil {
					return err
				}
				if !watch {
					return nil
				}
			}
		},
	}
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "keep refreshing until interrupted")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "how long each CPU sample lasts")
	return cmd
}

func renderTop(w io.Writer, u api.Usage) error {
	host := u.Host
	_, _ = fmt.Fprintf(w, "HOST   CPU %.0f%% of %d cores   MEMORY %s / %s   DISK POOL %s used, %s free\n\n",
		host.CPU, host.Cores, humanBytes(host.MemUsed), humanBytes(host.MemTotal),
		humanBytes(host.PoolUsed), humanBytes(host.PoolTotal-host.PoolUsed))
	if len(u.Agents) == 0 {
		_, _ = fmt.Fprintln(w, "No agents.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	// Two fractions, because they answer different questions. OF LIMIT is how
	// close this agent is to its own ceiling — the one that decides whether it
	// is the agent that needs more room. OF HOST is what it is costing the
	// machine everything else is sharing.
	_, _ = fmt.Fprintln(tw, "AGENT\tSTATE\tCPU\tOF LIMIT\tOF HOST\tMEMORY\tPROCESSES")
	for _, a := range u.Agents {
		ofLimit := "-" // uncapped: there is no limit to be a fraction of
		if a.Limits.CPU != "" && a.Cores > 0 {
			ofLimit = fmt.Sprintf("%.0f%% of %s cores", a.CPU/a.Cores, a.Limits.CPU)
			// "Never freeze my CPU" holding it below what it was chosen to be
			// is the fact worth a glance here, not the number alone.
			if a.Limits.ConfiguredCPU != "" && a.Limits.ConfiguredCPU != a.Limits.CPU {
				ofLimit += fmt.Sprintf(" (chosen: %s)", a.Limits.ConfiguredCPU)
			}
		}
		ofHost := "-"
		if host.Cores > 0 {
			ofHost = fmt.Sprintf("%.0f%%", a.CPU/float64(host.Cores))
		}
		memory := humanBytes(a.Memory) + " / no limit"
		if a.Limits.Memory != "" {
			memory = humanBytes(a.Memory) + " / " + a.Limits.Memory
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%.0f%%\t%s\t%s\t%s\t%d\n", a.Ref, a.State, a.CPU, ofLimit, ofHost, memory, a.Processes)
	}
	return tw.Flush()
}

// humanBytes is agent.HumanBytes, so a size reads the same in `agentbox top`
// as in the message that refuses a memory limit below what an agent is using.
func humanBytes(n int64) string { return agent.HumanBytes(n) }
