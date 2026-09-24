package cli

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// The browser commands work on the host, on the agent they name, and inside an
// agent, on its own browser.
func newBrowserCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browser",
		Short: "An agent's own browser: Chromium on a virtual display, which you can watch in the app",
	}
	cmd.AddCommand(
		newBrowserStatusCmd(a),
		newBrowserActionCmd(a, "start", "Start the browser, unless it's running"),
		newBrowserActionCmd(a, "stop", "Stop the browser and its display"),
		newBrowserOpenCmd(a),
	)
	return cmd
}

// scopedClient talks to the in-agent API when no agent is named, and to the
// daemon otherwise. The browser and media commands use it.
func (a *app) scopedClient(cmd *cobra.Command, ref string) (*api.Client, error) {
	if ref != "" {
		return a.client(cmd)
	}
	if _, err := os.Stat(api.InAgentSocket); err != nil {
		return nil, errors.New("name an agent, like pawly/agent-01 (inside an agent, the agent is the one running the command)")
	}
	return api.NewClient(api.InAgentSocket), nil
}

func printBrowser(cmd *cobra.Command, status api.BrowserStatus) error {
	out := cmd.OutOrStdout()
	if !status.Running {
		if status.Display {
			// The desktop outlives Chromium: the app still shows it, and the
			// dock on it can start a browser again.
			fmt.Fprintln(out, "The browser isn't running, but the desktop is")
			return nil
		}
		fmt.Fprintln(out, "The browser isn't running")
		return nil
	}
	fmt.Fprintf(out, "The browser is running (%s)\n", status.Version)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, p := range status.Pages {
		fmt.Fprintf(w, "  %s\t%s\n", p.URL, p.Title)
	}
	return w.Flush()
}

func optionalRef(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func newBrowserStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status [agent]",
		Short: "Show whether the browser is running, and its open pages",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			status, err := c.Browser(cmd.Context(), ref)
			if err != nil {
				return err
			}
			return printBrowser(cmd, status)
		},
	}
}

func newBrowserActionCmd(a *app, action, short string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " [agent]",
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			status, err := c.BrowserAction(cmd.Context(), ref, action)
			if err != nil {
				return err
			}
			return printBrowser(cmd, status)
		},
	}
}

func newBrowserOpenCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "open [agent] <url>",
		Short: "Open a page in the browser, starting it if needed",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, target := "", args[0]
			if len(args) == 2 {
				ref, target = args[0], args[1]
			}
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			status, err := c.OpenInBrowser(cmd.Context(), ref, target)
			if err != nil {
				return err
			}
			return printBrowser(cmd, status)
		},
	}
}
