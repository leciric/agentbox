package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// agentbox remote app: other apps — Claude Code, Claude Desktop, Cursor,
// Claude, ChatGPT, Poke — paired to use this machine's AgentBox over remote
// MCP: on this computer, through the tunnel phones use, or through its hub.
// The app's Settings has the same as a wizard; this is for a machine with no
// screen.

func newRemoteAppCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Let another app (Claude Code, Cursor, Claude, ChatGPT) use this AgentBox over MCP",
		Long: `Pairs another app with this machine's AgentBox, over remote MCP. A pairing
says what the app may do and in which projects, has a token of its own, and
lasts until revoked. The app reaches AgentBox one of three ways:

  local    an app on this computer, at http://127.0.0.1:7782/mcp, open only
           while an app is paired for it (the default with no hub)
  tunnel   from anywhere, through the Cloudflare tunnel phones use: turn it
           on for apps with "agentbox remote app tunnel on". A quick
           tunnel's URL changes every time it starts; a named tunnel's
           (--hostname) doesn't
  hub      at the hub's address, the only way for apps that sign in with
           OAuth (claude.ai, ChatGPT) (the default with a hub)`,
	}
	var access, client, name, route string
	var projects []string
	var token bool
	add := &cobra.Command{
		Use:   "add",
		Short: "Pair an app: prints the URL to give it, and its token or sign-in code",
		Long: `Pairs an app, and prints what to give it.

  --access read   see projects, agents, pull requests and the lead's chat
  --access chat   and send messages to a project's lead (which can start
                  agents and change code)
  --access full   and call the lead's own tools directly

On this computer or through the tunnel, the app sends the token AgentBox
prints as "Authorization: Bearer <token>", for example:

  claude mcp add --transport http agentbox <URL> --header "Authorization: Bearer <token>"

Through the hub, Claude and ChatGPT sign in with OAuth: add the URL as a
custom connector, and type the code on the page the app opens. An app that
takes a bearer token instead (Poke, most command-line clients) needs --token.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			made, err := c.PairMCP(cmd.Context(), api.MCPPairRequest{Route: route, Label: name, Client: client, Access: access, Projects: projects, Token: token})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Paired %s (%s, %s, %s)\n\n", made.Pairing.Label, routeText(made.Pairing.Route), made.Pairing.Access, projectsText(made.Pairing.Projects))
			_, _ = fmt.Fprintf(out, "  URL    %s\n", made.URL)
			if made.Code != "" {
				_, _ = fmt.Fprintf(out, "  Code   %s   (works once, until %s)\n", made.Code, made.CodeExpiresAt.Local().Format("15:04"))
			}
			if made.Token != "" {
				_, _ = fmt.Fprintf(out, "  Token  %s\n\nThe token is shown only now. Send it as \"Authorization: Bearer <token>\".\n", made.Token)
			}
			if made.Pairing.Route != api.MCPRouteHub {
				_, _ = fmt.Fprintf(out, "\nFor Claude Code:\n  claude mcp add --transport http agentbox %s --header \"Authorization: Bearer %s\"\n", made.URL, made.Token)
			}
			if made.Pairing.Route == api.MCPRouteTunnel {
				_, _ = fmt.Fprintln(out, "\nA quick tunnel's URL changes every time it starts: give the app the new one then (agentbox remote app list).\nA named tunnel's stays the same: agentbox remote app tunnel on --hostname <your hostname>.")
			}
			_, _ = fmt.Fprintf(out, "\nRevoke it with: agentbox remote app revoke %s\n", made.Pairing.ID)
			return nil
		},
	}
	add.Flags().StringVar(&route, "route", "", "how the app reaches AgentBox: local, tunnel or hub (default: hub when connected to one, else local)")
	add.Flags().StringVar(&access, "access", api.MCPAccessRead, "what the app may do: read, chat or full")
	add.Flags().StringVar(&client, "client", api.MCPClientOther, "which app it is: claude-code, claude-desktop, cursor, claude, chatgpt, poke or other")
	add.Flags().StringVar(&name, "name", "", "what to call the pairing (default: the app's name)")
	add.Flags().StringSliceVar(&projects, "project", nil, "a project it covers; repeat for more (default: all projects)")
	add.Flags().BoolVar(&token, "token", false, "through the hub, also make a bearer token, for an app that can't sign in with OAuth")

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
		Short: "Make a new sign-in code (through the hub) or token (otherwise) for a pairing",
		Long: `Through the hub: a new code, to sign the pairing's app in again.
On this computer or through the tunnel: a new token, which replaces the old
one at once.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			made, err := c.RenewMCPCode(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "URL    %s\n", made.URL)
			if made.Code != "" {
				_, _ = fmt.Fprintf(out, "Code   %s   (works once, until %s)\n", made.Code, made.CodeExpiresAt.Local().Format("15:04"))
			}
			if made.Token != "" {
				_, _ = fmt.Fprintf(out, "Token  %s   (the old one no longer works)\n", made.Token)
			}
			return nil
		},
	}

	var hostname string
	var quick, fromStdin bool
	tunnel := &cobra.Command{
		Use:   "tunnel on|off",
		Short: "Let apps reach AgentBox through the tunnel phones use, or stop",
		Long: `Turns the route through the Cloudflare tunnel on or off for apps. It's the
tunnel phones use (agentbox phone tunnel), which runs while either wants it.
Only /mcp is reachable through it for apps, and only with a pairing's token.

A quick tunnel's URL changes every time it starts, and apps paired through it
need the new one then. A named tunnel keeps its hostname: make one in
Cloudflare's dashboard, point its public hostname at the address "agentbox
phone tunnel status" shows, and use:

  agentbox remote app tunnel on --hostname mcp.example.com   (asks for its token)`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			var on bool
			switch args[0] {
			case "on":
				on = true
			case "off":
			default:
				return fmt.Errorf("%q: say on or off", args[0])
			}
			if !on && (quick || hostname != "") {
				return errors.New("--quick and --hostname choose the tunnel to turn on")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			// Which tunnel is the phones' setting too: there's one.
			switch {
			case quick && hostname != "":
				return errors.New("--quick and --hostname are different tunnels: pick one")
			case quick:
				if _, err := c.UpdateLAN(cmd.Context(), api.UpdateLANRequest{TunnelToken: new("")}); err != nil {
					return err
				}
			case hostname != "":
				token, err := secretValue(cmd, "the tunnel's token", fromStdin)
				if err != nil {
					return err
				}
				if _, err := c.UpdateLAN(cmd.Context(), api.UpdateLANRequest{TunnelToken: &token, TunnelHostname: &hostname}); err != nil {
					return err
				}
			}
			list, err := c.UpdateMCP(cmd.Context(), api.UpdateMCPRequest{Tunnel: &on})
			if err != nil {
				return err
			}
			for deadline := time.Now().Add(3 * time.Minute); on && list.Tunnel.State == "starting" && time.Now().Before(deadline); {
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(500 * time.Millisecond):
				}
				if list, err = c.MCPPairings(cmd.Context()); err != nil {
					return err
				}
			}
			printMCPTunnel(cmd.OutOrStdout(), list.Tunnel)
			return nil
		},
	}
	tunnel.Flags().StringVar(&hostname, "hostname", "", "use your own named tunnel, on this public hostname; its token is read from stdin")
	tunnel.Flags().BoolVar(&fromStdin, "token-stdin", false, "read the named tunnel's token from stdin even in a terminal")
	tunnel.Flags().BoolVar(&quick, "quick", false, "go back to a quick tunnel, forgetting the named tunnel's token")

	var port int
	portCmd := &cobra.Command{
		Use:   "port <port>",
		Short: "Change the port apps on this computer reach AgentBox at (default 7782)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := fmt.Sscan(args[0], &port); err != nil {
				return fmt.Errorf("%q isn't a port", args[0])
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			list, err := c.UpdateMCP(cmd.Context(), api.UpdateMCPRequest{Port: &port})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Apps on this computer reach AgentBox at %s\n", list.Local.URL)
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
	cmd.AddCommand(add, list, code, tunnel, portCmd, revoke)
	return cmd
}

