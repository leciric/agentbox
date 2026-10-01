package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// newDiskCmd shows the disk guard, and sets its floor.
func newDiskCmd(a *app) *cobra.Command {
	var floor, percent string
	cmd := &cobra.Command{
		Use:   "disk [--floor SIZE] [--floor-percent P]",
		Short: "Show the free space AgentBox keeps on your disks, and set it",
		Long: `AgentBox keeps a floor of free space on every disk it writes to: the storage
pool its agents' machines live on, the worktrees, its own data, and in its VM
the host's disk that holds the VM's disk images. The floor is the larger of
10GiB and 5% of the disk unless you choose otherwise.

Nearing the floor, the app warns. At it, AgentBox refuses new agents, forks,
image builds and saved bases, keeps queued agents waiting, and pauses the
agents writing the most; it resumes them once there's room again. It never
stops or deletes anything.

  agentbox disk                        # where every disk stands
  agentbox disk --floor 20GiB          # keep at least 20GiB free
  agentbox disk --floor-percent 10     # and at least 10% of each disk
  agentbox disk --floor default --floor-percent default`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if floor != "" || percent != "" {
				var req api.UpdateSettingsRequest
				if floor != "" {
					n := int64(0)
					if floor != "default" {
						if n, err = agent.ParseBytes(floor); err != nil {
							return fmt.Errorf("--floor is a size like 20GiB, or default; got %q", floor)
						}
					}
					req.DiskFloorMin = &n
				}
				if percent != "" {
					p := -1.0
					if percent != "default" {
						if p, err = strconv.ParseFloat(strings.TrimSuffix(percent, "%"), 64); err != nil || p < 0 {
							return fmt.Errorf("--floor-percent is a percentage like 5, or default; got %q", percent)
						}
					}
					req.DiskFloorPercent = &p
				}
				settings, err := c.UpdateSettings(cmd.Context(), req)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Keeping free the larger of %s and %g%% of each disk.\n", humanBytes(settings.DiskFloorMin), settings.DiskFloorPercent)
				return nil
			}
			guard, err := c.DiskGuard(cmd.Context())
			if err != nil {
				return err
			}
			renderDiskGuard(cmd.OutOrStdout(), guard)
			return nil
		},
	}
	cmd.Flags().StringVar(&floor, "floor", "", `the least free space to keep on each disk, like 20GiB ("default": 10GiB)`)
	cmd.Flags().StringVar(&percent, "floor-percent", "", `the share of each disk to keep free, if more ("default": 5)`)
	return cmd
}

func renderDiskGuard(out io.Writer, g api.DiskGuard) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "DISK\tFREE\tSIZE\tFLOOR\tSTATE")
	for _, d := range g.Disks {
		label := d.Label
		if d.Path != "" {
			label += " (" + d.Path + ")"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", label, humanBytes(d.Free), humanBytes(d.Total), humanBytes(d.Floor), diskLevelWords(d.Level))
	}
	_ = tw.Flush()
	if g.Level != api.DiskOK {
		_, _ = fmt.Fprintln(out, "\n"+g.Message)
	}
	if g.Level == api.DiskFull {
		_, _ = fmt.Fprintln(out, "New agents, forks, image builds and saved bases are refused until there's room.")
	}
}

func diskLevelWords(level string) string {
	switch level {
	case api.DiskLow:
		return "nearing the floor"
	case api.DiskFull:
		return "at the floor"
	}
	return "ok"
}
