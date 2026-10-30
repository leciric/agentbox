package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// claudeLogin logs in to Claude Code the way the app's Setup page does (D59):
// the daemon runs `claude setup-token` on the machine that holds its accounts,
// with the Claude Code installed there, and reports the page to approve. This
// side prints that page, opens it, and hands back a code when a browser can't
// reach that machine. Nothing is pasted unless it has to be, and nothing
// touches your own ~/.claude (D6).
func claudeLogin(cmd *cobra.Command, c *api.Client, account, where string) error {
	ctx := cmd.Context()
	out := cmd.ErrOrStderr()
	job, err := c.StartClaudeLogin(ctx, api.ClaudeLoginRequest{Account: account})
	if err != nil {
		return err
	}
	// The code is only wanted when a browser can't reach that machine, so it is
	// read alongside the login rather than before it. The reader is left on
	// stdin when the login finishes without it: the command returns straight
	// after, and the process with it.
	codes := make(chan string, 1)
	go readCodes(cmd, codes)
	_, _ = fmt.Fprintf(out, "Starting the Claude Code login on %s…\n", where)
	opened, prompted, sent := false, false, false
	for {
		login, err := c.ClaudeLogin(ctx, job.ID)
		if err != nil {
			return err
		}
		if login.URL != "" && !opened {
			opened = true
			_, _ = fmt.Fprintf(out, "\nApprove the login here:\n  %s\n\nWaiting for you to approve it…\n", login.URL)
			openBrowser(cmd, login.URL)
		}
		if login.CodeURL != "" && !prompted {
			prompted = true
			_, _ = fmt.Fprintf(out, "\nIf that page can't reach %s, approve this one instead and paste the code it gives you:\n  %s\n", where, login.CodeURL)
		}
		select {
		case code := <-codes:
			if !sent {
				sent = true
				if err := c.ClaudeLoginCode(ctx, job.ID, code); err != nil {
					return err
				}
			}
		default:
		}
		switch login.Status {
		case "succeeded":
			return nil
		case "failed":
			return fmt.Errorf("the Claude Code login failed: %s", login.Error)
		case "cancelled":
			return errors.New("the Claude Code login was cancelled")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// readCodes takes a line at a time from stdin for the fallback flow. Reading
// the code with the terminal's echo off would hide a mistyped paste, and it
// isnt a secret on its own: it is spent the moment Claude Code redeems it.
func readCodes(cmd *cobra.Command, codes chan<- string) {
	scanner := bufio.NewScanner(cmd.InOrStdin())
	for scanner.Scan() {
		if code := strings.TrimSpace(scanner.Text()); code != "" {
			codes <- code
		}
	}
}

// openBrowser opens the sign-in page, best effort: the daemon sets $BROWSER
// while Claude Code runs, so this is what opens it instead. On a machine with
// no browser — over SSH, say — the URL above is the whole answer.
func openBrowser(cmd *cobra.Command, url string) {
	open := exec.CommandContext(cmd.Context(), "xdg-open", url)
	open.Stdout, open.Stderr = nil, nil
	if err := open.Start(); err == nil {
		go func() { _ = open.Wait() }()
		return
	}
	if browser := os.Getenv("BROWSER"); browser != "" {
		fallback := exec.CommandContext(cmd.Context(), browser, url)
		if err := fallback.Start(); err == nil {
			go func() { _ = fallback.Wait() }()
		}
	}
}
