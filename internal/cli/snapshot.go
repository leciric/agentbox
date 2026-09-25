package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

func newSnapshotCmd(a *app) *cobra.Command {
	var consistent bool
	cmd := &cobra.Command{
		Use:   "snapshot <project/agent> [name]",
		Short: "Snapshot an agent's machine and worktree, uncommitted files included",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.SnapshotRequest{Consistent: consistent}
			if len(args) == 2 {
				req.Name = args[1]
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			start := time.Now()
			s, err := c.TakeSnapshot(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Snapshot %s@%s taken in %s\n", args[0], s.Name, time.Since(start).Round(10*time.Millisecond))
			return nil
		},
	}
	cmd.Flags().BoolVar(&consistent, "consistent", false, "pause the agent while the snapshot is taken")
	cmd.AddCommand(&cobra.Command{
		Use:   "rm <project/agent> <name>",
		Short: "Delete a snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.DeleteSnapshot(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted snapshot %s@%s\n", args[0], args[1])
			return nil
		},
	})
	return cmd
}

func newSnapshotsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "snapshots <project/agent>",
		Short: "List an agent's snapshots",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			snapshots, err := c.Snapshots(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "SNAPSHOT\tTAKEN\tBRANCH AT")
			for _, s := range snapshots {
				fmt.Fprintf(w, "%s\t%s (%s)\t%s\n", s.Name, s.CreatedAt.Local().Format(time.DateTime), ago(s.CreatedAt), short(s.Head))
			}
			return w.Flush()
		},
	}
}

func newRestoreCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "restore <project/agent> <snapshot>",
		Short: "Put an agent back to a snapshot: machine, branch and files",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			start := time.Now()
			j, err := c.Restore(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			if _, err := waitJob(cmd, c, j); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Restored %s to %s in %s\n", args[0], args[1], time.Since(start).Round(100*time.Millisecond))
			return nil
		},
	}
}

func newForkCmd(a *app) *cobra.Command {
	var name, title string
	cmd := &cobra.Command{
		Use:   "fork <project/agent>[@snapshot]",
		Short: "Create a new agent from another agent's current state or one of its snapshots",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, snapshot, _ := strings.Cut(args[0], "@")
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			start := time.Now()
			j, err := c.Fork(cmd.Context(), ref, api.ForkRequest{Name: name, Title: title, Snapshot: snapshot})
			if err != nil {
				return err
			}
			return finishAgentJob(cmd, c, j, start)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name of the new agent (default: the next free agent-NN)")
	cmd.Flags().StringVar(&title, "title", "", `title of the new agent (default: the source's title, with " (fork)")`)
	return cmd
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
