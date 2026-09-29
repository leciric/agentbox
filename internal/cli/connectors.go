package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/connectors"
)

// newConnectorCmd gives agents remote MCP servers — Notion, Linear, Figma… —
// signed in to once, here. The daemon keeps the sign-in and relays each
// agent's requests, so no token ever reaches an agent (docs/connectors.md).
func newConnectorCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "connector",
		Aliases: []string{"connectors"},
		Short:   "Give agents remote MCP servers, like Notion or Linear",
		Long: `A connector is a remote MCP server that agents get as tools. You sign in to it
once, in your browser; AgentBox keeps the sign-in on this machine, renews it,
and adds it to every request an agent makes. No token is ever written into an
agent.

A project's connectors go to every agent of the project. An agent's own go to
that agent alone, and replace a project connector of the same name.

  agentbox connector add pawly notion --url https://mcp.notion.com/mcp
  agentbox connector connect pawly notion

A server that can't sign AgentBox in with OAuth — Figma's, for one — can be
sent one of the project's secrets as a header instead:

  agentbox secrets set pawly FIGMA_TOKEN
  agentbox connector add pawly figma --url https://mcp.figma.com/mcp --secret FIGMA_TOKEN --header X-Figma-Token`,
	}
	cmd.AddCommand(newConnectorAddCmd(a), newConnectorListCmd(a), newConnectorConnectCmd(a),
		newConnectorDisconnectCmd(a), newConnectorRemoveCmd(a), newConnectorMCPCmd(a))
	return cmd
}

