package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newClaudeAccountCmd shows or picks the Claude Code account a project's agents
// use. The accounts themselves are stored with `agentbox auth claude`.
func newClaudeAccountCmd(a *app) *cobra.Command {
	var clear, allowAll, moveAgents bool
	var allow, allowAdd, allowRemove []string
	cmd := &cobra.Command{
		Use:   "claude-account <project | project/agent> [account]",
		Short: "Show or pick the Claude Code account a project's agents use",
		Long: `Without an account, shows which one is used, and where that comes from.

With an account, a project's new agents use it instead of this machine's default;
naming one agent writes that account's token into the running agent, and Claude
Code picks it up the next time it starts. --clear goes back to inheriting.

Changing a project's account only moves the project's chat: its agents that
already exist keep the account they were made with. When some of them are
still on the account being replaced, this asks whether to move them too;
--move-agents answers yes without asking, for scripts.

A project can also be limited to some of this machine's accounts: --allow sets
the list, --allow-add and --allow-remove change it, and --allow-all goes back to
every account. The project's own account has to stay in the list. Agents that
already hold an account the list leaves out keep it; the list only refuses new
choices.

  agentbox claude-account myapp --allow work,client
  agentbox claude-account myapp work --allow work,client

Accounts are stored with agentbox auth claude.`,
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
			changesList := allowAll || cmd.Flags().Changed("allow") || len(allowAdd) > 0 || len(allowRemove) > 0
			if strings.Contains(target, "/") {
				if changesList {
					return fmt.Errorf("the accounts allowed belong to a project, not to one agent: name %s", strings.SplitN(target, "/", 2)[0])
				}
				if cmd.Flags().Changed("move-agents") {
					return errors.New("--move-agents moves a project's agents, not one agent's own account")
				}
				return claudeAccountForAgent(cmd, c, target, account, set)
			}
			if allowAll && (cmd.Flags().Changed("allow") || len(allowAdd) > 0 || len(allowRemove) > 0) {
				return errors.New("--allow-all allows every account: don't combine it with --allow, --allow-add or --allow-remove")
			}
			var allowed *[]string
			if changesList {
				list, err := allowedAccounts(cmd, c, target, allowAll, cmd.Flags().Changed("allow"), allow, allowAdd, allowRemove)
				if err != nil {
					return err
				}
				allowed = &list
			}
			var move *bool
			if cmd.Flags().Changed("move-agents") {
				move = &moveAgents
			}
			return claudeAccountForProject(cmd, c, target, account, set, allowed, move)
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "go back to inheriting: a project falls back to the default account, an agent to its project's")
	cmd.Flags().BoolVar(&moveAgents, "move-agents", false, "move agents still on the account being replaced to the new one, without asking")
	cmd.Flags().StringSliceVar(&allow, "allow", nil, "limit the project's agents to these accounts, comma-separated")
	cmd.Flags().StringSliceVar(&allowAdd, "allow-add", nil, "add accounts to the project's list")
	cmd.Flags().StringSliceVar(&allowRemove, "allow-remove", nil, "remove accounts from the project's list")
	cmd.Flags().BoolVar(&allowAll, "allow-all", false, "let the project's agents use every account again")
	return cmd
}

// allowedAccounts works out the project's new list from the --allow flags.
func allowedAccounts(cmd *cobra.Command, c *api.Client, name string, all, replace bool, allow, add, remove []string) ([]string, error) {
	if all {
		return []string{}, nil
	}
	list := allow
	if !replace {
		p, err := c.Project(cmd.Context(), name)
		if err != nil {
			return nil, err
		}
		list = p.ClaudeAccounts
		if len(list) == 0 && len(remove) > 0 {
			// Every account is allowed, so removing one means listing the rest.
			auth, err := c.Auth(cmd.Context())
			if err != nil {
				return nil, err
			}
			for _, acc := range auth.ClaudeAccounts {
				list = append(list, acc.Name)
			}
		}
	}
	list = append(slices.Clone(list), add...)
	list = slices.DeleteFunc(list, func(a string) bool { return slices.Contains(remove, a) })
	if len(list) == 0 && (len(remove) > 0 || replace) {
		return nil, errors.New("that leaves the project no Claude Code account at all: use --allow-all to allow every account")
	}
	return list, nil
}

