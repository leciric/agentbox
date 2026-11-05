package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
)

// newAuthCopilotCmd signs agents in to GitHub Copilot CLI with a GitHub token
// that has Copilot access, through the daemon so the app's Settings and this
// command keep the same accounts. Copilot's own `copilot login` keeps its
// token in the OS keychain, where AgentBox can't hand it to agents, so the
// account is the token itself (credentials.CopilotLogin).
func newAuthCopilotCmd(a *app) *cobra.Command {
	var fromStdin, fromGH, logout bool
	var account string
	cmd := &cobra.Command{
		Use:   "copilot",
		Short: "Sign agents in to GitHub Copilot CLI",
		Long: `Signs agents in to GitHub Copilot CLI with a GitHub token whose account has
Copilot: a fine-grained personal access token with the "Copilot Requests"
permission, read from stdin with --token-stdin, or the token the GitHub CLI is
signed in with on this computer, with --from-gh. Copilot doesn't take classic
personal access tokens (ghp_).

The token is kept in AgentBox's own directory. Agents get a copy of their
project's account, or the default one, as COPILOT_GITHUB_TOKEN, which Copilot
reads before the agent's own GH_TOKEN. Run it once per account, with
--account, to keep several.`,
		Example: `  agentbox auth copilot --from-gh
  printenv COPILOT_GITHUB_TOKEN | agentbox auth copilot --token-stdin --account work
  agentbox auth copilot --logout --account work`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			var token string
			switch {
			case logout:
				name := account
				if name == "" {
					if name, err = a.credentials().DefaultToolAccount(credentials.ToolCopilot, ""); err != nil {
						return err
					}
				}
				if name == "" {
					_, _ = fmt.Fprintln(out, "Agents aren't signed in to GitHub Copilot CLI")
					return nil
				}
				if err := c.RemoveToolAccount(cmd.Context(), credentials.ToolCopilot, "", name); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(out, "Removed the GitHub Copilot CLI account %q. Agents already created keep the token they were given.\n", name)
				return nil
			case fromStdin:
				b, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
				if err != nil {
					return err
				}
				token = strings.TrimSpace(string(b))
				if token == "" {
					return errors.New("no token on stdin")
				}
			case fromGH:
				var stdout, stderr bytes.Buffer
				gh := exec.CommandContext(cmd.Context(), "gh", "auth", "token")
				gh.Stdout, gh.Stderr = &stdout, &stderr
				if err := gh.Run(); err != nil {
					return fmt.Errorf("gh auth token: %w: %s", err, strings.TrimSpace(stderr.String()))
				}
				token = strings.TrimSpace(stdout.String())
			default:
				return errors.New("give agents a GitHub token with Copilot access: --from-gh for the GitHub CLI's, or --token-stdin to read one")
			}
			if err := c.SaveToolAccount(cmd.Context(), credentials.ToolCopilot, api.ToolAccountRequest{Account: account, APIKey: token}); err != nil {
				return err
			}
			return savedToolAccount(cmd, a, credentials.ToolCopilot, "", cmpOr(account, credentials.DefaultAccount))
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "token-stdin", false, "read a GitHub token with Copilot access from stdin")
	cmd.Flags().BoolVar(&fromGH, "from-gh", false, "use the token `gh auth token` prints on this computer")
	cmd.Flags().BoolVar(&logout, "logout", false, "forget the GitHub Copilot CLI account")
	cmd.Flags().StringVar(&account, "account", "", `the account to sign in or out (default "`+credentials.DefaultAccount+`")`)
	cmd.MarkFlagsMutuallyExclusive("token-stdin", "from-gh", "logout")
	addToolAccountCmds(cmd, a, credentials.ToolCopilot)
	return cmd
}
