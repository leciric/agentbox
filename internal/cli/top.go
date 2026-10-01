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
	_, _ = fmt.Fprintf(w, "       DISK IO read %s/s, write %s/s", humanBytes(host.DiskRead), humanBytes(host.DiskWrite))
	if p := host.Pressure; p != nil {
		// Full, not some: the share of time nothing at all could run, which
		// is what the desktop freezing feels like.
		_, _ = fmt.Fprintf(w, "   STALLED io %.0f%%, memory %.0f%%", p.IOFull, p.MemoryFull)
		if p.Stalling {
			_, _ = fmt.Fprint(w, " (the host is stalling)")
		}
	}
	_, _ = fmt.Fprint(w, "\n\n")
	if len(u.Agents) == 0 {
		_, _ = fmt.Fprintln(w, "No agents.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	// CPU is in cores' worth, 100% a core; OF HOST is what it is costing the
	// machine everything else is sharing.
	_, _ = fmt.Fprintln(tw, "AGENT\tSTATE\tCPU\tOF HOST\tMEMORY\tDISK IO\tPROCESSES")
	for _, a := range u.Agents {
		ofHost := "-"
		if host.Cores > 0 {
			ofHost = fmt.Sprintf("%.0f%%", a.CPU/float64(host.Cores))
		}
		disk := fmt.Sprintf("%s/s read, %s/s write", humanBytes(a.DiskRead), humanBytes(a.DiskWrite))
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%.0f%%\t%s\t%s\t%s\t%d\n", a.Ref, a.State, a.CPU, ofHost, humanBytes(a.Memory), disk, a.Processes)
	}
	return tw.Flush()
}

// humanBytes is agent.HumanBytes, so a size reads the same in `agentbox top`
// as everywhere else AgentBox shows one.
func humanBytes(n int64) string { return agent.HumanBytes(n) }
