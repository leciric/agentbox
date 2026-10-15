package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/credentials"
)

// The list, default, rename and remove subcommands of `agentbox auth codex`,
// `opencode` and `cursor`, whose logins are named accounts as Claude Code's
// are (credentials/toolaccounts.go). Changes go through the daemon, which
// carries a rename over to every project and agent that names the account,
// and moves a project's chat to its new account.

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
			accounts, err := a.credentials().ToolAccounts(tool)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(accounts) == 0 {
				_, _ = fmt.Fprintf(out, "No %s accounts yet. Add one with: agentbox auth %s\n", name, tool)
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "ACCOUNT\tDEFAULT\tSIGN-IN\tSAVED")
			for _, acc := range accounts {
				def := ""
				if acc.Default {
					def = "yes"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", toolAccountLabel(acc), orDash(def), toolAccountStatus(acc), acc.SavedAt.Local().Format(time.DateTime))
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

// savedToolAccount says where a login was kept, and whether agents use it.
func savedToolAccount(cmd *cobra.Command, a *app, tool, provider, account string) error {
	out := cmd.OutOrStdout()
	name := credentials.ToolName(tool)
	_, _ = fmt.Fprintf(out, "Saved the %s account %q for agents\n", name, toolAccountRef(provider, account))
	def, err := a.credentials().DefaultToolAccount(tool, provider)
	if err != nil {
		return err
	}
	if def != account {
		flag := ""
		if provider != "" {
			flag = " --provider " + provider
		}
		_, _ = fmt.Fprintf(out, "Agents still use %q by default: change that with agentbox auth %s default%s %s, or give it to one project in its settings\n", def, tool, flag, account)
	}
	return nil
}

// toolAccountRef names an account, with its provider for OpenCode's.
func toolAccountRef(provider, account string) string {
	if provider == "" {
		return account
	}
	return provider + "/" + account
}

func toolAccountLabel(acc credentials.ToolAccount) string {
	label := toolAccountRef(acc.Provider, acc.Name)
	if acc.Default {
		label += " (default)"
	}
	return label
}

// toolAccountStatus is how an account signs in, and whose it is.
func toolAccountStatus(acc credentials.ToolAccount) string {
	if acc.Expired {
		return "expired: sign in again (agentbox auth cursor --login --account " + acc.Name + ")"
	}
	kind := map[string]string{"apikey": "API key", "chatgpt": "ChatGPT", "oauth": "OAuth", "wellknown": "well-known"}[acc.Kind]
	if kind == "" {
		kind = "login stored"
	}
	if acc.Email != "" {
		kind += " (" + acc.Email + ")"
	}
	if !acc.Expiry.IsZero() {
		kind += ", until " + acc.Expiry.Format("2 January 2006")
	}
	return kind
}
