package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"agentbox/internal/api"
)

// toolNames are the AI tools a server can be imported from, as people know them.
var toolNames = map[string]string{
	"claude": "Claude Code", "claude-desktop": "Claude Desktop", "codex": "Codex",
	"opencode": "OpenCode", "cursor": "Cursor", "gemini": "Gemini CLI",
}

func newConnectorImportCmd(a *app) *cobra.Command {
	var global, dryRun bool
	cmd := &cobra.Command{
		Use:   "import <project | --global> [NAME...]",
		Short: "Add the MCP servers your own AI tools have as connectors",
		Long: `Looks for the MCP servers Claude Code, Claude Desktop, Codex, OpenCode, Cursor
and Gemini CLI have in your home, and adds the remote ones as connectors: all
that can be, or the NAMEs given. --dry-run lists what it found and adds nothing.

A connector is a remote server over streamable HTTP. A server your AI tool runs
as a command (stdio), or one that speaks the older SSE transport, is listed and
left out. The same server in two tools is one, and one that is a connector
already is skipped.

Nothing of your AI tools' sign-ins is copied: a server that wants one is added
for you to connect (agentbox connector connect). A header the configuration
sends, like an API key, is kept as a secret; one it reads from an environment
variable of your shell is asked for here.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var project string
			switch {
			case global:
			case len(args) == 0:
				return errors.New("which project? agentbox connector import <project>, or --global for AgentBox-wide connectors")
			default:
				project, args = args[0], args[1:]
				if strings.Contains(project, "/") {
					return errors.New("import into a project, or --global: an agent's own connectors are added one at a time with agentbox connector add")
				}
			}
			target := project
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			scan, err := c.ScanConnectors(cmd.Context(), project)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, u := range scan.Unreadable {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Couldn't read %s's %s: %s\n", toolNames[u.Tool], u.Path, u.Error)
			}
			if dryRun || len(scan.Candidates) == 0 {
				if len(scan.Candidates) == 0 {
					_, _ = fmt.Fprintln(out, "None of your AI tools has an MCP server configured.")
					return nil
				}
				w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
				_, _ = fmt.Fprintln(w, "NAME\tFROM\tIMPORT\tSERVER")
				for _, cand := range scan.Candidates {
					server := cand.URL
					if server == "" {
						server = cand.Command
					}
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cand.Name, sourcesText(cand.Sources), candidateText(cand), server)
				}
				return w.Flush()
			}

			var req api.ImportConnectorsRequest
			req.Project = project
			for _, cand := range scan.Candidates {
				if len(args) > 0 && !slices.Contains(args, cand.Name) {
					continue
				}
				if cand.Problem != "" || cand.Existing != "" {
					if len(args) > 0 {
						_, _ = fmt.Fprintf(out, "%s: %s\n", cand.Name, candidateText(cand))
					}
					continue
				}
				srv := api.ImportConnector{URL: cand.URL}
				if cand.NeedsValue && term.IsTerminal(int(os.Stdin.Fd())) {
					value, err := readSecret(fmt.Sprintf("%s sends $%s as %s; its value (not shown, Enter to skip): ", cand.Name, cand.Variable, cand.Header))
					if err != nil {
						return err
					}
					srv.Value = strings.TrimSpace(value)
				}
				req.Servers = append(req.Servers, srv)
			}
			for _, name := range args {
				if !slices.ContainsFunc(scan.Candidates, func(cand api.ConnectorCandidate) bool { return cand.Name == name }) {
					return fmt.Errorf("none of your AI tools has a server that would be imported as %s: agentbox connector import %s --dry-run lists them", name, scopeArg(target))
				}
			}
			if len(req.Servers) == 0 {
				_, _ = fmt.Fprintln(out, "Nothing to import: agentbox connector import --dry-run says why.")
				return nil
			}
			res, err := c.ImportConnectors(cmd.Context(), req)
			if err != nil {
				return err
			}
			for _, conn := range res.Added {
				_, _ = fmt.Fprintf(out, "Added %s, %s, for %s\n", conn.Name, describeConnector(conn), scopeText(target))
			}
			for _, f := range res.Failed {
				_, _ = fmt.Fprintf(out, "Didn't add %s: %s\n", cmpName(f), f.Error)
			}
			for _, conn := range res.Added {
				if conn.Auth == api.ConnectorOAuth {
					_, _ = fmt.Fprintf(out, "Sign in to %s with: agentbox connector connect %s %s\n", conn.Name, scopeArg(target), conn.Name)
				}
			}
			if len(res.Failed) > 0 && len(res.Added) == 0 {
				return errors.New("nothing was imported")
			}
			return nil
		},
	}
	globalFlag(cmd, &global)
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "list what your AI tools have, and add nothing")
	return cmd
}

func cmpName(f api.ImportConnectorFailure) string {
	if f.Name != "" {
		return f.Name
	}
	return f.URL
}

// sourcesText is the tools a server was found in.
func sourcesText(sources []api.ConnectorSource) string {
	var names []string
	for _, s := range sources {
		n := toolNames[s.Tool]
		if n == "" {
			n = s.Tool
		}
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return strings.Join(names, ", ")
}

// candidateText says what importing a server would do, or why it can't.
func candidateText(c api.ConnectorCandidate) string {
	switch {
	case c.Problem == api.ConnectorProblemStdio:
		return "no: a command (stdio), not a remote server"
	case c.Problem == api.ConnectorProblemSSE:
		return "no: SSE only, not streamable HTTP"
	case c.Problem == api.ConnectorProblemVariable:
		return "no: a variable in its address"
	case c.Problem != "":
		return "no: not an https address AgentBox can reach"
	case c.Existing != "":
		return "already a connector"
	case c.NeedsValue:
		return fmt.Sprintf("yes, sending %s from $%s (asked for)", c.Header, c.Variable)
	case c.Header != "":
		return fmt.Sprintf("yes, sending %s as $%s", c.Header, c.Secret)
	}
	return "yes, OAuth if it asks"
}
