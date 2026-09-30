package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

func newQueueCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue [project]",
		Short: "Show the agent queue: each project's slots and the agents waiting for one",
		Long: `Shows how many agents each project may run at once (its slots) and the agents
queued for one. A queued agent (agentbox create --queue, or a project set to
always queue) has its name, title, branch and task, but no machine: the daemon
starts it when one of its project's slots is free, and never stops anything
running to make room.

Slots are auto unless pinned in the project's settings: the memory agents may
use, less a reserve, shared fairly between the projects with work, by what
each project's agents have been seen to peak at.

  agentbox queue                        # every project
  agentbox queue acme
  agentbox queue move acme/agent-12 1   # next in line
  agentbox queue remove acme/agent-12`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			project := ""
			if len(args) == 1 {
				project = args[0]
			}
			status, err := c.Queue(cmd.Context(), project)
			if err != nil {
				return err
			}
			printQueue(cmd.OutOrStdout(), status, project, time.Now())
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list [project]",
		Short: "Show the agent queue (the same as agentbox queue)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  cmd.RunE,
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "move <project/agent> <position>",
		Short: "Move a queued agent to a place in its project's queue, 1 for next",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			position, err := strconv.Atoi(args[1])
			if err != nil || position < 1 {
				return fmt.Errorf("the position is a place in line from 1, not %q", args[1])
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			status, err := c.MoveQueued(cmd.Context(), args[0], position)
			if err != nil {
				return err
			}
			project, _, _ := strings.Cut(args[0], "/")
			printQueue(cmd.OutOrStdout(), status, project, time.Now())
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:     "remove <project/agent>",
		Aliases: []string{"rm"},
		Short:   "Take a queued agent out of the queue before it starts",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveQueued(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from the queue\n", args[0])
			return nil
		},
	})
	return cmd
}

// printQueue writes the slots of every project, or of one, and the agents
// waiting for them.
func printQueue(out io.Writer, status api.QueueStatus, project string, now time.Time) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROJECT\tSLOTS\tRUNNING\tQUEUED\tPER AGENT")
	for _, p := range status.Projects {
		if project != "" && p.Project != project {
			continue
		}
		slots := fmt.Sprintf("%d (auto)", p.Slots)
		if p.Pinned > 0 {
			slots = fmt.Sprintf("%d (pinned)", p.Slots)
		}
		peak := humanBytes(p.Peak) + " peak"
		if !p.PeakLearned {
			peak = humanBytes(p.Peak) + " (not seen yet: its memory limit)"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n", p.Project, slots, p.Running, p.Queued, peak)
	}
	_ = w.Flush()
	_, _ = fmt.Fprintf(out, "\nAuto slots share %s: %s of memory for agents, less %s kept free.\n",
		humanBytes(max(status.Budget-status.Reserve, 0)), humanBytes(status.Budget), humanBytes(status.Reserve))
	if len(status.Queued) == 0 {
		_, _ = fmt.Fprintln(out, "Nothing is queued.")
		return
	}
	_, _ = fmt.Fprintln(out)
	w = tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "#\tAGENT\tTITLE\tBRANCH\tQUEUED")
	for _, q := range status.Queued {
		_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s ago\n", q.Position, q.Ref, dash(q.Title), q.Branch, now.Sub(q.QueuedAt).Round(time.Second))
	}
	_ = w.Flush()
}
