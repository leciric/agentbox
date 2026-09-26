package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newGitHubAccountCmd shows or picks the GitHub account a project's agents
// use. The accounts themselves are stored with `agentbox auth github`.
func newGitHubAccountCmd(a *app) *cobra.Command {
	var clear, moveAgents bool
	cmd := &cobra.Command{
		Use:   "github-account <project | project/agent> [account]",
		Short: "Show or pick the GitHub account a project's agents use",
		Long: `Without an account, shows which one is used, and where that comes from.

With an account, a project's new agents use it instead of this machine's default;
naming one agent writes that account's token into the running agent. A new shell
picks it up, and so does the AI tool the next time it starts. --clear goes back
to inheriting.

Changing a project's account only moves the project's chat: its agents that
already exist keep the account they were made with. When some of them are
still on the account being replaced, this asks whether to move them too;
--move-agents answers yes without asking, for scripts.

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
				if cmd.Flags().Changed("move-agents") {
					return errors.New("--move-agents moves a project's agents, not one agent's own account")
				}
				return githubAccountForAgent(cmd, c, target, account, set)
			}
			var move *bool
			if cmd.Flags().Changed("move-agents") {
				move = &moveAgents
			}
			return githubAccountForProject(cmd, c, target, account, set, move)
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "go back to inheriting: a project falls back to the default account, an agent to its project's")
	cmd.Flags().BoolVar(&moveAgents, "move-agents", false, "move agents still on the account being replaced to the new one, without asking")
	return cmd
}

func githubAccountForProject(cmd *cobra.Command, c *api.Client, name, account string, set bool, moveAgents *bool) error {
	out := cmd.OutOrStdout()
	p, err := c.Project(cmd.Context(), name)
	if err != nil {
		return err
	}
	if set {
		move := false
		if account != p.GitHubAccount {
			if moveAgents != nil {
				move = *moveAgents
			} else {
				old := p.GitHubAccount
				if old == "" {
					if old, err = defaultGitHubAccountName(cmd, c); err != nil {
						return err
					}
				}
				if move, err = confirmMoveAgents(cmd, c, p.Name, func(ag api.Agent) bool { return ag.GitHubAccount == old }, old, account); err != nil {
					return err
				}
			}
		}
		if p, err = c.UpdateProject(cmd.Context(), name, api.UpdateProjectRequest{GitHubAccount: &account, MoveGitHubAgents: move}); err != nil {
			return err
		}
		if p.GitHubAccount == "" {
			_, _ = fmt.Fprintf(out, "New agents of %s use this machine's default GitHub account\n", p.Name)
		} else {
			_, _ = fmt.Fprintf(out, "New agents of %s use the GitHub account %q\n", p.Name, p.GitHubAccount)
		}
		if move {
			_, _ = fmt.Fprintln(out, "Its agents that were still on the old account move to the new one.")
		} else {
			_, _ = fmt.Fprintln(out, "Agents that already exist keep the account they were created with.")
		}
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
