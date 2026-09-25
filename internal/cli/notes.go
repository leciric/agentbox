package cli

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"agentbox/internal/api"
)

func newNotesCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notes",
		Short: "What every agent of a project is told about it",
		Long: `A project's notes are folded into the brief of every one of its agents, so
what the project knows about itself is in the context window from the first
token instead of being explained again to each new agent.

Write what agents need and can't read off the repository: a gotcha about the
build, a convention, a decision you made. The project's chat adds what it
learns, under a heading of its own; it edits and removes entries only when you
ask it to in the chat, and says which ones it changed.`,
	}
	cmd.AddCommand(newNotesShowCmd(a), newNotesEditCmd(a), newNotesAppendCmd(a))
	return cmd
}

func newNotesShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <project>",
		Short: "Print a project's notes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			n, err := c.Notes(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if n.Text == "" {
				fmt.Fprintf(out, "%s has no notes yet. Write some with: agentbox notes edit %s\n", args[0], args[0])
				return nil
			}
			fmt.Fprint(out, n.Text)
			return nil
		},
	}
}

func newNotesEditCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <project>",
		Short: "Edit a project's notes in $EDITOR, or read them from stdin",
		Long: `Opens the notes in $EDITOR (or $VISUAL) and saves what you leave there.
With the notes piped in, it replaces them with what it reads:

    agentbox notes edit pawly < notes.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			n, err := c.Notes(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var text string
			if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
				if text, err = editInEditor(args[0], n.Text); err != nil {
					return err
				}
			} else {
				piped, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				// Emptying the notes is a thing to do on purpose, in the editor
				// or in the app, not something an empty pipe does by accident.
				if strings.TrimSpace(string(piped)) == "" {
					return fmt.Errorf("nothing came in on stdin: pipe the notes in (agentbox notes edit %s < notes.md), or run this on a terminal to open $EDITOR", args[0])
				}
				text = string(piped)
			}
			if strings.TrimSpace(text) == strings.TrimSpace(n.Text) {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s's notes are unchanged.\n", args[0])
				return nil
			}
			return saveNotes(cmd, c, args[0], text)
		},
	}
}

func newNotesAppendCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "append <project> <text>",
		Short: "Add a line to the end of a project's notes",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			n, err := c.Notes(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			text := strings.TrimSpace(args[1])
			if text == "" {
				return fmt.Errorf("nothing to add")
			}
			if n.Text != "" {
				text = strings.TrimRight(n.Text, "\n") + "\n\n" + text
			}
			return saveNotes(cmd, c, args[0], text)
		},
	}
}

func saveNotes(cmd *cobra.Command, c *api.Client, project, text string) error {
	n, err := c.SetNotes(cmd.Context(), project, text)
	if err != nil {
		return err
	}
	if n.Text == "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Cleared %s's notes. New agents are told nothing beyond the brief.\n", project)
		return nil
	}
	lines := strings.Count(strings.TrimRight(n.Text, "\n"), "\n") + 1
	unit := "lines"
	if lines == 1 {
		unit = "line"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Saved %s's notes (%d %s). Every agent of %s has them, and new ones start with them.\n",
		project, lines, unit, project)
	return nil
}

// editInEditor opens the notes in the user's editor and returns what they left
// there. The file is a temporary one: the notes themselves live where the
// daemon keeps them, which may be another machine (--env).
func editInEditor(project, text string) (string, error) {
	editor := cmp.Or(os.Getenv("VISUAL"), os.Getenv("EDITOR"))
	if editor == "" {
		return "", fmt.Errorf("no editor: set $EDITOR, or pipe the notes in (agentbox notes edit %s < notes.md)", project)
	}
	dir, err := os.MkdirTemp("", "agentbox-notes-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, project+"-notes.md")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", err
	}
	// The editor may be a command with arguments ("code --wait").
	fields := strings.Fields(editor)
	ed := exec.Command(fields[0], append(fields[1:], path)...)
	ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := ed.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", editor, err)
	}
	edited, err := os.ReadFile(path)
	return string(edited), err
}
