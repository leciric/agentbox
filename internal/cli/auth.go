package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/opencode"
)

func newAuthCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log agents in to Claude Code, Codex, OpenCode and Cursor",
		Long: `Agents use AgentBox's own logins, never your host's ~/.claude, ~/.codex,
~/.local/share/opencode or ~/.cursor. Sharing those directories would let an
agent change your host configuration.`,
	}
	cmd.AddCommand(newAuthClaudeCmd(a), newAuthCodexCmd(a), newAuthOpenCodeCmd(a), newAuthCursorCmd(a), newAuthGitHubCmd(a), newAuthStatusCmd(a))
	return cmd
}

func newAuthClaudeCmd(a *app) *cobra.Command {
	var fromStdin bool
	var account string
	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Store a long-lived Claude Code token for agents",
		Long: `Runs 'claude setup-token' and keeps the token it mints, so there is nothing
to copy: open the page it prints, approve the login, and it saves itself. It
uses the Claude Code AgentBox installed for itself, and installs it if this
machine has none. With --token-stdin, reads a token from stdin instead.

Tokens are stored under a name, so this machine can hold several Anthropic
accounts: run this once per account, with --account. An agent uses its project's
account (agentbox claude-account), and otherwise the default one.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if account == "" {
				account = credentials.DefaultAccount
			}
			if fromStdin {
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				if err := c.SaveClaudeToken(cmd.Context(), api.ClaudeTokenRequest{Token: string(b), Account: account}); err != nil {
					return err
				}
			} else if err := claudeLogin(cmd, c, account, a.target.where()); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Saved the Claude Code account %q for agents on %s\n", account, a.target.describe())
			def, err := defaultClaudeAccountName(cmd, c)
			if err != nil {
				return err
			}
			if def == account {
				_, _ = fmt.Fprintln(out, "Agents use it unless their project picks another one.")
			} else if def != "" {
				_, _ = fmt.Fprintf(out, "Agents still use %q by default: change that with agentbox auth claude default %s, or give it to one project with agentbox claude-account <project> %s\n", def, account, account)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "token-stdin", false, "read the token from stdin")
	cmd.Flags().StringVar(&account, "account", "", `store it under this name (default "`+credentials.DefaultAccount+`")`)
	cmd.AddCommand(newAuthClaudeListCmd(a), newAuthClaudeDefaultCmd(a), newAuthClaudeRenameCmd(a), newAuthClaudeRemoveCmd(a))
	return cmd
}

func newAuthClaudeListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the stored Claude Code accounts",
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
			out := cmd.OutOrStdout()
			if len(status.ClaudeAccounts) == 0 {
				_, _ = fmt.Fprintln(out, "No Claude Code accounts yet. Add one with: agentbox auth claude")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "ACCOUNT\tDEFAULT\tSAVED")
			for _, acc := range status.ClaudeAccounts {
				// A token stored before AgentBox recorded the date has none,
				// and the file's own timestamp isn't one: copying a
				// credentials directory rewrites it.
				saved := "unknown"
				if !acc.SavedAt.IsZero() {
					saved = acc.SavedAt.Local().Format(time.DateTime)
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", acc.Name, orDash(boolWord(acc.Default)), saved)
			}
			return w.Flush()
		},
	}
}

func newAuthClaudeDefaultCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "default <account>",
		Short: "Pick the Claude Code account agents use when their project doesn't name one",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.SetDefaultClaudeAccount(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "New agents use the Claude Code account %q unless their project picks another one\n", args[0])
			return nil
		},
	}
}

func newAuthClaudeRenameCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <account> <new-name>",
		Short: "Give a stored Claude Code account another name",
		Long: `Everything that names the account follows it: the machine's default, each
project's account and allowed accounts, each agent's account and each project
chat's, and its usage-limit reading. The token stays the same, so agents on
the account keep running without a restart. A name another account already
has is refused.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			got, err := c.RenameClaudeAccount(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Renamed the Claude Code account %q to %q\n", got.Old, got.Name)
			if len(got.Projects) > 0 {
				_, _ = fmt.Fprintf(out, "Projects carried over: %s\n", strings.Join(got.Projects, ", "))
			}
			if len(got.Agents) > 0 {
				_, _ = fmt.Fprintf(out, "Agents carried over: %s. They keep the same token, so none needs a restart.\n", strings.Join(got.Agents, ", "))
			}
			return nil
		},
	}
}

func newAuthClaudeRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <account>",
		Short: "Forget a stored Claude Code account",
		Long: `Agents that already have this account's token keep working with it:
the token was written into them when they were created.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveClaudeAccount(cmd.Context(), args[0]); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Removed the Claude Code account %q\n", args[0])
			def, err := defaultClaudeAccountName(cmd, c)
			if err != nil {
				return err
			}
			if def == "" {
				_, _ = fmt.Fprintln(out, "No accounts left: new Claude Code agents need one (agentbox auth claude)")
			} else {
				_, _ = fmt.Fprintf(out, "New agents now use %q by default. Projects that picked the removed account need another one: agentbox claude-account <project> <account>\n", def)
			}
			return nil
		},
	}
}

func newAuthCodexCmd(a *app) *cobra.Command {
	var apiKey bool
	var account string
	cmd := &cobra.Command{
		Use:   "codex",
		Short: "Log agents in to Codex",
		Long: `Runs 'codex login' against a Codex home of AgentBox's own, so the login is
separate from your host's ~/.codex, and keeps it as a named account. New agents
get a copy of their project's account, or the default one.

Run it once per account, with --account, to keep several.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			args := []string{"login"}
			if apiKey {
				args = append(args, "--with-api-key")
			}
			creds := a.credentials()
			account = cmp.Or(account, credentials.DefaultAccount)
			if err := credentials.ValidateAccount(account); err != nil {
				return err
			}
			if err := os.MkdirAll(creds.Dir, 0o700); err != nil {
				return err
			}
			home, err := os.MkdirTemp(creds.Dir, ".codex-login-")
			if err != nil {
				return err
			}
			defer func() { _ = os.RemoveAll(home) }()
			login := exec.CommandContext(cmd.Context(), "codex", args...)
			login.Env = append(os.Environ(), "CODEX_HOME="+home)
			login.Stdin, login.Stdout, login.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := login.Run(); err != nil {
				if errors.Is(err, exec.ErrNotFound) {
					return errors.New("logging agents in to Codex needs the Codex CLI on this machine, and it isn't installed: install it (npm install -g @openai/codex, or a release from https://github.com/openai/codex/releases), then run agentbox auth codex again")
				}
				return fmt.Errorf("codex %s: %w", strings.Join(args, " "), err)
			}
			auth, err := os.ReadFile(filepath.Join(home, "auth.json"))
			if err != nil {
				return fmt.Errorf("codex stored no login: run agentbox auth codex again and finish it (%w)", err)
			}
			if err := creds.SaveToolAccount(credentials.ToolCodex, "", account, auth); err != nil {
				return err
			}
			return savedToolAccount(cmd, a, credentials.ToolCodex, "", account)
		},
	}
	cmd.Flags().BoolVar(&apiKey, "api-key-stdin", false, "read an OpenAI API key from stdin instead of logging in with ChatGPT")
	cmd.Flags().StringVar(&account, "account", "", `store it under this name (default "`+credentials.DefaultAccount+`")`)
	addToolAccountCmds(cmd, a, credentials.ToolCodex)
	return cmd
}

