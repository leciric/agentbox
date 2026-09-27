package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/mediapub"
)

// newMediaPublishCmd puts an agent's screenshots and recordings on the
// repository's agentbox-media branch and prints the Markdown that shows them
// in a pull request: GitHub has no API to attach a file to one (mediapub).
func newMediaPublishCmd(a *app) *cobra.Command {
	var remote string
	var noPush bool
	var names []string
	cmd := &cobra.Command{
		Use:   "publish [agent] --name <media>...",
		Short: "Push named screenshots or recordings to the agentbox-media branch, and print Markdown for a pull request",
		Long: `Commits the media named with --name (repeatable) to the repository's ` + mediapub.Branch + ` branch, under
a directory named after the agent's branch, pushes it, and prints Markdown for the
pull request's description: each screenshot inline, each recording as an inline GIF
preview linking to its mp4. Paste it into the pull request's body. The links go
through github.com, so they show on a private repository to whoever can read it.

A pull request's body should carry only what shows the result — one screenshot,
a couple at most for several distinct results — never everything you captured: the
rest stays in the Media tab, named in your report. Give --name that result's name.

Recordings are re-encoded smaller and their GIF previews kept to a few seconds and a
few megabytes, with ffmpeg; without it, files go as they are and recordings get no
preview. Publishing again replaces what the agent published before. Nothing but the
` + mediapub.Branch + ` branch on the remote changes: no local branch, no working tree.

Inside an agent, leave out the agent: it publishes its own media from its worktree.
On the host, name the agent: it publishes from the agent's worktree with your own
git credentials.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(names) == 0 {
				return errors.New("nothing to publish: pass --name <media> for what shows the result (see agentbox media list)")
			}
			ref, _ := splitRef(args, 0)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			repo, dir, err := publishTarget(ctx, c, ref)
			if err != nil {
				return err
			}
			all, err := c.Media(ctx, ref)
			if err != nil {
				return err
			}
			// Oldest first, the order the work was done in.
			slices.Reverse(all)
			var items []mediapub.Item
			matched := make(map[string]bool, len(names))
			for _, it := range all {
				if !slices.Contains(names, it.Name) {
					continue
				}
				matched[it.Name] = true
				item := mediapub.Item{ID: it.ID, Kind: it.Kind, Name: it.Name, Mime: it.Mime, Text: it.Text}
				if it.Kind != "note" {
					id := it.ID
					item.Open = func(ctx context.Context) (io.ReadCloser, error) { return c.MediaFile(ctx, ref, id) }
				}
				items = append(items, item)
			}
			for _, name := range names {
				if !matched[name] {
					return fmt.Errorf("no media named %q: see agentbox media list", name)
				}
			}
			result, err := mediapub.Publish(ctx, items, mediapub.Options{
				Repo: repo, Remote: remote, Dir: dir, NoPush: noPush,
				Warn: func(msg string) { _, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+msg) },
			})
			if err != nil {
				return err
			}
			verb := "Pushed"
			if noPush {
				verb = "Committed (not pushed)"
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s %d file(s) to %s as %.12s. Paste this into the pull request's description:\n\n",
				verb, len(result.Files), mediapub.Branch, result.Commit)
			_, _ = fmt.Fprint(cmd.OutOrStdout(), result.Markdown)
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "origin", "the remote to push to")
	cmd.Flags().BoolVar(&noPush, "no-push", false, "commit, but don't push")
	cmd.Flags().StringArrayVar(&names, "name", nil, "the name of media to publish (repeatable); required")
	return cmd
}

// publishTarget is the repository to publish from, and the directory on the
// media branch: the agent's worktree and branch, as the host knows them, or,
// inside an agent, the worktree it is standing in and that worktree's branch.
func publishTarget(ctx context.Context, c *api.Client, ref string) (repo, dir string, err error) {
	if ref != "" {
		ag, err := c.Agent(ctx, ref)
		if err != nil {
			return "", "", err
		}
		if _, err := os.Stat(ag.Worktree); err != nil {
			return "", "", fmt.Errorf("%s's worktree isn't on this machine (%s)", ref, ag.Worktree)
		}
		return ag.Worktree, ag.Branch, nil
	}
	out, err := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "HEAD").Output()
	branch := strings.TrimSpace(string(out))
	if err != nil || branch == "" {
		return "", "", errors.New("run it from your worktree, on your branch: the media goes under the branch's name")
	}
	return ".", branch, nil
}