func claudeAccountForProject(cmd *cobra.Command, c *api.Client, name, account string, set bool, allowed *[]string, moveAgents *bool) error {
	out := cmd.OutOrStdout()
	p, err := c.Project(cmd.Context(), name)
	if err != nil {
		return err
	}
	move := false
	if set && account != p.ClaudeAccount {
		if moveAgents != nil {
			move = *moveAgents
		} else {
			old, err := resolvedClaudeAccount(cmd, c, p.ClaudeAccount)
			if err != nil {
				return err
			}
			if move, err = confirmMoveAgents(cmd, c, p.Name, func(ag api.Agent) bool { return ag.AI == "claude" && ag.ClaudeAccount == old }, old, account); err != nil {
				return err
			}
		}
	}
	if set || allowed != nil {
		req := api.UpdateProjectRequest{ClaudeAccounts: allowed}
		if set {
			req.ClaudeAccount = &account
			req.MoveClaudeAgents = move
		}
		if p, err = c.UpdateProject(cmd.Context(), name, req); err != nil {
			return err
		}
		if set {
			if p.ClaudeAccount == "" {
				_, _ = fmt.Fprintf(out, "New agents of %s use this machine's default Claude Code account\n", p.Name)
			} else {
				_, _ = fmt.Fprintf(out, "New agents of %s use the Claude Code account %q\n", p.Name, p.ClaudeAccount)
			}
		}
		if allowed != nil {
			printAllowed(out, p)
		}
		if move {
			_, _ = fmt.Fprintln(out, "Its agents that were still on the old account move to the new one.")
		} else {
			_, _ = fmt.Fprintln(out, "Agents that already exist keep the account they were created with.")
		}
		return nil
	}
	defer printAllowed(out, p)
	if p.ClaudeAccount != "" {
		_, _ = fmt.Fprintf(out, "%s: new agents use the Claude Code account %q\n", p.Name, p.ClaudeAccount)
		return nil
	}
	def, err := defaultClaudeAccount(cmd, c)
	if err != nil {
		return err
	}
	if def == "" {
		_, _ = fmt.Fprintf(out, "%s: no Claude Code account picked, and none is stored (agentbox auth claude)\n", p.Name)
		return nil
	}
	_, _ = fmt.Fprintf(out, "%s: new agents use this machine's default Claude Code account, %q\n", p.Name, def)
	return nil
}

func claudeAccountForAgent(cmd *cobra.Command, c *api.Client, ref, account string, set bool) error {
	out := cmd.OutOrStdout()
	if set {
		ag, err := c.UpdateAgent(cmd.Context(), ref, api.UpdateAgentRequest{ClaudeAccount: &account})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s now uses the Claude Code account %q\n", ag.Ref, ag.ClaudeAccount)
		_, _ = fmt.Fprintln(out, "Claude Code picks the new token up the next time it starts: /exit in its window, then start it again.")
		return nil
	}
	ag, err := c.Agent(cmd.Context(), ref)
	if err != nil {
		return err
	}
	if ag.ClaudeAccount == "" {
		_, _ = fmt.Fprintf(out, "%s runs %s, so it has no Claude Code account\n", ag.Ref, ag.AI)
		return nil
	}
	_, _ = fmt.Fprintf(out, "%s uses the Claude Code account %q\n", ag.Ref, ag.ClaudeAccount)
	return nil
}

func printAllowed(out io.Writer, p api.Project) {
	if len(p.ClaudeAccounts) == 0 {
		_, _ = fmt.Fprintf(out, "%s may use every Claude Code account\n", p.Name)
		return
	}
	_, _ = fmt.Fprintf(out, "%s may use the Claude Code accounts %s\n", p.Name, strings.Join(p.ClaudeAccounts, ", "))
}

// defaultClaudeAccount is the account new agents get when nothing else names one.
func defaultClaudeAccount(cmd *cobra.Command, c *api.Client) (string, error) {
	auth, err := c.Auth(cmd.Context())
	if err != nil {
		return "", err
	}
	for _, acc := range auth.ClaudeAccounts {
		if acc.Default {
			return acc.Name, nil
		}
	}
	return "", nil
}

// resolvedClaudeAccount is account, or the machine's default when it's empty
// — what an agent on "the project's account" actually holds, since an agent
// never stores the empty string itself (agent.ClaudeAccountFor).
func resolvedClaudeAccount(cmd *cobra.Command, c *api.Client, account string) (string, error) {
	if account != "" {
		return account, nil
	}
	return defaultClaudeAccount(cmd, c)
}

// confirmMoveAgents asks whether to move a project's agents still on the old
// account to the new one, unless there's nobody on it to ask about. It takes
// silence, or anything but yes, for no.
func confirmMoveAgents(cmd *cobra.Command, c *api.Client, project string, on func(api.Agent) bool, old, account string) (bool, error) {
	agents, err := c.Agents(cmd.Context(), project)
	if err != nil {
		return false, err
	}
	n := 0
	for _, ag := range agents {
		if on(ag) {
			n++
		}
	}
	if n == 0 {
		return false, nil
	}
	plural := ""
	if n != 1 {
		plural = "s"
	}
	question := fmt.Sprintf("Move %d agent%s on %s to %s too?", n, plural, accountLabel(old), accountLabel(account))
	return confirmPrompt(cmd, question)
}

// accountLabel names an account the way a question reads best: quoted, or
// "the default account" for the machine's own.
func accountLabel(name string) string {
	if name == "" {
		return "the default account"
	}
	return strconv.Quote(name)
}
