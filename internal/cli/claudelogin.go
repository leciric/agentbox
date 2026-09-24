package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/agent"
)

// setupToken logs in to Claude Code the way the app's Setup page does (D59):
// AgentBox runs `claude setup-token` itself, on the Claude Code it installed
// for the host, or one on your PATH, and takes the token out of what it
// prints. Nothing is pasted, and nothing touches your own ~/.claude (D6).
func setupToken(cmd *cobra.Command, a *app) (string, error) {
	out := cmd.ErrOrStderr()
	m := &agent.Manager{Paths: a.paths}
	codes := make(chan string, 1)
	// The code is only wanted when a browser can't reach this machine, so it is
	// read alongside the login rather than before it. The reader is left on
	// stdin when the login finishes without it: the command returns straight
	// after, and the process with it.
	go readCodes(cmd, codes)

	return m.SetupToken(cmd.Context(), agent.SetupTokenEvents{
		Status: func(detail string) { fmt.Fprintln(out, detail) },
		Browser: func(url string) {
			fmt.Fprintf(out, "\nApprove the login here:\n  %s\n\nWaiting for you to approve it…\n", url)
			openBrowser(cmd, url)
		},
		Paste: func(url string) {
			fmt.Fprintf(out, "\nIf that page can't reach this machine, approve this one instead and paste the code it gives you:\n  %s\n", url)
		},
	}, codes)
}

// readCodes takes a line at a time from stdin for the fallback flow. Reading
// the code with the terminal's echo off would hide a mistyped paste, and it
// isn't a secret on its own: it is spent the moment Claude Code redeems it.
func readCodes(cmd *cobra.Command, codes chan<- string) {
	scanner := bufio.NewScanner(cmd.InOrStdin())
	for scanner.Scan() {
		if code := strings.TrimSpace(scanner.Text()); code != "" {
			codes <- code
		}
	}
}

// openBrowser opens the sign-in page, best effort: AgentBox holds $BROWSER
// while Claude Code runs, so this is what opens it instead. On a machine with
// no browser — over SSH, say — the URL above is the whole answer.
func openBrowser(cmd *cobra.Command, url string) {
	open := exec.CommandContext(cmd.Context(), "xdg-open", url)
	open.Stdout, open.Stderr = nil, nil
	if err := open.Start(); err == nil {
		go open.Wait()
		return
	}
	if browser := os.Getenv("BROWSER"); browser != "" {
		fallback := exec.CommandContext(cmd.Context(), browser, url)
		if err := fallback.Start(); err == nil {
			go fallback.Wait()
		}
	}
}
