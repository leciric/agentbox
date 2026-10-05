package cli

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/snap"
)

// newSnapCmd takes a SnapShot. It runs on the user's machine even when
// AgentBox runs in a VM (cmd/agentbox), because the window is there.
func newSnapCmd(a *app) *cobra.Command {
	var full, noTree bool
	var project, save string
	cmd := &cobra.Command{
		Use:   "snap",
		Short: "Capture the active window and open the app's composer to send it as a bug report",
		Long: `Takes a SnapShot: a picture of the window you are in, with its app, its title and,
on Linux, a summary of its accessibility tree when the app exposes one. The
AgentBox app comes forward with a composer to add a note and send it to a
project's chat or to one of its agents.

Bind it to a key in your desktop. On Hyprland, in hyprland.conf:

  bind = SUPER SHIFT, S, exec, agentbox snap

Hyprland, Sway, KDE Plasma, GNOME (where it still allows window captures),
X11 and a Mac are supported; anywhere else, or when the window can't be had,
it captures the whole screen.

  agentbox snap                    # the active window
  agentbox snap --full             # the whole screen
  agentbox snap --save shot.png    # only save it, without the app`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := snap.Take(ctx, snap.System(runtime.GOOS), full)
			if err != nil {
				return err
			}
			if noTree {
				c.Accessibility = ""
			}
			if save != "" {
				if err := os.WriteFile(save, c.Image, 0o600); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved %s (%s)\n", save, describeSnap(c))
				return nil
			}
			if err := snap.Fit(&c, api.MaxChatImageBytes); err != nil {
				return err
			}
			client, err := a.client(cmd)
			if err != nil {
				return err
			}
			held, err := client.TakeSnap(ctx, api.SnapRequest{
				Image: api.ChatImageUpload{MimeType: c.MimeType, Data: base64.StdEncoding.EncodeToString(c.Image)},
				App:   c.App, Title: c.Title, Desktop: c.Desktop, Window: c.Window,
				Accessibility: c.Accessibility, TakenAt: c.TakenAt, Project: project,
			})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Captured %s: the AgentBox app's composer has it (%s)\n", describeSnap(c), held.ID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "capture the whole screen rather than the active window")
	cmd.Flags().BoolVar(&noTree, "no-accessibility", false, "leave out the window's accessibility tree")
	cmd.Flags().StringVar(&project, "project", "", "the project the composer starts on")
	cmd.Flags().StringVar(&save, "save", "", "save the picture to this file instead of sending it to the app")
	return cmd
}

func describeSnap(c snap.Capture) string {
	what := "the screen"
	if c.Window {
		what = "the window"
	}
	if c.App != "" || c.Title != "" {
		what += " " + strings.TrimSpace(fmt.Sprintf("%s %q", c.App, c.Title))
	}
	if c.Accessibility != "" {
		what += ", with its accessibility tree"
	}
	return what
}

// newBrowserCookiesCmd imports a cookie export into a project. Like snap it
// runs on the user's machine, where the export file is.
func newBrowserCookiesCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browser-cookies",
		Short: "Sign new agents' browsers in with cookies you export from your own browser",
		Long: `Imports the cookies of the domains you pick from an export you made of your own
browser: a Netscape cookies.txt, or the JSON a cookie extension (Cookie-Editor,
EditThisCookie) or Playwright's storageState writes. AgentBox never reads a
browser's own files.

The cookies are stored encrypted as a project secret, never shown again, and set
into the Chromium of agents created after the import, the first time it starts.
Importing again replaces them.

  agentbox browser-cookies import pawly cookies.txt --domain github.com --domain linear.app
  agentbox browser-cookies status pawly
  agentbox browser-cookies remove pawly`,
	}
	cmd.AddCommand(newBrowserCookiesImportCmd(a), newBrowserCookiesStatusCmd(a), newBrowserCookiesRemoveCmd(a))
	return cmd
}

func newBrowserCookiesImportCmd(a *app) *cobra.Command {
	var domains []string
	cmd := &cobra.Command{
		Use:   "import <project> <export | ->",
		Short: "Import the cookies of some domains from an export file (- for stdin)",
		Long:  `Without --domain, lists the sites the export has cookies for, and imports nothing.`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var data []byte
			var err error
			if args[1] == "-" {
				data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), 8<<20))
			} else {
				data, err = os.ReadFile(args[1])
			}
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(domains) == 0 {
				preview, err := c.PreviewBrowserCookies(cmd.Context(), args[0], string(data))
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(out, "The export (%s) has %d cookies:\n", preview.Format, preview.Cookies)
				for _, d := range preview.Domains {
					_, _ = fmt.Fprintf(out, "  %-40s %d\n", d.Domain, d.Cookies)
				}
				return errors.New("pick the domains to import with --domain: nothing was imported")
			}
			info, err := c.ImportBrowserCookies(cmd.Context(), args[0], string(data), domains)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Imported %d cookies of %s into %s: agents created from now on get them in their browser.\n", info.Cookies, strings.Join(info.Domains, ", "), args[0])
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&domains, "domain", nil, "a domain to keep cookies of, with its subdomains (repeat, or comma-separate)")
	return cmd
}

func newBrowserCookiesStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status <project>",
		Short: "Say which domains a project's imported cookies are for",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			info, err := c.BrowserCookies(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !info.Imported {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s has no imported browser cookies.\n", args[0])
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d cookies of %s, imported %s from a %s export.\n", info.Cookies, strings.Join(info.Domains, ", "), info.ImportedAt.Local().Format(time.DateTime), info.Format)
			return nil
		},
	}
}

func newBrowserCookiesRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <project>",
		Short: "Forget a project's imported cookies (agents that have them keep them)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveBrowserCookies(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s's imported browser cookies.\n", args[0])
			return nil
		},
	}
}
