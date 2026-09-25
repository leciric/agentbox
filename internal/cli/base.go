package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/image"
)

func newBaseCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "base",
		Short: "Save a set-up agent as the starting point for its project's new agents",
		Long: `Once an agent has set its project up (dependencies, runtimes, Docker images),
save its machine as the project's base: every new agent for the project then
starts from that machine instead of the plain base image. The worktree isn't part
of the base, and the agent's own logins, identity and AI sessions are removed.

A base is a photograph of one machine, and two of them can't be merged. You
update one by creating an agent from it, changing what needs changing, and
saving that machine. "agentbox base revert" goes back to the photograph the last
save replaced, which is kept for exactly one step.`,
	}
	cmd.AddCommand(newBaseSaveCmd(a), newBaseShowCmd(a), newBaseRevertCmd(a), newBaseRmCmd(a))
	return cmd
}

func newBaseSaveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "save <project/agent>",
		Short: "Save the agent's machine as its project's base",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, name, ok := strings.Cut(args[0], "/")
			if !ok || project == "" || name == "" {
				return fmt.Errorf("invalid agent %q: use <project>/<agent>", args[0])
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			start := time.Now()
			j, err := c.SaveBase(cmd.Context(), project, name)
			if err != nil {
				return err
			}
			if j, err = waitJob(cmd, c, j); err != nil {
				return err
			}
			var base api.Base
			if err := json.Unmarshal(j.Result, &base); err != nil {
				return fmt.Errorf("reading the job result: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nSaved %s as the base for %s in %s.\nNew %s agents start from %s (pass --clean to skip it).\n",
				args[0], project, time.Since(start).Round(100*time.Millisecond), project, base.Snapshot)
			if was, ok, err := c.Base(cmd.Context(), project); err == nil && ok && was.Previous != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "The base it replaced, saved from %s, is kept: agentbox base revert %s goes back to it.\n", was.Previous.SavedFrom, project)
			}
			return nil
		},
	}
}

func newBaseShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <project>",
		Short: "Show the project's saved base",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			base, ok, err := c.Base(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !ok {
				_, _ = fmt.Fprintf(out, "%s has no saved base: new agents start from %s.\n", args[0], image.SnapshotRef())
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintf(w, "base\t%s\n", base.Snapshot)
			_, _ = fmt.Fprintf(w, "saved from\t%s\n", base.SavedFrom)
			_, _ = fmt.Fprintf(w, "saved\t%s (%s)\n", base.SavedAt.Local().Format(time.DateTime), ago(base.SavedAt))
			if base.Previous != nil {
				_, _ = fmt.Fprintf(w, "previous\t%s, saved from %s (%s)\n", base.Previous.Snapshot, base.Previous.SavedFrom, ago(base.Previous.SavedAt))
			}
			return w.Flush()
		},
	}
}

// newBaseRevertCmd goes back to the base the last save replaced. Saving keeps
// it under another name, which costs a rename rather than a copy, so this is
// the one way back from a save that made things worse.
func newBaseRevertCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "revert <project>",
		Short: "Go back to the base the last save replaced",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			base, err := c.RevertBase(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is back on the base saved from %s (%s).\nThe base that replaced it is gone, and there is nothing left to revert to.\n",
				args[0], base.SavedFrom, ago(base.SavedAt))
			return nil
		},
	}
}

func newBaseRmCmd(a *app) *cobra.Command {
	var previous bool
	cmd := &cobra.Command{
		Use:   "rm <project>",
		Short: "Delete the project's saved base (existing agents are unaffected)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if previous {
				if err := c.RemovePreviousBase(cmd.Context(), args[0]); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Dropped what the last save of %s kept. Its base is unchanged, and there is nothing to revert to.\n", args[0])
				return nil
			}
			if err := c.RemoveBase(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed the base for %s: new agents start from %s.\n", args[0], image.SnapshotRef())
			return nil
		},
	}
	cmd.Flags().BoolVar(&previous, "previous", false, "delete only what the last save kept, giving its disk back")
	return cmd
}
