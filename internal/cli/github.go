package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newGitHubAccountCmd shows or picks the GitHub account a project's agents
// use. The accounts themselves are stored with `agentbox auth github`.
func newGitHubAccountCmd(a *app) *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "github-account <project | project/agent> [account]",
		Short: "Show or pick the GitHub account a project's agents use",
		Long: `Without an account, shows which one is used, and where that comes from.

With an account, a project's new agents use it instead of this machine's default;
naming one agent writes that account's token into the running agent. A new shell
picks it up, and so does the AI tool the next time it starts. --clear goes back
to inheriting.

Accounts are stored with agentbox auth github.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			account := ""
			if len(args) == 2 {
				account = args[1]
			}
			if clear && account != "" {
				return fmt.Errorf("--clear and the account %q ask for opposite things: pass one of them", account)
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			set := clear || account != ""
			if strings.Contains(target, "/") {
				return githubAccountForAgent(cmd, c, target, account, set)
			}
			return githubAccountForProject(cmd, c, target, account, set)
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "go back to inheriting: a project falls back to the default account, an agent to its project's")
	return cmd
}

func githubAccountForProject(cmd *cobra.Command, c *api.Client, name, account string, set bool) error {
	out := cmd.OutOrStdout()
	p, err := c.Project(cmd.Context(), name)
	if err != nil {
		return err
	}
	if set {
		if p, err = c.UpdateProject(cmd.Context(), name, api.UpdateProjectRequest{GitHubAccount: &account}); err != nil {
			return err
		}
		if p.GitHubAccount == "" {
			_, _ = fmt.Fprintf(out, "New agents of %s use this machine's default GitHub account\n", p.Name)
		} else {
			_, _ = fmt.Fprintf(out, "New agents of %s use the GitHub account %q\n", p.Name, p.GitHubAccount)
		}
		_, _ = fmt.Fprintln(out, "Agents that already exist keep the account they were created with.")
		return nil
	}
	if p.GitHubAccount != "" {
		_, _ = fmt.Fprintf(out, "%s: new agents use the GitHub account %q\n", p.Name, p.GitHubAccount)
		return nil
	}
	def, err := defaultGitHubAccountName(cmd, c)
	if err != nil {
		return err
	}
	if def == "" {
		_, _ = fmt.Fprintf(out, "%s: no GitHub account picked, and none is stored (agentbox auth github)\n", p.Name)
		return nil
	}
	_, _ = fmt.Fprintf(out, "%s: new agents use this machine's default GitHub account, %q\n", p.Name, def)
	return nil
}

func githubAccountForAgent(cmd *cobra.Command, c *api.Client, ref, account string, set bool) error {
	out := cmd.OutOrStdout()
	if set {
		ag, err := c.UpdateAgent(cmd.Context(), ref, api.UpdateAgentRequest{GitHubAccount: &account})
		if err != nil {
			return err
		}
		if ag.GitHubAccount == "" {
			_, _ = fmt.Fprintf(out, "%s now uses its project's GitHub account (or none, when its project has none)\n", ag.Ref)
		} else {
			_, _ = fmt.Fprintf(out, "%s now uses the GitHub account %q\n", ag.Ref, ag.GitHubAccount)
		}
		_, _ = fmt.Fprintln(out, "A new shell picks it up, and so does the AI tool the next time it starts: /exit in its window, then start it again.")
		return nil
	}
	ag, err := c.Agent(cmd.Context(), ref)
	if err != nil {
		return err
	}
	if ag.GitHubAccount == "" {
		_, _ = fmt.Fprintf(out, "%s has no GitHub account (agentbox auth github)\n", ag.Ref)
		return nil
	}
	_, _ = fmt.Fprintf(out, "%s uses the GitHub account %q\n", ag.Ref, ag.GitHubAccount)
	return nil
}
