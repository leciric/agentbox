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

// newBrowserCookiesCmd signs new agents' browsers in with the user's cookies,
// either straight from an installed browser or from an export they made. Like
// snap it runs on the user's machine.
func newBrowserCookiesCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browser-cookies",
		Short: "Sign new agents' browsers in with cookies from your own browser",
		Long: `Signs the browser of agents created from now on in to the sites you use, with
cookies from your own browser. Pick one of your installed browsers to import
every cookie of it (browsers / from-browser), or import the domains you pick
from an export file (import): a Netscape cookies.txt, or the JSON a cookie
extension (Cookie-Editor, EditThisCookie) or Playwright's storageState writes.

The cookies are stored encrypted as a project secret, never shown again, and set
into the Chromium of agents created after the import, the first time it starts.
Importing again replaces them.

  agentbox browser-cookies browsers pawly
  agentbox browser-cookies from-browser pawly chrome:Default
  agentbox browser-cookies import pawly cookies.txt --domain github.com --domain linear.app
  agentbox browser-cookies status pawly
  agentbox browser-cookies remove pawly`,
	}
	cmd.AddCommand(newBrowserCookiesBrowsersCmd(a), newBrowserCookiesFromBrowserCmd(a),
		newBrowserCookiesImportCmd(a), newBrowserCookiesStatusCmd(a), newBrowserCookiesRemoveCmd(a))
	return cmd
}

// browserRef is the short name a person types for a profile, browser:profile,
// mapped to its opaque ID. The daemon's IDs are opaque, so the CLI lists them
// under these names and translates.
func newBrowserCookiesBrowsersCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "browsers <project>",
		Short: "List the browsers installed on this computer, to import from",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			list, err := c.BrowserProfiles(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if len(list.Profiles) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No browser found on this computer.")
				return nil
			}
			out := cmd.OutOrStdout()
			for _, p := range list.Profiles {
				_, _ = fmt.Fprintf(out, "  %-24s %s — %s\n", browserRef(p), p.BrowserName, p.Name)
			}
			_, _ = fmt.Fprintln(out, "\nImport one with: agentbox browser-cookies from-browser <project> <ref>")
			return nil
		},
	}
}

func newBrowserCookiesFromBrowserCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "from-browser <project> <browser:profile>",
		Short: "Import every cookie of one installed browser profile",
		Long: `The ref is one from 'agentbox browser-cookies browsers'. A Chromium browser's
cookies are sealed with a key in your OS keyring; this reads it where it can
(a Linux or Mac host), so from inside AgentBox's VM a Chromium import may get
only unsealed cookies — use the desktop app, which reads the key on the host.
Firefox needs no key.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			list, err := c.BrowserProfiles(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var chosen *api.BrowserProfile
			for i := range list.Profiles {
				if browserRef(list.Profiles[i]) == args[1] {
					chosen = &list.Profiles[i]
					break
				}
			}
			if chosen == nil {
				return fmt.Errorf("no browser %q; run 'agentbox browser-cookies browsers %s' to see them", args[1], args[0])
			}
			secret := ""
			if chosen.Keyring != "" {
				secret = keyringSecret(chosen.Keyring)
			}
			info, err := c.ImportFromBrowser(cmd.Context(), args[0], chosen.ID, secret)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Imported %d cookies for %d sites from %s into %s: agents created from now on get them in their browser.\n",
				info.Cookies, len(info.Sites), info.Source, args[0])
			return nil
		},
	}
}

// browserRef names a profile as browser:profileDir for a person to type.
func browserRef(p api.BrowserProfile) string {
	// The opaque ID is browser\x00goos\x00dir; its last field is the dir.
	parts := strings.Split(p.ID, "\x00")
	return p.Browser + ":" + parts[len(parts)-1]
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
			source := info.Source
			if source == "" {
				source = "a " + info.Format + " export"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d cookies for %d sites, imported %s from %s.\n", info.Cookies, len(info.Sites), info.ImportedAt.Local().Format(time.DateTime), source)
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
