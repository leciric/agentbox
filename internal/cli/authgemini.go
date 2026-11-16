package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
)

// newAuthGeminiCmd signs agents in to Gemini CLI, through the daemon so the
// app's Settings and this command keep the same accounts: a Google sign-in
// that Gemini CLI itself makes (agent.GeminiSignIn), or a Gemini API key.
func newAuthGeminiCmd(a *app) *cobra.Command {
	var fromStdin, logout bool
	var account string
	cmd := &cobra.Command{
		Use:   "gemini",
		Short: "Sign agents in to Gemini CLI",
		Long: `Signs agents in to Gemini CLI with your Google account: AgentBox runs Gemini
CLI's own sign-in, prints the Google page to approve, and asks for the code that
page ends on. Or pass --api-key-stdin to give it a Gemini API key from Google AI
Studio instead.

The sign-in is kept in AgentBox's own directory, never your host's ~/.gemini.
Agents get a copy of their project's account, or the default one. Run it once
per account, with --account, to keep several.`,
		Example: `  agentbox auth gemini
  printenv GEMINI_API_KEY | agentbox auth gemini --api-key-stdin --account studio
  agentbox auth gemini --logout --account work`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case logout:
				name := account
				if name == "" {
					if name, err = a.credentials().DefaultToolAccount(credentials.ToolGemini, ""); err != nil {
						return err
					}
				}
				if name == "" {
					_, _ = fmt.Fprintln(out, "Agents aren't signed in to Gemini CLI")
					return nil
				}
				if err := c.RemoveToolAccount(cmd.Context(), credentials.ToolGemini, "", name); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(out, "Removed the Gemini CLI account %q. Agents already created keep the login they were given.\n", name)
				return nil
			case fromStdin:
				b, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
				if err != nil {
					return err
				}
				key := strings.TrimSpace(string(b))
				if key == "" {
					return errors.New("no API key on stdin")
				}
				if err := c.SaveToolAccount(cmd.Context(), credentials.ToolGemini, api.ToolAccountRequest{Account: account, APIKey: key}); err != nil {
					return err
				}
				return savedToolAccount(cmd, a, credentials.ToolGemini, "", cmpOr(account, credentials.DefaultAccount))
			}
			return geminiSignIn(cmd, c, account)
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "api-key-stdin", false, "read a Gemini API key from stdin instead of signing in with Google")
	cmd.Flags().BoolVar(&logout, "logout", false, "forget the Gemini CLI account")
	cmd.Flags().StringVar(&account, "account", "", `the account to sign in or out (default "`+credentials.DefaultAccount+`")`)
	cmd.MarkFlagsMutuallyExclusive("api-key-stdin", "logout")
	addToolAccountCmds(cmd, a, credentials.ToolGemini)
	return cmd
}

// geminiSignIn follows the daemon's sign-in job: it prints the page to
// approve, reads the code from the terminal, and waits for the job to keep it.
// Interrupting it cancels the job.
func geminiSignIn(cmd *cobra.Command, c *api.Client, account string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	job, err := c.StartGeminiLogin(ctx, account)
	if err != nil {
		return err
	}
	defer func() {
		if ctx.Err() != nil {
			_, _ = c.CancelJob(context.WithoutCancel(ctx), job.ID)
		}
	}()
	_, _ = fmt.Fprintln(out, "Starting Gemini CLI's Google sign-in (the first time, AgentBox installs Gemini CLI)...")
	lines := make(chan string)
	go func() {
		s := bufio.NewScanner(cmd.InOrStdin())
		for s.Scan() {
			lines <- s.Text()
		}
		close(lines)
	}()
	asked := false
	for {
		login, err := c.GeminiLogin(ctx, job.ID)
		if err != nil {
			return err
		}
		switch login.Status {
		case api.JobSucceeded:
			_, _ = fmt.Fprintf(out, "Signed agents in to Gemini CLI as the account %q\n", login.Account)
			return nil
		case api.JobRunning:
		default:
			return errors.New(cmpOr(login.Error, "the sign-in ended without one"))
		}
		if login.CodeURL != "" && !asked {
			asked = true
			_, _ = fmt.Fprintf(out, "Open this page, sign in with Google, and paste the code it shows:\n\n  %s\n\nCode: ", login.CodeURL)
			select {
			case line, ok := <-lines:
				if !ok || strings.TrimSpace(line) == "" {
					return errors.New("no code: run agentbox auth gemini again and paste the code the page shows")
				}
				if err := c.GeminiLoginCode(ctx, job.ID, strings.TrimSpace(line)); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(out, "Checking the code...")
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
