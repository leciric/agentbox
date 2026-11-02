package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// newMediaSaveCmd copies one item's file out of the Media tab onto this
// machine: what an agent, whose media is kept on the host, needs before
// gh pr create --attach can upload a screenshot to a pull request.
func newMediaSaveCmd(a *app) *cobra.Command {
	var name string
	var raw bool
	cmd := &cobra.Command{
		Use:   "save [agent] <media> <path>",
		Short: "Copy a screenshot or recording out of the Media tab into a file here, to attach it to a pull request",
		Long: `Copies the file of a media item, given by its id or its name (the newest, when several
share the name; --name works too), to <path>, or into <path> under its own file name when <path> is a directory. Inside an agent,
leave out the agent: it saves its own media into the agent's machine. On the host, name
the agent.

It is what gh's --attach needs to put a screenshot in a pull request:

  agentbox media save empty-state /tmp/empty-state.png
  gh pr create --attach '/tmp/empty-state.png#The empty state' --body '![The empty state](/tmp/empty-state.png)' ...`,
		Example: `  agentbox media save empty-state /tmp/empty-state.png
  agentbox media save m12 /tmp/empty-state.png
  agentbox media save pawly/agent-01 empty-state /tmp`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The media is --name or the argument before the path; an agent
			// goes first when there is one more argument than that takes.
			want := 2
			if name != "" {
				want = 1
			}
			ref, rest := splitRef(args, want)
			// A lone agent is a forgotten path, not a file to write called pawly/agent-01.
			if len(rest) != want || ref == "" && refPattern.MatchString(rest[len(rest)-1]) {
				return errors.New("usage: agentbox media save [agent] <id or name> <path>")
			}
			if name == "" {
				name, rest = rest[0], rest[1:]
			}
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			items, err := c.Media(ctx, ref)
			if err != nil {
				return err
			}
			// Newest first, so the first match is the latest take.
			i := -1
			for n, it := range items {
				if it.ID == name || it.Name == name {
					i = n
					break
				}
			}
			if i < 0 {
				if info, err := os.Stat(rest[0]); err == nil && info.Mode().IsRegular() {
					return fmt.Errorf("no media with id or name %q: save copies out of Media, it doesn't store a file. To keep %s in Media first: agentbox media add %s --name %s", name, rest[0], rest[0], name)
				}
				return fmt.Errorf("no media with id or name %q: see agentbox media list", name)
			}
			item := items[i]
			switch {
			case item.Kind == "note":
				return fmt.Errorf("%q is a note, with no file to save", name)
			case item.Meta.Entry != "":
				return fmt.Errorf("%q is a directory: zip it, or save one screenshot of it instead", name)
			}
			dest := rest[0]
			if info, err := os.Stat(dest); err == nil && info.IsDir() {
				dest = filepath.Join(dest, filepath.Base(item.File))
			}
			open := c.MediaFile
			if raw {
				if item.Meta.Raw == "" {
					return fmt.Errorf("%q has no unedited original: it wasn't edited", name)
				}
				open = c.RawMediaFile
				if dest != rest[0] {
					dest = filepath.Join(rest[0], item.Meta.Raw)
				}
			}
			body, err := open(ctx, ref, item.ID)
			if err != nil {
				return err
			}
			defer func() { _ = body.Close() }()
			f, err := os.Create(dest)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, body); err != nil {
				_ = f.Close()
				_ = os.Remove(dest)
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved %s %q to %s\n", item.Kind, name, dest)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "the name of the media to save, instead of giving it before the path")
	cmd.Flags().BoolVar(&raw, "raw", false, "an edited recording's original, as it was recorded")
	return cmd
}
