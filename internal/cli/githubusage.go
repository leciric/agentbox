package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newGitHubCmd groups what AgentBox can say about its use of GitHub.
func newGitHubCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "See how AgentBox uses GitHub",
	}
	cmd.AddCommand(newGitHubUsageCmd(a))
	return cmd
}

func newGitHubUsageCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show what spends each GitHub account's hourly rate limit",
		Long: `Shows every request the daemon made of GitHub since it started, by account,
by what in AgentBox asked (the pull request list, the watch, the pull request
view, issue pickup…) and by endpoint, busiest first, with what GitHub last said
is left of the account's hourly limits.

GitHub counts a request whatever sent it, and the token is shared: agents' own
gh in their machines spends the same 5,000 an hour. OTHERS is what GitHub says
was used since the last reset beyond AgentBox's own requests — mostly agents'
gh, plus anything else of yours using the token.

COUNTED is what GitHub held against the limit: a 304 (an unchanged answer
asked for with its ETag) is free. SHARED reads joined one already in flight,
HELD ones waited because the limit was spent or nearly.

  agentbox github usage
  agentbox github usage --all   # every endpoint, not only the busiest`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			usage, err := c.GitHubUsage(cmd.Context())
			if err != nil {
				return err
			}
			return renderGitHubUsage(cmd.OutOrStdout(), usage, all, time.Now())
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "list every endpoint, not only the 15 busiest")
	return cmd
}

// githubUsageRows is how many caller and endpoint rows each account shows
// without --all.
const githubUsageRows = 15

func renderGitHubUsage(out io.Writer, usage api.GitHubUsage, all bool, now time.Time) error {
	if len(usage.Accounts) == 0 {
		_, err := fmt.Fprintln(out, "AgentBox hasn't asked GitHub anything since the daemon started.")
		return err
	}
	for i, u := range usage.Accounts {
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		name := u.Account
		switch {
		case name == "":
			name = "token " + u.Token
		case u.Login != "":
			name = fmt.Sprintf("%s (%s)", u.Login, u.Account)
		}
		_, _ = fmt.Fprintf(out, "%s, since %s\n", name, u.Since.Local().Format("Jan 2 15:04"))
		for _, r := range u.Rates {
			line := fmt.Sprintf("  %-8s %d of %d left, resets %s", r.Resource, r.Remaining, r.Limit, r.Reset.Local().Format("15:04"))
			if r.Resource == "core" && r.Used > 0 {
				line += fmt.Sprintf("; of %d used: AgentBox %d, others (agents' gh, you) %d", r.Used, r.Ours, r.Others)
			}
			if r.PausedUntil != nil && r.PausedUntil.After(now) {
				line += fmt.Sprintf("; paused until %s", r.PausedUntil.Local().Format("15:04"))
			}
			if !r.At.IsZero() {
				line += fmt.Sprintf(" (read %s)", ago(r.At))
			}
			_, _ = fmt.Fprintln(out, line)
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "  CALLER\tENDPOINT\tCOUNTED\tSENT\t304\tSHARED\tHELD\tFAILED\tLAST")
		calls := u.Calls
		if !all && len(calls) > githubUsageRows {
			calls = calls[:githubUsageRows]
		}
		for _, c := range calls {
			endpoint := c.Endpoint
			if c.Method != "GET" {
				endpoint = c.Method + " " + endpoint
			}
			_, _ = fmt.Fprintf(w, "  %s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", c.Caller, endpoint, c.Counted, c.Sent,
				zeroDash(c.NotModified), zeroDash(c.Shared), zeroDash(c.Held), zeroDash(c.Failed), ago(c.Last))
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if hidden := len(u.Calls) - len(calls); hidden > 0 {
			_, _ = fmt.Fprintf(out, "  …and %d more (--all)\n", hidden)
		}
	}
	return nil
}

func zeroDash(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprint(n)
}