func projectsText(projects []string) string {
	if len(projects) == 0 {
		return "all projects"
	}
	return strings.Join(projects, ", ")
}

func routeText(route string) string {
	switch route {
	case api.MCPRouteLocal:
		return "on this computer"
	case api.MCPRouteTunnel:
		return "through the tunnel"
	}
	return "through the hub"
}

func printMCPTunnel(out io.Writer, t api.MCPTunnel) {
	switch {
	case !t.Enabled:
		_, _ = fmt.Fprintln(out, "Tunnel     off for apps (agentbox remote app tunnel on)")
	case t.URL != "":
		_, _ = fmt.Fprintf(out, "Tunnel     %s", t.URL)
		if t.Mode == "quick" {
			_, _ = fmt.Fprint(out, "   (a quick tunnel: this URL changes every time it starts)")
		}
		_, _ = fmt.Fprintln(out)
	default:
		_, _ = fmt.Fprintf(out, "Tunnel     %s %s\n", t.State, t.Error)
	}
}

func printMCPPairings(out io.Writer, list api.MCPPairings) error {
	local := list.Local.URL
	if !list.Local.Listening {
		local += "   (opens when an app is paired for it)"
		if list.Local.Error != "" {
			local = list.Local.URL + "   (" + list.Local.Error + ")"
		}
	}
	_, _ = fmt.Fprintf(out, "Local      %s\n", local)
	printMCPTunnel(out, list.Tunnel)
	switch list.Reason {
	case "no-hub":
		_, _ = fmt.Fprintln(out, "Hub        none (agentbox remote connect <hub URL>, for apps that sign in with OAuth)")
	case "old-hub":
		_, _ = fmt.Fprintf(out, "Hub        %s is from before AgentBox could be used from another app: update it (npx agentbox-hub@latest)\n", list.Hub)
	case "offline":
		_, _ = fmt.Fprintf(out, "Hub        %s can't be reached now (%s): what follows is what this machine allows\n", list.Hub, list.Error)
	default:
		if list.URL != "" {
			_, _ = fmt.Fprintf(out, "Hub        %s\n", list.URL)
		}
	}
	_, _ = fmt.Fprintln(out)
	if len(list.Pairings) == 0 {
		_, _ = fmt.Fprintln(out, "No app is paired. Pair one with: agentbox remote app add")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tROUTE\tACCESS\tPROJECTS\tSIGNED IN\tLAST USED")
	for _, p := range list.Pairings {
		used := "never"
		if !p.LastUsedAt.IsZero() {
			used = p.LastUsedAt.Local().Format(time.DateTime)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Label, cmpOrDash(p.Route), p.Access, projectsText(p.Projects), cmpOrDash(strings.Join(p.Apps, ", ")), used)
	}
	return w.Flush()
}

func cmpOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
