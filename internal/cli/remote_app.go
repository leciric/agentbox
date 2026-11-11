package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// agentbox remote app: other apps — Claude, ChatGPT, Poke — paired to use this
// machine's AgentBox over remote MCP, through its hub. The app's Settings has
// the same as a wizard; this is for a machine with no screen.

func newRemoteAppCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Let another app (Claude, ChatGPT, Poke) use this AgentBox over MCP, through the hub",
		Long: `Pairs another app with this machine's AgentBox. The app reaches it over
remote MCP at the hub's address, so the machine opens no port. A pairing
says what the app may do and in which projects, and lasts until revoked.`,
	}
	var access, client, name string
	var projects []string
	var token bool
	add := &cobra.Command{
		Use:   "add",
		Short: "Pair an app: prints the URL to give it, and the code to type when it signs in",
		Long: `Pairs an app, and prints what to give it.

  --access read   see projects, agents, pull requests and the lead's chat
  --access chat   and send messages to a project's lead (which can start
                  agents and change code)
  --access full   and call the lead's own tools directly

Claude and ChatGPT sign in with OAuth: add the URL as a custom connector, and
type the code on the page the app opens. An app that takes a bearer token or
an API key instead (Poke, most command-line clients) needs --token.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			made, err := c.PairMCP(cmd.Context(), api.MCPPairRequest{Label: name, Client: client, Access: access, Projects: projects, Token: token})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Paired %s (%s, %s)\n\n", made.Pairing.Label, made.Pairing.Access, projectsText(made.Pairing.Projects))
			_, _ = fmt.Fprintf(out, "  URL    %s\n", made.URL)
			_, _ = fmt.Fprintf(out, "  Code   %s   (works once, until %s)\n", made.Code, made.CodeExpiresAt.Local().Format("15:04"))
			if made.Token != "" {
				_, _ = fmt.Fprintf(out, "  Token  %s\n\nThe token is shown only now. Send it as \"Authorization: Bearer <token>\".\n", made.Token)
			}
			_, _ = fmt.Fprintf(out, "\nRevoke it with: agentbox remote app revoke %s\n", made.Pairing.ID)
			return nil
		},
	}
	add.Flags().StringVar(&access, "access", api.MCPAccessRead, "what the app may do: read, chat or full")
	add.Flags().StringVar(&client, "client", api.MCPClientOther, "which app it is: claude, chatgpt, poke or other")
	add.Flags().StringVar(&name, "name", "", "what to call the pairing (default: the app's name)")
	add.Flags().StringSliceVar(&projects, "project", nil, "a project it covers; repeat for more (default: all projects)")
	add.Flags().BoolVar(&token, "token", false, "also make a bearer token, for an app that can't sign in with OAuth")

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the paired apps",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			list, err := c.MCPPairings(cmd.Context())
			if err != nil {
				return err
			}
			return printMCPPairings(cmd.OutOrStdout(), list)
		},
	}

	code := &cobra.Command{
		Use:   "code <id>",
		Short: "Make a new code for a pairing, to sign its app in again",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			made, err := c.RenewMCPCode(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "URL   %s\nCode  %s   (works once, until %s)\n", made.URL, made.Code, made.CodeExpiresAt.Local().Format("15:04"))
			return nil
		},
	}

	revoke := &cobra.Command{
		Use:     "revoke <id>",
		Aliases: []string{"rm"},
		Short:   "End an app's access",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RevokeMCPPairing(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(add, list, code, revoke)
	return cmd
}

func projectsText(projects []string) string {
	if len(projects) == 0 {
		return "all projects"
	}
	return strings.Join(projects, ", ")
}

func printMCPPairings(out io.Writer, list api.MCPPairings) error {
	switch list.Reason {
	case "no-hub":
		_, _ = fmt.Fprintln(out, "This machine isn't connected to a hub, which is how another app reaches it: agentbox remote connect <hub URL>")
		return nil
	case "old-hub":
		_, _ = fmt.Fprintf(out, "The hub at %s is from before AgentBox could be used from another app: update it (npx agentbox-hub@latest)\n", list.Hub)
	case "offline":
		_, _ = fmt.Fprintf(out, "The hub at %s can't be reached now (%s): what follows is what this machine allows\n", list.Hub, list.Error)
	}
	if len(list.Pairings) == 0 {
		_, _ = fmt.Fprintln(out, "No app is paired. Pair one with: agentbox remote app add")
		return nil
	}
	_, _ = fmt.Fprintf(out, "MCP URL: %s\n\n", list.URL)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tACCESS\tPROJECTS\tSIGNED IN\tLAST USED")
	for _, p := range list.Pairings {
		used := "never"
		if !p.LastUsedAt.IsZero() {
			used = p.LastUsedAt.Local().Format(time.DateTime)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Label, p.Access, projectsText(p.Projects), cmpOrDash(strings.Join(p.Apps, ", ")), used)
	}
	return w.Flush()
}

func cmpOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
