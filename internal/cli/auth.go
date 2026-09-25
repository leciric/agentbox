package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/opencode"
)

func newAuthCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log agents in to Claude Code, Codex and OpenCode",
		Long: `Agents use AgentBox's own logins, never your host's ~/.claude, ~/.codex or
~/.local/share/opencode. Sharing those directories would let an agent change
your host configuration.`,
	}
	cmd.AddCommand(newAuthClaudeCmd(a), newAuthCodexCmd(a), newAuthOpenCodeCmd(a), newAuthGitHubCmd(a), newAuthStatusCmd(a))
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
			var token string
			if fromStdin {
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				token = string(b)
			} else {
				t, err := setupToken(cmd, a)
				if err != nil {
					return err
				}
				token = t
			}
			creds := a.credentials()
			if account == "" {
				account = credentials.DefaultAccount
			}
			if err := creds.SaveClaudeToken(account, token); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Saved the Claude Code account %q for agents in %s\n", account, creds.ClaudeTokenPath(account))
			def, err := creds.DefaultClaudeAccount()
			if err != nil {
				return err
			}
			if def == account {
				fmt.Fprintf(out, "Agents use it unless their project picks another one.\n")
			} else {
				fmt.Fprintf(out, "Agents still use %q by default: change that with agentbox auth claude default %s, or give it to one project with agentbox claude-account <project> %s\n", def, account, account)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "token-stdin", false, "read the token from stdin")
	cmd.Flags().StringVar(&account, "account", "", `store it under this name (default "`+credentials.DefaultAccount+`")`)
	cmd.AddCommand(newAuthClaudeListCmd(a), newAuthClaudeDefaultCmd(a), newAuthClaudeRemoveCmd(a))
	return cmd
}

func newAuthClaudeListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the stored Claude Code accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			accounts, err := a.credentials().ClaudeAccounts()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(accounts) == 0 {
				fmt.Fprintln(out, "No Claude Code accounts yet. Add one with: agentbox auth claude")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "ACCOUNT\tDEFAULT\tSAVED")
			for _, acc := range accounts {
				def := ""
				if acc.Default {
					def = "yes"
				}
				// A token stored before AgentBox recorded the date has none,
				// and the file's own timestamp isn't one: copying a
				// credentials directory rewrites it.
				saved := "unknown"
				if acc.SavedAtKnown {
					saved = acc.SavedAt.Local().Format(time.DateTime)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", acc.Name, orDash(def), saved)
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
			if err := a.credentials().SetDefaultClaudeAccount(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "New agents use the Claude Code account %q unless their project picks another one\n", args[0])
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
			creds := a.credentials()
			if err := creds.RemoveClaudeAccount(args[0]); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Removed the Claude Code account %q\n", args[0])
			def, err := creds.DefaultClaudeAccount()
			if err != nil {
				return err
			}
			if def == "" {
				fmt.Fprintln(out, "No accounts left: new Claude Code agents need one (agentbox auth claude)")
			} else {
				fmt.Fprintf(out, "New agents now use %q by default. Projects that picked the removed account need another one: agentbox claude-account <project> <account>\n", def)
			}
			return nil
		},
	}
}