func newConnectorAddCmd(a *app) *cobra.Command {
	var req api.SetConnectorRequest
	var disabled, enabled bool
	cmd := &cobra.Command{
		Use:   "add <project | project/agent> NAME --url URL",
		Short: "Add a connector, or change one",
		Long: `NAME is what agents' AI tools know it as: lowercase letters, digits, - and _.

--auth is oauth (the default: sign in with connect), secret (send the secret
named by --secret, as --header, "Authorization: Bearer <value>" by default), or
none. Changing the URL or --auth of a connector forgets its sign-in.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case disabled && enabled:
				return errors.New("--enabled and --disabled together")
			case disabled:
				req.Enabled = new(false)
			case enabled:
				req.Enabled = new(true)
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			conn, err := c.SetConnector(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "%s is %s for %s\n", conn.Name, describeConnector(conn), args[0])
			if conn.Auth == api.ConnectorOAuth && conn.Status != api.ConnectorConnected {
				_, _ = fmt.Fprintf(out, "Sign in to it with: agentbox connector connect %s %s\n", args[0], conn.Name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&req.URL, "url", "", "the server's MCP endpoint (streamable HTTP), like https://mcp.notion.com/mcp")
	cmd.Flags().StringVar(&req.Auth, "auth", "", "oauth, secret or none (oauth unless --secret is given)")
	cmd.Flags().StringVar(&req.Secret, "secret", "", "the secret to send, for --auth secret")
	cmd.Flags().StringVar(&req.Header, "header", "", "the header the secret goes in (Authorization)")
	cmd.Flags().StringVar(&req.Scheme, "scheme", "", "what comes before the secret in the header (Bearer, for Authorization)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "keep it, but don't give it to agents")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "give it to agents again")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func newConnectorListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "list [project | project/agent]",
		Aliases: []string{"ls"},
		Short:   "List a project's connectors, or an agent's",
		Long: `A project lists its own connectors; an agent lists everything it is given.
Inside an agent, with no argument, it lists that agent's.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				socket := inAgentSocket()
				if _, err := os.Stat(socket); err != nil {
					return errors.New("which project? agentbox connector list <project | project/agent>")
				}
				mine, err := api.NewClient(socket).SelfConnectors(cmd.Context())
				if err != nil {
					return err
				}
				w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
				_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tURL")
				for _, c := range mine {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", c.Name, statusText(c.Status, c.Error), c.URL)
				}
				return w.Flush()
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			found, err := c.Connectors(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if len(found) == 0 {
				_, _ = fmt.Fprintf(out, "No connectors for %s yet. Add one with: agentbox connector add %s NAME --url URL\n", args[0], args[0])
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tSCOPE\tAUTH\tSTATUS\tURL")
			for _, conn := range found {
				status := statusText(conn.Status, conn.Error)
				if !conn.Enabled {
					status += " (off)"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", conn.Name, conn.Scope, describeConnector(conn), status, conn.URL)
			}
			return w.Flush()
		},
	}
}

func statusText(status, why string) string {
	if why != "" {
		return status + ": " + why
	}
	return status
}

func describeConnector(c api.Connector) string {
	switch c.Auth {
	case api.ConnectorSecret:
		scheme := ""
		if c.Scheme != "" {
			scheme = c.Scheme + " "
		}
		return fmt.Sprintf("sending $%s as %s: %s…", c.Secret, c.Header, scheme)
	case api.ConnectorNone:
		return "public"
	}
	return "oauth"
}

func newConnectorConnectCmd(a *app) *cobra.Command {
	var noWait bool
	cmd := &cobra.Command{
		Use:   "connect <project | project/agent> NAME",
		Short: "Sign in to a connector's server, in your browser",
		Long: `Opens the server's sign-in page and waits until you have signed in. The server
sends your browser back to AgentBox on 127.0.0.1, which finishes the sign-in:
nothing needs pasting back here.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			target, name := args[0], args[1]
			project, agent, _ := strings.Cut(target, "/")
			// Listening before connecting: a sign-in takes a person seconds
			// at the least, and the stream is up well before it ends.
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			done := make(chan api.Connector, 1)
			if !noWait {
				go func() {
					_ = c.Events(ctx, func(ev api.Event) error {
						var conn api.Connector
						if ev.Type == api.EventConnector && json.Unmarshal(ev.Data, &conn) == nil && conn.Project == project &&
							conn.Agent == agent && conn.Name == name && conn.Status != api.ConnectorConnecting {
							select {
							case done <- conn:
							default:
							}
						}
						return nil
					})
				}()
			}
			res, err := c.ConnectConnector(ctx, target, name)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Sign in to %s at:\n\n  %s\n\n", name, res.AuthorizationURL)
			openBrowser(cmd, res.AuthorizationURL)
			if noWait {
				return nil
			}
			_, _ = fmt.Fprintln(out, "Waiting for the sign-in to finish…")
			select {
			case conn := <-done:
				if conn.Removed {
					return fmt.Errorf("%s was removed while you signed in", name)
				}
				if conn.Status != api.ConnectorConnected {
					return fmt.Errorf("%s isn't connected: %s", name, statusText(conn.Status, conn.Error))
				}
				_, _ = fmt.Fprintf(out, "Connected %s. Agents that get it have it in their next session.\n", name)
				return nil
			case <-time.After(time.Until(res.ExpiresAt) + 5*time.Second):
				return errors.New("nobody finished signing in in time: connect again")
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			}
		},
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "print the sign-in page and return, rather than wait for the sign-in")
	return cmd
}

func newConnectorDisconnectCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "disconnect <project | project/agent> NAME",
		Short: "Forget a connector's sign-in, and keep the connector",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if _, err := c.DisconnectConnector(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Disconnected %s of %s\n", args[1], args[0])
			return nil
		},
	}
}

func newConnectorRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <project | project/agent> NAME",
		Aliases: []string{"remove"},
		Short:   "Remove a connector, sign-in and all",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveConnector(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from %s\n", args[1], args[0])
			return nil
		},
	}
}

// newConnectorMCPCmd is the relay inside an agent: the MCP server its AI tools
// start for a connector, which sends everything on to the daemon.
func newConnectorMCPCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:    "mcp NAME",
		Short:  "Relay a connector's MCP server over stdio",
		Hidden: true, // started by the agent's AI tool, not by people
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			socket := inAgentSocket()
			if _, err := os.Stat(socket); err != nil {
				return errors.New("agentbox connector mcp runs inside an agent, for the connectors it is given")
			}
			c := api.NewClient(socket)
			relay := &connectors.Relay{HTTP: c.HTTPClient(), URL: c.SelfConnectorURL(args[0]), Log: cmd.ErrOrStderr()}
			return relay.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}