// newAuthOpenCodeCmd logs agents in to OpenCode. OpenCode's logins are
// provider keys, all of them in one auth.json it reads from
// $XDG_DATA_HOME/opencode: the login runs with that variable, and the config
// one, pointing at directories of AgentBox's own (D6), so the host's own
// opencode.json — which may be written for the other release — is neither read
// nor rewritten. Each provider it wrote is kept as an account of that provider.
func newAuthOpenCodeCmd(a *app) *cobra.Command {
	var provider, method, account string
	var openCodeV2 bool
	cmd := &cobra.Command{
		Use:   "opencode",
		Short: "Log agents in to OpenCode",
		Long: `Runs 'opencode auth login' against a data and config directory of AgentBox's
own, so the login is separate from your host's ~/.local/share/opencode and
~/.config/opencode (whose opencode.json may belong to the other OpenCode
release), and keeps what it stores as a named account of that provider. The OpenCode it runs is AgentBox's
own, the version agents run, downloaded the first time: OpenCode 2 (when the
installation chose it) keeps credentials in its database, so they are read back
with its own 'auth export'.

The version is the installation's setting (Settings → Run OpenCode 2). Pass
--opencode2 to sign in to OpenCode 2 anyway, without moving the agents to it:
the account is stored the same way, and both versions can use it.

OpenCode's accounts are per provider: a project picks one for each provider
(the default otherwise), and an OpenCode agent gets all of them. Run this once
per account, with --account, to keep several for a provider.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			args := []string{"auth", "login"}
			if provider != "" {
				args = append(args, "--provider", provider)
			}
			if method != "" {
				args = append(args, "--method", method)
			}
			creds := a.credentials()
			account = cmp.Or(account, credentials.DefaultAccount)
			if err := credentials.ValidateAccount(account); err != nil {
				return err
			}
			if err := os.MkdirAll(creds.Dir, 0o700); err != nil {
				return err
			}
			// Which OpenCode this installation runs is the daemon's setting. It
			// is asked rather than guessed: the two write credentials in
			// different places, and a login captured in the wrong one is no use
			// to the agents. A daemon that isn't reachable leaves OpenCode 1,
			// which is what an installation that never chose runs.
			version := opencode.V1
			if settings, err := api.NewClient(a.paths.Socket()).Settings(cmd.Context()); err == nil {
				version = opencode.ReleaseOf(settings.OpenCodeVersion)
			}
			if openCodeV2 {
				version = opencode.V2
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Signing in to OpenCode %s\n", version)
			if version == opencode.V2 {
				return openCode2Login(cmd, a, creds, provider, method, account)
			}
			dataHome, err := os.MkdirTemp(creds.Dir, ".opencode-login-")
			if err != nil {
				return err
			}
			defer func() { _ = os.RemoveAll(dataHome) }()
			if err := os.MkdirAll(filepath.Join(dataHome, "opencode"), 0o700); err != nil {
				return err
			}
			// The machine this runs on is the VM, which has no OpenCode of
			// its own: AgentBox fetches the version agents run, once.
			command, err := opencode.Installer{Dir: a.paths.Tools()}.Ensure(cmd.Context(), func(s string) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), s)
			})
			if err != nil {
				return fmt.Errorf("logging agents in to OpenCode needs the OpenCode CLI: %w", err)
			}
			login := exec.CommandContext(cmd.Context(), command, args...)
			login.Env = opencode.HostEnv(dataHome, os.Environ())
			login.Stdin, login.Stdout, login.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := login.Run(); err != nil {
				return fmt.Errorf("opencode %s: %w", strings.Join(args, " "), err)
			}
			auth, err := os.ReadFile(filepath.Join(dataHome, "opencode", "auth.json"))
			if err != nil {
				return errors.New("opencode stored no provider login: run agentbox auth opencode again and finish the login")
			}
			providers, err := credentials.SplitOpenCodeAuth(auth)
			if err != nil {
				return err
			}
			if len(providers) == 0 {
				return errors.New("opencode stored no provider login: run agentbox auth opencode again and finish the login")
			}
			for _, p := range slices.Sorted(maps.Keys(providers)) {
				if err := creds.SaveToolAccount(credentials.ToolOpenCode, p, account, providers[p]); err != nil {
					return err
				}
				if err := savedToolAccount(cmd, a, credentials.ToolOpenCode, p, account); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "provider id to log in to, skipping OpenCode's own list")
	cmd.Flags().StringVar(&method, "method", "", "login method, skipping OpenCode's own list")
	cmd.Flags().BoolVar(&openCodeV2, "opencode2", false, "sign in to OpenCode 2 even when the installation runs OpenCode 1")
	cmd.Flags().StringVar(&account, "account", "", `store it under this name (default "`+credentials.DefaultAccount+`")`)
	addToolAccountCmds(cmd, a, credentials.ToolOpenCode)
	return cmd
}

// openCode2Login runs OpenCode 2's own login against a data home of AgentBox's
// own, then asks it to export what it stored. OpenCode 2 keeps credentials in a
// database rather than an auth.json, so this is the only way to read them back;
// the entry it exports is the same shape AgentBox stores for OpenCode 1.
func openCode2Login(cmd *cobra.Command, a *app, creds credentials.Store, provider, method, account string) error {
	m := &agent.Manager{Paths: a.paths}
	command, err := m.EnsureOpenCode2(cmd.Context(), func(s string) {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), s)
	})
	if err != nil {
		return fmt.Errorf("logging agents in to OpenCode 2 needs its CLI: %w", err)
	}
	dataHome, err := os.MkdirTemp(creds.Dir, ".opencode2-login-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dataHome) }()
	if err := os.MkdirAll(filepath.Join(dataHome, "opencode"), 0o700); err != nil {
		return err
	}
	args := []string{"auth", "login", "--standalone"}
	if method != "" {
		args = append(args, "--method", method)
	}
	if provider != "" {
		args = append(args, provider)
	}
	login := exec.CommandContext(cmd.Context(), command, args...)
	login.Env = opencode.HostEnv(dataHome, os.Environ())
	login.Stdin, login.Stdout, login.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := login.Run(); err != nil {
		return fmt.Errorf("opencode %s: %w", strings.Join(args, " "), err)
	}
	export, err := opencode.Export(cmd.Context(), command, dataHome)
	if err != nil {
		return err
	}
	providers, err := credentials.SplitOpenCodeExport(export)
	if err != nil {
		return err
	}
	if len(providers) == 0 {
		return errors.New("opencode stored no provider login: run agentbox auth opencode again and finish the login")
	}
	for _, p := range slices.Sorted(maps.Keys(providers)) {
		if err := creds.SaveToolAccount(credentials.ToolOpenCode, p, account, providers[p]); err != nil {
			return err
		}
		if err := savedToolAccount(cmd, a, credentials.ToolOpenCode, p, account); err != nil {
			return err
		}
	}
	return nil
}

func newAuthStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which agent logins are configured",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			// The daemon answers for the installation it is, which is the one
			// the profile names: the accounts it holds, whether Anthropic still
			// takes each Claude token, and whose the default GitHub token is.
			status, err := c.Auth(cmd.Context())
			if err != nil {
				return err
			}
			// The daemon checks a token it has no fresh answer for in the
			// background, so the app can poll without waiting on Anthropic.
			// One command should still answer with what that check found, so
			// it waits a moment for the answer the next poll would have.
			if uncheckedClaude(status) {
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					select {
					case <-cmd.Context().Done():
						return cmd.Context().Err()
					case <-time.After(300 * time.Millisecond):
					}
					next, err := c.Auth(cmd.Context())
					if err != nil {
						break
					}
					status = next
					if !uncheckedClaude(status) {
						break
					}
				}
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			claude := "not configured (run: agentbox auth claude)"
			if len(status.ClaudeAccounts) > 0 {
				names := make([]string, 0, len(status.ClaudeAccounts))
				for _, acc := range status.ClaudeAccounts {
					names = append(names, accountName(acc.Name, acc.Default))
				}
				claude = fmt.Sprintf("%d account(s): %s", len(status.ClaudeAccounts), strings.Join(names, ", "))
			}
			_, _ = fmt.Fprintf(w, "Claude Code\t%s\n", claude)
			for _, acc := range status.ClaudeAccounts {
				_, _ = fmt.Fprintf(w, "  %s\t%s\n", acc.Name, apiClaudeStatus(acc))
			}
			for _, tool := range credentials.AccountTools {
				accounts := accountsOf(status, tool)
				if len(accounts) == 0 {
					_, _ = fmt.Fprintf(w, "%s\tnot configured (run: agentbox auth %s)\n", credentials.ToolName(tool), tool)
					continue
				}
				_, _ = fmt.Fprintf(w, "%s\t%d account(s)\n", credentials.ToolName(tool), len(accounts))
				for _, acc := range accounts {
					_, _ = fmt.Fprintf(w, "  %s\t%s\n", apiToolLabel(acc), apiToolStatus(acc))
				}
			}
			gh := "not configured (run: agentbox auth github)"
			if len(status.GitHubAccounts) > 0 {
				names := make([]string, 0, len(status.GitHubAccounts))
				for _, acc := range status.GitHubAccounts {
					names = append(names, accountName(acc.Name, acc.Default))
				}
				gh = fmt.Sprintf("%d account(s): %s", len(status.GitHubAccounts), strings.Join(names, ", "))
				switch {
				case status.GitHubUser != "":
					gh += ", signed in as " + status.GitHubUser
				case status.GitHubError != "":
					gh += ", which GitHub couldn't be asked about right now"
				}
			}
			_, _ = fmt.Fprintf(w, "GitHub\t%s\n", gh)
			return w.Flush()
		},
	}
}

// uncheckedClaude is whether a stored token has no answer from Anthropic yet:
// the daemon's background check hasn't landed, or couldn't ask.
func uncheckedClaude(st api.AuthStatus) bool {
	for _, acc := range st.ClaudeAccounts {
		if acc.Valid == "" {
			return true
		}
	}
	return false
}

// defaultClaudeAccountName is the account new agents get when nothing else
// names one, as the daemon that holds the accounts answers it.
func defaultClaudeAccountName(cmd *cobra.Command, c *api.Client) (string, error) {
	status, err := c.Auth(cmd.Context())
	if err != nil {
		return "", err
	}
	for _, acc := range status.ClaudeAccounts {
		if acc.Default {
			return acc.Name, nil
		}
	}
	return "", nil
}

// accountsOf is one tool's accounts in the daemon's answer.
func accountsOf(st api.AuthStatus, tool string) []api.ToolAccount {
	switch tool {
	case credentials.ToolCodex:
		return st.CodexAccounts
	case credentials.ToolOpenCode:
		return st.OpenCodeAccounts
	case credentials.ToolCursor:
		return st.CursorAccounts
	}
	return nil
}

// accountName is "work (default)".
func accountName(name string, def bool) string {
	if def {
		return name + " (default)"
	}
	return name
}

// boolWord fills a table's yes column.
func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return ""
}

// apiClaudeStatus is the line under a Claude Code account: whether Anthropic
// still takes its token, and when it was stored. A token nobody could check is
// never called dead, since an offline machine mustn't read as a revoked login.
func apiClaudeStatus(acc api.ClaudeAccount) string {
	saved := "saved on an unknown date"
	if !acc.SavedAt.IsZero() {
		saved = "saved " + acc.SavedAt.Format(time.DateOnly)
	}
	switch acc.Valid {
	case "valid":
		return "valid, " + saved
	case "rejected":
		login := "agentbox auth claude"
		if !acc.Default {
			login += " --account " + acc.Name
		}
		return fmt.Sprintf("rejected, run %s (%s)", login, saved)
	default:
		return "not checked, " + saved
	}
}

// apiToolLabel and apiToolStatus are the words the accounts page uses, read from
// the daemon's answer rather than from this machine's store.
func apiToolLabel(acc api.ToolAccount) string {
	label := acc.Name
	if acc.Provider != "" {
		label = acc.Provider + "/" + acc.Name
	}
	if acc.Default {
		label += " (default)"
	}
	return label
}

func apiToolStatus(acc api.ToolAccount) string {
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
	if acc.Expiry != nil {
		kind += ", until " + acc.Expiry.Format("2 January 2006")
	}
	return kind
}

func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("stdin is not a terminal: use --token-stdin")
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

// newAuthGitHubCmd stores a GitHub token for agents, so `gh` and the GitHub
// API work inside them and AgentBox can show each agent's pull request.
func newAuthGitHubCmd(a *app) *cobra.Command {
	var fromStdin bool
	var account string
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Store a GitHub token for agents",
		Long: `Stores a GitHub token for agents, as GH_TOKEN and GITHUB_TOKEN, so 'gh' and
the GitHub API work inside them without logging in. AgentBox also uses it to
show which pull request each agent's branch has, and whether its checks pass.

Without --token-stdin, the token comes from 'gh auth token' on this machine.
It is checked against GitHub before it is stored.

Tokens are stored under a name, so this machine can hold several GitHub
accounts: run this once per account, with --account. A project picks which
one its agents use (agentbox github-account), and otherwise the default one.
Agents are told to read pull requests, not to push or merge.`,
		Example: `  agentbox auth github
  gh auth token | agentbox auth github --token-stdin
  agentbox auth github --account work
  agentbox auth github rename default personal
  agentbox auth github remove personal`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := githubToken(cmd, fromStdin)
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			user, err := c.SaveGitHubToken(cmd.Context(), account, token)
			if err != nil {
				return err
			}
			if account == "" {
				account = credentials.DefaultAccount
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Saved the GitHub account %q for agents, as %s\n", account, user)
			def, err := defaultGitHubAccountName(cmd, c)
			if err != nil {
				return err
			}
			if def == account {
				_, _ = fmt.Fprintln(out, "Agents use it unless their project picks another one.")
			} else {
				_, _ = fmt.Fprintf(out, "Agents still use %q by default: change that with agentbox auth github default %s, or give it to one project with agentbox github-account <project> %s\n", def, account, account)
			}
			_, _ = fmt.Fprintln(out, "They are told to read pull requests, not to push or merge.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "token-stdin", false, "read the token from stdin instead of asking gh for it")
	cmd.Flags().StringVar(&account, "account", "", `store it under this name (default "`+credentials.DefaultAccount+`")`)
	cmd.AddCommand(newAuthGitHubListCmd(a), newAuthGitHubDefaultCmd(a), newAuthGitHubRenameCmd(a), newAuthGitHubRemoveCmd(a))
	return cmd
}

func newAuthGitHubListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the stored GitHub accounts",
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
			out := cmd.OutOrStdout()
			if len(status.GitHubAccounts) == 0 {
				_, _ = fmt.Fprintln(out, "No GitHub accounts yet. Add one with: agentbox auth github")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "ACCOUNT\tDEFAULT\tSAVED")
			for _, acc := range status.GitHubAccounts {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", acc.Name, orDash(boolWord(acc.Default)), acc.SavedAt.Local().Format(time.DateTime))
			}
			return w.Flush()
		},
	}
}

func newAuthGitHubDefaultCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "default <account>",
		Short: "Pick the GitHub account agents use when their project doesn't name one",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.SetDefaultGitHubAccount(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "New agents use the GitHub account %q unless their project picks another one\n", args[0])
			return nil
		},
	}
}

// defaultGitHubAccountName is the account new agents get when nothing else names one.
func defaultGitHubAccountName(cmd *cobra.Command, c *api.Client) (string, error) {
	auth, err := c.Auth(cmd.Context())
	if err != nil {
		return "", err
	}
	for _, acc := range auth.GitHubAccounts {
		if acc.Default {
			return acc.Name, nil
		}
	}
	return "", nil
}

// githubToken reads the token from stdin, or borrows the one the host's gh has.
func githubToken(cmd *cobra.Command, fromStdin bool) (string, error) {
	if fromStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", err
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", errors.New("no token on stdin")
		}
		return token, nil
	}
	out, err := exec.CommandContext(cmd.Context(), "gh", "auth", "token").Output()
	if errors.Is(err, exec.ErrNotFound) {
		return "", errors.New("gh isn't installed on this machine: install the GitHub CLI and run 'gh auth login', or pipe a token in with --token-stdin")
	}
	if err != nil {
		return "", errors.New("gh has no token on this machine: run 'gh auth login', or pipe a token in with --token-stdin")
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", errors.New("gh returned no token: run 'gh auth login', or pipe a token in with --token-stdin")
	}
	return token, nil
}

func newAuthGitHubRenameCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <account> <new-name>",
		Short: "Give a stored GitHub account another name",
		Long: `Everything that names the account follows it: the machine's default, each
project's account and each agent's. The token stays the same, so agents that
hold it keep running without a restart. A name another account already has is
refused.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			got, err := c.RenameGitHubAccount(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Renamed the GitHub account %q to %q\n", got.Old, got.Name)
			if len(got.Projects) > 0 {
				_, _ = fmt.Fprintf(out, "Projects carried over: %s\n", strings.Join(got.Projects, ", "))
			}
			if len(got.Agents) > 0 {
				_, _ = fmt.Fprintf(out, "Agents carried over: %s. They keep the same token, so none needs a restart.\n", strings.Join(got.Agents, ", "))
			}
			return nil
		},
	}
}

func newAuthGitHubRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <account>",
		Short: "Forget a stored GitHub account",
		Long: `Agents that already have this account's token keep working with it:
the token was written into them when they were created.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveGitHubAccount(cmd.Context(), args[0]); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Removed the GitHub account %q\n", args[0])
			def, err := defaultGitHubAccountName(cmd, c)
			if err != nil {
				return err
			}
			if def == "" {
				_, _ = fmt.Fprintln(out, "No GitHub accounts left for agents.")
			} else {
				_, _ = fmt.Fprintf(out, "New agents now use %q by default. Projects that picked the removed account need another one: agentbox github-account <project> <account>\n", def)
			}
			return nil
		},
	}
}
