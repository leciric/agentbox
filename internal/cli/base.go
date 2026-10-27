package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/agentimage"
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
save replaced, which is kept for exactly one step.

An agent on a Kubernetes cluster has no machine on this host to photograph: its
base is its own container image, built from the project's
.agentbox/agent.Dockerfile on top of the agent image the cluster runs and pushed
to the cluster's registry. "agentbox base save" builds it where the project's
checkout and Docker are, which is this machine, and records what the registry
stored; "base show", "base revert" and "base rm" handle it beside a machine
base.`,
	}
	cmd.AddCommand(newBaseSaveCmd(a), newBaseShowCmd(a), newBaseRevertCmd(a), newBaseRmCmd(a))
	return cmd
}

func newBaseSaveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "save <project/agent>",
		Short: "Save its project's base: the agent's machine, or its image on a cluster",
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
			// A cluster agent's base is an image, not a machine to copy: it is
			// built where the project's checkout and Docker are, which is this
			// machine, and the daemon only records what the registry stored.
			ag, err := c.Agent(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if ag.Cluster != "" {
				return saveProjectImage(cmd, c, ag)
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

// saveProjectImage is `base save` for an agent in a cluster: it builds the
// project's own image from its .agentbox/agent.Dockerfile on top of the agent
// image the cluster runs, pushes it to the cluster's registry, and records what
// the registry stored so every pod of the project runs it.
func saveProjectImage(cmd *cobra.Command, c *api.Client, ag api.Agent) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	p, err := c.Project(ctx, ag.Project)
	if err != nil {
		return err
	}
	cluster, err := c.Cluster(ctx, ag.Cluster)
	if err != nil {
		return err
	}
	if cluster.Registry == "" {
		return fmt.Errorf("cluster %s has no registry: a project image has to be pushed where its pods can pull it. Add one with agentbox registry add, then give it to the cluster with agentbox cluster set %s --registry <name>", cluster.Name, cluster.Name)
	}
	reg, err := c.Registry(ctx, cluster.Registry)
	if err != nil {
		return err
	}
	if reg.URL == "" {
		return fmt.Errorf("registry %s has no address: add it again with agentbox registry add", reg.Name)
	}
	dockerfile := filepath.Join(p.Root, agentimage.ProjectDockerfile)
	if _, err := os.Stat(dockerfile); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return fmt.Errorf("%s has no %s (its checkout, as the daemon sees it, is %s): a project on a cluster saves its base as an image, built from a Dockerfile in the project. Write one that starts with FROM agentbox-agent, then run this again", ag.Project, agentimage.ProjectDockerfile, p.Root)
	}
	prefix := strings.TrimSuffix(reg.URL, "/")
	base := prefix + "/" + cluster.Image
	tag := prefix + "/" + ag.Project + "-agent:" + time.Now().UTC().Format("20060102-150405")
	start := time.Now()
	_, _ = fmt.Fprintf(out, "Building %s from %s on top of %s\n", tag, p.Root, base)
	if err := agentimage.BuildProject(ctx, agentimage.ProjectOptions{Root: p.Root, Base: base, Image: tag}); err != nil {
		return err
	}
	digest, err := agentimage.Push(ctx, tag)
	if err != nil {
		return err
	}
	saved, err := c.SetProjectImage(ctx, ag.Project, api.ProjectImageRequest{
		Image: tag, Digest: strings.TrimPrefix(digest, "@"), Agent: ag.Ref,
	})
	if err != nil {
		return err
	}
	ref := saved.Image
	if saved.Digest != "" {
		ref += "@" + saved.Digest
	}
	_, _ = fmt.Fprintf(out, "\nSaved %s as the base image for %s in %s.\nIts agents of cluster %s run it: new ones as they are made, and the others when their pod next starts.\nTo replace a running pod now: agentbox recreate <agent>.\n",
		ref, ag.Project, time.Since(start).Round(100*time.Millisecond), cluster.Name)
	return nil
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
				_, _ = fmt.Fprintf(out, "%s has no saved base: agents on this host start from %s, and a cluster agent runs its cluster's image.\n", args[0], image.SnapshotRef())
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			if base.Snapshot != "" {
				_, _ = fmt.Fprintf(w, "base\t%s\n", base.Snapshot)
				_, _ = fmt.Fprintf(w, "saved from\t%s\n", base.SavedFrom)
				_, _ = fmt.Fprintf(w, "saved\t%s (%s)\n", base.SavedAt.Local().Format(time.DateTime), ago(base.SavedAt))
				if base.Image != "" {
					_, _ = fmt.Fprintf(w, "image\t%s, tools %s\n", base.Image, orUnknown(base.Tools))
				}
				if base.Previous != nil {
					_, _ = fmt.Fprintf(w, "previous\t%s, saved from %s (%s)\n", base.Previous.Snapshot, base.Previous.SavedFrom, ago(base.Previous.SavedAt))
				}
			}
			if img := base.Project; img != nil {
				_, _ = fmt.Fprintf(w, "project image\t%s\n", img.Image)
				_, _ = fmt.Fprintf(w, "project image digest\t%s\n", orUnknown(img.Digest))
				_, _ = fmt.Fprintf(w, "project image saved from\t%s\n", img.SavedFrom)
				_, _ = fmt.Fprintf(w, "project image saved\t%s (%s)\n", img.SavedAt.Local().Format(time.DateTime), ago(img.SavedAt))
				if img.Previous != nil {
					_, _ = fmt.Fprintf(w, "previous project image\t%s, saved from %s (%s)\n", img.Previous.Image, img.Previous.SavedFrom, ago(img.Previous.SavedAt))
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if base.Snapshot != "" && base.Behind != nil {
				_, _ = fmt.Fprintf(out, "\nThe base image has moved on since this base was saved:\n%s"+
					"Refresh it: agentbox create %s --catch-up makes an agent from it and catches its machine up; check the project works in it, then save it with agentbox base save.\n", describeBehind(base.Behind), args[0])
			}
			return nil
		},
	}
}

// newBaseRevertCmd goes back to the base the last save replaced. Saving keeps
// it under another name, which costs a rename rather than a copy, so this is
// the one way back from a save that made things worse. A project image keeps
// the one it replaced in the database, and goes back the same way.
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
			ctx := cmd.Context()
			var reverted []string
			if base, err := c.RevertBase(ctx, args[0]); err == nil {
				reverted = append(reverted, fmt.Sprintf("the base saved from %s (%s)", base.SavedFrom, ago(base.SavedAt)))
			} else if !api.IsNotFound(err) {
				return err
			}
			if img, err := c.RevertProjectImage(ctx, args[0]); err == nil {
				reverted = append(reverted, fmt.Sprintf("the image saved from %s (%s)", img.SavedFrom, ago(img.SavedAt)))
			} else if !api.IsNotFound(err) {
				return err
			}
			if len(reverted) == 0 {
				return fmt.Errorf("%s has no previous base to go back to", args[0])
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is back on %s.\nWhat replaced it is gone, and there is nothing left to revert to.\n",
				args[0], strings.Join(reverted, " and "))
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
			ctx := cmd.Context()
			var removed []string
			remove := c.RemoveBase
			if previous {
				remove = c.RemovePreviousBase
			}
			if err := remove(ctx, args[0]); err == nil {
				if previous {
					removed = append(removed, "the base its last save kept")
				} else {
					removed = append(removed, "its machine base")
				}
			} else if !api.IsNotFound(err) {
				return err
			}
			if err := c.RemoveProjectImage(ctx, args[0], previous); err == nil {
				if previous {
					removed = append(removed, "the image its last save kept")
				} else {
					removed = append(removed, "its image")
				}
			} else if !api.IsNotFound(err) {
				return err
			}
			if len(removed) == 0 {
				if previous {
					return fmt.Errorf("%s has no previous base", args[0])
				}
				return fmt.Errorf("%s has no saved base", args[0])
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Removed %s for %s.\n", strings.Join(removed, " and "), args[0])
			if !previous {
				_, _ = fmt.Fprintf(out, "Agents on this host start from %s again, and its cluster agents run their cluster's image.\n", image.SnapshotRef())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&previous, "previous", false, "delete only what the last save kept, giving its disk back")
	return cmd
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// describeBehind lists what a base is behind the image on, a line each.
func describeBehind(b *api.BaseBehind) string {
	var out strings.Builder
	switch {
	case b.ImageTo != "" && b.ImageFrom == "":
		fmt.Fprintf(&out, "  image: unknown → %s\n", b.ImageTo)
	case b.ImageTo != "":
		fmt.Fprintf(&out, "  image: %s → %s\n", b.ImageFrom, b.ImageTo)
	}
	for _, c := range b.Changes {
		fmt.Fprintf(&out, "    %s: %s\n", c.Version, c.What)
	}
	if len(b.Components) > 0 {
		fmt.Fprintf(&out, "  new in the image: %s\n", strings.Join(b.Components, ", "))
	}
	if b.ToolsUnknown {
		out.WriteString("  tools: not recorded; a catch-up installs every pinned one\n")
	}
	for _, t := range b.Tools {
		switch {
		case t.From == "":
			fmt.Fprintf(&out, "  %s: new, %s\n", t.Name, t.To)
		case t.To == "":
			fmt.Fprintf(&out, "  %s: %s, no longer pinned\n", t.Name, t.From)
		default:
			fmt.Fprintf(&out, "  %s: %s → %s\n", t.Name, t.From, t.To)
		}
	}
	return out.String()
}
