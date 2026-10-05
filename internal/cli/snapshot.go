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
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Snapshot %s@%s taken in %s\n", args[0], s.Name, time.Since(start).Round(10*time.Millisecond))
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
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted snapshot %s@%s\n", args[0], args[1])
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
			_, _ = fmt.Fprintln(w, "SNAPSHOT\tTAKEN\tBRANCH AT")
			for _, s := range snapshots {
				_, _ = fmt.Fprintf(w, "%s\t%s (%s)\t%s\n", s.Name, s.CreatedAt.Local().Format(time.DateTime), ago(s.CreatedAt), short(s.Head))
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
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Restored %s to %s in %s\n", args[0], args[1], time.Since(start).Round(100*time.Millisecond))
			return nil
		},
	}
}

func newCheckpointsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "checkpoints <project/agent>",
		Short: "List the checkpoints of an agent's chat turns, to roll back to or fork from",
		Long: `List the checkpoints of an agent's chat turns, oldest first.

Every turn of an agent's chat ends with one: its worktree's files, uncommitted
ones included, without touching its branch. The latest 50 turns are kept.
"saved-…" checkpoints are what the worktree had before a rollback: they can be
forked from but not rolled back to.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			list, err := c.Checkpoints(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if len(list) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s has no checkpoints yet: one is taken when each chat turn ends\n", args[0])
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "CHECKPOINT\tTAKEN\tBRANCH AT\tTURN")
			for _, cp := range list {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cp.ID, ago(cp.CreatedAt), short(cp.Head), promptLine(cp.Prompt))
			}
			return w.Flush()
		},
	}
}

func newRollbackCmd(a *app) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rollback <project/agent> <checkpoint|turn>",
		Short: "Put an agent's worktree and chat back to the end of one of its turns",
		Long: `Put an agent's worktree and chat back to the end of one of its turns.

The turns after it leave the conversation, the files go back to what they were
then, and the AI tool's session starts again, told the conversation up to there.
What the worktree has now is saved first as a checkpoint you can fork from.
Refused while a turn runs.`,
		Example: `  agentbox rollback pawly/agent-01 3
  agentbox rollback pawly/agent-01 turn-3 --yes`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if !yes {
				ok, err := confirmPrompt(cmd, fmt.Sprintf("Roll %s back to %s? The turns after it leave its chat, and its files go back.", args[0], args[1]))
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Nothing rolled back")
					return nil
				}
			}
			res, err := c.Rollback(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rolled %s back to %s. What it had is saved as %s: agentbox fork %s --checkpoint %s\n",
				args[0], args[1], res.Saved.ID, args[0], res.Saved.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask first")
	return cmd
}

// promptLine fits the start of a turn's message in a table's last column.
func promptLine(s string) string {
	if r := []rune(oneLine(s)); len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return oneLine(s)
}

func newForkCmd(a *app) *cobra.Command {
	var name, title, checkpoint string
	cmd := &cobra.Command{
		Use:   "fork <project/agent>[@snapshot]",
		Short: "Create a new agent from another agent's current state, one of its snapshots or one of its turns",
		Long: `Create a new agent from another agent's current state, one of its snapshots,
or, with --checkpoint, the end of one of its chat turns: its branch starts from
the files as they were then, and its chat from the conversation up to there.`,
		Example: `  agentbox fork pawly/agent-01
  agentbox fork pawly/agent-01@before-refactor
  agentbox fork pawly/agent-01 --checkpoint 3`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, snapshot, _ := strings.Cut(args[0], "@")
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			start := time.Now()
			j, err := c.Fork(cmd.Context(), ref, api.ForkRequest{Name: name, Title: title, Snapshot: snapshot, Checkpoint: checkpoint})
			if err != nil {
				return err
			}
			return finishAgentJob(cmd, c, j, start)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name of the new agent (default: the next free agent-NN)")
	cmd.Flags().StringVar(&title, "title", "", `title of the new agent (default: the source's title, with " (fork)")`)
	cmd.Flags().StringVar(&checkpoint, "checkpoint", "", "fork from a checkpoint (agentbox checkpoints), or a turn's number")
	return cmd
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
