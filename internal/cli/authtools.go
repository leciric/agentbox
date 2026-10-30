package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
)

// The list, default, rename and remove subcommands of `agentbox auth codex`,
// `opencode` and `cursor`, whose logins are named accounts as Claude Code's
// are (credentials/toolaccounts.go). All of them go through the daemon, which
// is the one that holds the accounts, carries a rename over to every project
// and agent that names the account, and moves a project's chat to its new
// account.

func addToolAccountCmds(cmd *cobra.Command, a *app, tool string) {
	name := credentials.ToolName(tool)
	var provider string
	providerFlag := func(c *cobra.Command) {
		if tool == credentials.ToolOpenCode {
			c.Flags().StringVar(&provider, "provider", "", "the provider the account is for (anthropic, openai…)")
			_ = c.MarkFlagRequired("provider")
		}
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List the stored " + name + " accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			status, err := c.Auth(cmd.Context())
			if err != nil {
				return err
			}
			accounts := accountsOf(status, tool)
			out := cmd.OutOrStdout()
			if len(accounts) == 0 {
				_, _ = fmt.Fprintf(out, "No %s accounts on %s yet. Add one with: agentbox auth %s\n", name, a.target.where(), tool)
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "ACCOUNT\tDEFAULT\tSIGN-IN\tSAVED")
			for _, acc := range accounts {
				def := ""
				if acc.Default {
					def = "yes"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", apiToolLabel(acc), orDash(def), apiToolStatus(acc), acc.SavedAt.Local().Format(time.DateTime))
			}
			return w.Flush()
		},
	}
	def := &cobra.Command{
		Use:   "default <account>",
		Short: "Pick the " + name + " account agents use when their project doesn't name one",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.SetDefaultToolAccount(cmd.Context(), tool, provider, args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "New %s agents use %q unless their project picks another one\n", name, toolAccountRef(provider, args[0]))
			return nil
		},
	}
	rename := &cobra.Command{
		Use:   "rename <account> <new-name>",
		Short: "Give a stored " + name + " account another name",
		Long: `Everything that names the account follows it: the default, each project's
account, each agent's and each project chat's. The login stays the same, so
agents on it keep running. A name another account already has is refused.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			got, err := c.RenameToolAccount(cmd.Context(), tool, provider, args[0], args[1])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Renamed the %s account %q to %q\n", name, toolAccountRef(provider, got.Old), got.Name)
			if len(got.Projects) > 0 {
				_, _ = fmt.Fprintf(out, "Projects carried over: %s\n", strings.Join(got.Projects, ", "))
			}
			if len(got.Agents) > 0 {
				_, _ = fmt.Fprintf(out, "Agents carried over: %s\n", strings.Join(got.Agents, ", "))
			}
			return nil
		},
	}
	remove := &cobra.Command{
		Use:   "remove <account>",
		Short: "Forget a stored " + name + " account",
		Long: `Agents that already have this account's login keep working with it: it was
written into them when they were created.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveToolAccount(cmd.Context(), tool, provider, args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed the %s account %q\n", name, toolAccountRef(provider, args[0]))
			return nil
		},
	}
	for _, c := range []*cobra.Command{def, rename, remove} {
		providerFlag(c)
	}
	cmd.AddCommand(list, def, rename, remove)
}

// savedToolAccount says where a login was kept, and whether agents there use it.
func savedToolAccount(cmd *cobra.Command, a *app, c *api.Client, tool, provider, account string) error {
	out := cmd.OutOrStdout()
	name := credentials.ToolName(tool)
	_, _ = fmt.Fprintf(out, "Saved the %s account %q for agents on %s\n", name, toolAccountRef(provider, account), a.target.where())
	def, err := defaultToolAccount(cmd, c, tool, provider)
	if err != nil {
		return err
	}
	if def != "" && def != account {
		flag := ""
		if provider != "" {
			flag = " --provider " + provider
		}
		_, _ = fmt.Fprintf(out, "Agents still use %q by default: change that with agentbox auth %s default%s %s, or give it to one project in its settings\n", def, tool, flag, account)
	}
	return nil
}

// defaultToolAccount is the account a tool, or one of OpenCode's providers,
// gives agents when nothing names one, as the daemon that holds the accounts
// answers it.
func defaultToolAccount(cmd *cobra.Command, c *api.Client, tool, provider string) (string, error) {
	status, err := c.Auth(cmd.Context())
	if err != nil {
		return "", err
	}
	for _, acc := range accountsOf(status, tool) {
		if acc.Provider == provider && acc.Default {
			return acc.Name, nil
		}
	}
	return "", nil
}

// toolAccountRef names an account, with its provider for OpenCode's.
func toolAccountRef(provider, account string) string {
	if provider == "" {
		return account
	}
	return provider + "/" + account
}