func newAuthCodexCmd(a *app) *cobra.Command {
	var apiKey bool
	cmd := &cobra.Command{
		Use:   "codex",
		Short: "Log agents in to Codex",
		Long: `Runs 'codex login' against AgentBox's own Codex home, so the login is separate
from your host's ~/.codex. New agents get a copy of it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			args := []string{"login"}
			if apiKey {
				args = append(args, "--with-api-key")
			}
			creds := a.credentials()
			if err := os.MkdirAll(creds.CodexHome(), 0o700); err != nil {
				return err
			}
			login := exec.CommandContext(cmd.Context(), "codex", args...)
			login.Env = append(os.Environ(), "CODEX_HOME="+creds.CodexHome())
			login.Stdin, login.Stdout, login.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := login.Run(); err != nil {
				if errors.Is(err, exec.ErrNotFound) {
					return errors.New("logging agents in to Codex needs the Codex CLI on this machine, and it isn't installed: install it (npm install -g @openai/codex, or a release from https://github.com/openai/codex/releases), then run agentbox auth codex again")
				}
				return fmt.Errorf("codex %s: %w", strings.Join(args, " "), err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Stored the Codex login for agents in %s\n", creds.CodexHome())
			return nil
		},
	}
	cmd.Flags().BoolVar(&apiKey, "api-key-stdin", false, "read an OpenAI API key from stdin instead of logging in with ChatGPT")
	return cmd
}

// newAuthOpenCodeCmd logs agents in to OpenCode. OpenCode's logins are
// provider API keys, all of them in one auth.json, and it reads that file from
// $XDG_DATA_HOME/opencode — so pointing that one variable at AgentBox's own
// directory is the whole isolation, as CODEX_HOME is for Codex (D6).
func newAuthOpenCodeCmd(a *app) *cobra.Command {
	var provider, method string
	cmd := &cobra.Command{
		Use:   "opencode",
		Short: "Log agents in to OpenCode",
		Long: `Runs 'opencode auth login' against AgentBox's own OpenCode data directory, so
the login is separate from your host's ~/.local/share/opencode. New agents get a
copy of its auth.json.

OpenCode's models belong to the providers you log in to: the one you add here is
what an OpenCode agent can run, and AgentBox asks OpenCode itself for the list.`,
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
			if err := os.MkdirAll(creds.OpenCodeHome(), 0o700); err != nil {
				return err
			}
			login := exec.CommandContext(cmd.Context(), opencode.Command, args...)
			login.Env = append(os.Environ(), "XDG_DATA_HOME="+creds.OpenCodeDataHome())
			login.Stdin, login.Stdout, login.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := login.Run(); err != nil {
				if errors.Is(err, exec.ErrNotFound) {
					return errors.New("logging agents in to OpenCode needs the OpenCode CLI on this machine, and it isn't installed: install it (npm install -g opencode-ai, or see https://opencode.ai), then run agentbox auth opencode again")
				}
				return fmt.Errorf("opencode %s: %w", strings.Join(args, " "), err)
			}
			if !creds.HasOpenCodeLogin() {
				return fmt.Errorf("opencode stored no provider login in %s: run agentbox auth opencode again and finish the login", creds.OpenCodeHome())
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Stored the OpenCode login for agents in %s\n", creds.OpenCodeHome())
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "provider id to log in to, skipping OpenCode's own list")
	cmd.Flags().StringVar(&method, "method", "", "login method, skipping OpenCode's own list")
	return cmd
}

func newAuthStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which agent logins are configured",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			creds := a.credentials()
			accounts, err := creds.ClaudeAccounts()
			if err != nil {
				return err
			}
			claude, codex := "not configured (run: agentbox auth claude)", "not configured (run: agentbox auth codex)"
			oc := "not configured (run: agentbox auth opencode)"
			if creds.HasOpenCodeLogin() {
				oc = "login stored"
			}
			if len(accounts) > 0 {
				names := make([]string, 0, len(accounts))
				for _, acc := range accounts {
					if acc.Default {
						names = append(names, acc.Name+" (default)")
						continue
					}
					names = append(names, acc.Name)
				}
				claude = fmt.Sprintf("%d account(s): %s", len(accounts), strings.Join(names, ", "))
			}
			if creds.HasCodexLogin() {
				codex = "login stored"
			}
			ghAccounts, err := creds.GitHubAccounts()
			if err != nil {
				return err
			}
			gh := "not configured (run: agentbox auth github)"
			if len(ghAccounts) > 0 {
				names := make([]string, 0, len(ghAccounts))
				for _, acc := range ghAccounts {
					if acc.Default {
						names = append(names, acc.Name+" (default)")
						continue
					}
					names = append(names, acc.Name)
				}
				gh = fmt.Sprintf("%d account(s): %s", len(ghAccounts), strings.Join(names, ", "))
			}
			// What Anthropic makes of each stored token. An answer stands for
			// an hour and is shared with the daemon, so asking twice in a row
			// costs one round trip.
			checked := checkClaudeAccounts(cmd.Context(), creds, accounts)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintf(w, "Claude Code\t%s\n", claude)
			for _, acc := range accounts {
				fmt.Fprintf(w, "  %s\t%s\n", acc.Name, claudeAccountStatus(acc, checked[acc.Name]))
			}
			fmt.Fprintf(w, "Codex\t%s\n", codex)
			fmt.Fprintf(w, "OpenCode\t%s\n", oc)
			fmt.Fprintf(w, "GitHub\t%s\n", gh)
			return w.Flush()
		},
	}
}

// checkClaudeAccounts asks Anthropic about every stored token, all at once so
// several accounts cost one wait rather than one each.
func checkClaudeAccounts(ctx context.Context, creds credentials.Store, accounts []credentials.ClaudeAccount) map[string]credentials.Validity {
	out := make(map[string]credentials.Validity, len(accounts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, acc := range accounts {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			v, err := creds.CheckClaudeAccount(ctx, name)
			if err != nil {
				v = credentials.Validity{Detail: err.Error()}
			}
			mu.Lock()
			defer mu.Unlock()
			out[name] = v
		}(acc.Name)
	}
	wg.Wait()
	return out
}

// claudeAccountStatus is the line under a Claude Code account: whether
// Anthropic still takes its token, when it was stored, and what to do when it
// is dead. A token nobody could check is never called dead — an offline
// machine mustn't read as a revoked login.
func claudeAccountStatus(acc credentials.ClaudeAccount, v credentials.Validity) string {
	saved := "saved on an unknown date"
	if acc.SavedAtKnown {
		saved = "saved " + acc.SavedAt.Format(time.DateOnly)
	}
	switch v.State {
	case credentials.TokenValid:
		return "valid, " + saved
	case credentials.TokenRejected:
		login := "agentbox auth claude"
		if acc.Name != credentials.DefaultAccount {
			login += " --account " + acc.Name
		}
		return fmt.Sprintf("rejected, run %s (%s)", login, saved)
	default:
		// Whatever went wrong — no connection, a rate limit, an outage — says
		// nothing about the token, and the daemon's log has the detail.
		if v.Detail != "" {
			return "couldn't be checked, " + saved
		}
		return "not checked yet, " + saved
	}
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
			fmt.Fprintf(out, "Saved the GitHub account %q for agents, as %s\n", account, user)
			def, err := defaultGitHubAccountName(cmd, c)
			if err != nil {
				return err
			}
			if def == account {
				fmt.Fprintln(out, "Agents use it unless their project picks another one.")
			} else {
				fmt.Fprintf(out, "Agents still use %q by default: change that with agentbox auth github default %s, or give it to one project with agentbox github-account <project> %s\n", def, account, account)
			}
			fmt.Fprintln(out, "They are told to read pull requests, not to push or merge.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "token-stdin", false, "read the token from stdin instead of asking gh for it")
	cmd.Flags().StringVar(&account, "account", "", `store it under this name (default "`+credentials.DefaultAccount+`")`)
	cmd.AddCommand(newAuthGitHubListCmd(a), newAuthGitHubDefaultCmd(a), newAuthGitHubRemoveCmd(a))
	return cmd
}

func newAuthGitHubListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the stored GitHub accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			accounts, err := a.credentials().GitHubAccounts()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(accounts) == 0 {
				fmt.Fprintln(out, "No GitHub accounts yet. Add one with: agentbox auth github")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "ACCOUNT\tDEFAULT\tSAVED")
			for _, acc := range accounts {
				def := ""
				if acc.Default {
					def = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", acc.Name, orDash(def), acc.SavedAt.Local().Format(time.DateTime))
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
			fmt.Fprintf(cmd.OutOrStdout(), "New agents use the GitHub account %q unless their project picks another one\n", args[0])
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
			fmt.Fprintf(out, "Removed the GitHub account %q\n", args[0])
			def, err := defaultGitHubAccountName(cmd, c)
			if err != nil {
				return err
			}
			if def == "" {
				fmt.Fprintln(out, "No GitHub accounts left for agents.")
			} else {
				fmt.Fprintf(out, "New agents now use %q by default. Projects that picked the removed account need another one: agentbox github-account <project> <account>\n", def)
			}
			return nil
		},
	}
}
