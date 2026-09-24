package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// tokenWindow is what `agentbox tokens` looks back over when not told: the
// length of Claude's usage-limit window, since "what spent my limit" is the
// question it is usually asked.
const tokenWindow = 5 * time.Hour

func newTokensCmd(a *app) *cobra.Command {
	var since string
	var turns int
	cmd := &cobra.Command{
		Use:   "tokens [project | project/agent]",
		Short: "Show what each agent's chat spent: tokens, context and estimated cost",
		Long: `Shows the token ledger: what every agent's chat spent, turn by turn, including
agents that have since been retired. It covers the chat only — a Claude Code
started by hand in an agent's terminal isn't counted.

Every model call sends the whole conversation again, so CACHE READ is usually
most of it, and PEAK CONTEXT is what each call of the agent's fullest turn was
carrying. COST is the AI tool's own estimate at API prices: a subscription
isn't billed per token, but it is the fairest single number to compare by.

  agentbox tokens                         # every project, the last 5 hours
  agentbox tokens acme --since 7d      # one project, the last week
  agentbox tokens acme/agent-24 --turns 30
  agentbox tokens --since all`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			q := api.TokenQuery{}
			if len(args) == 1 {
				q.Project, q.Agent, _ = strings.Cut(args[0], "/")
			}
			switch since {
			case "all":
			case "":
				q.Since = time.Now().Add(-tokenWindow)
			default:
				d, err := parseStretch(since)
				if err != nil {
					return err
				}
				q.Since = time.Now().Add(-d)
			}
			out := cmd.OutOrStdout()
			if turns > 0 {
				lines, err := c.TokenTurns(cmd.Context(), q, turns)
				if err != nil {
					return err
				}
				return renderTokenTurns(out, lines, q.Agent == "")
			}
			report, err := c.Tokens(cmd.Context(), q)
			if err != nil {
				return err
			}
			// The account's own limits come first: they are what the ledger is
			// being read against. A daemon too old to know them just has none.
			if limits, err := c.ClaudeLimits(cmd.Context()); err == nil {
				renderLimits(out, limits, time.Now())
			}
			return renderTokens(out, report, q.Agent != "")
		},
	}
	cmd.Flags().StringVar(&since, "since", "", `how far back to look, like 5h, 90m or 7d, or "all" (default 5h)`)
	cmd.Flags().IntVar(&turns, "turns", 0, "list the last N lines of the ledger instead of adding them up")
	return cmd
}

// parseStretch reads a stretch of time, in days as well as what Go's durations
// know.
func parseStretch(raw string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--since %q isn't a stretch of time like 5h, 90m or 7d, or \"all\"", raw)
	}
	return d, nil
}

func renderTokens(w io.Writer, r api.TokenReport, oneAgent bool) error {
	span := "since the ledger began"
	if r.Since != nil {
		span = "since " + r.Since.Local().Format("Jan 2 15:04")
	}
	if len(r.Agents) == 0 {
		fmt.Fprintf(w, "Nothing spent %s.\n", span)
		return nil
	}
	fmt.Fprintf(w, "%s tokens %s, %s estimated at API prices.\n\n", humanTokens(r.Total), span, usd(r.CostUSD))
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tTOKENS\tCACHE READ\tCACHE WRITE\tINPUT\tOUTPUT\tCOST\tTURNS\tPEAK CONTEXT\tLAST")
	for _, a := range r.Agents {
		name := a.Ref
		if !a.Exists {
			name += " (gone)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", name, humanTokens(a.Total), humanTokens(a.CacheRead),
			humanTokens(a.CacheWrite), humanTokens(a.Input), humanTokens(a.Output), usd(a.CostUSD), a.Turns,
			humanTokens(a.MaxContext), a.LastAt.Local().Format("Jan 2 15:04"))
		if oneAgent || len(a.Models) > 1 {
			for _, m := range a.Models {
				model := m.Model
				if model == "" {
					model = "(unnamed model)"
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t\t\t\n", model, humanTokens(m.Total), humanTokens(m.CacheRead),
					humanTokens(m.CacheWrite), humanTokens(m.Input), humanTokens(m.Output), usd(m.CostUSD))
			}
		}
	}
	return tw.Flush()
}

// renderLimits writes each Claude account's usage limits, as last reported. A
// window that has reset since the reading is said to have, rather than shown
// with a number that no longer describes it.
func renderLimits(w io.Writer, limits []api.ClaudeLimit, now time.Time) {
	if len(limits) == 0 {
		return
	}
	fmt.Fprintln(w, "Claude usage limits, as the last chat on each account reported them:")
	for _, l := range limits {
		parts := make([]string, 0, len(l.Windows))
		for _, win := range l.Windows {
			if !win.ResetsAt.IsZero() && now.After(win.ResetsAt) {
				parts = append(parts, fmt.Sprintf("%s reset at %s", win.Label, win.ResetsAt.Local().Format("Jan 2 15:04")))
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %.0f%% (resets %s)", win.Label, win.Utilization*100, win.ResetsAt.Local().Format("Jan 2 15:04")))
		}
		status := ""
		if l.Status != "" && l.Status != "allowed" {
			status = " · " + strings.ReplaceAll(l.Status, "_", " ")
		}
		fmt.Fprintf(w, "  %s: %s%s · as of %s\n", l.Account, strings.Join(parts, ", "), status, l.At.Local().Format("Jan 2 15:04"))
	}
	fmt.Fprintln(w)
}

func renderTokenTurns(w io.Writer, lines []api.TokenTurn, showAgent bool) error {
	if len(lines) == 0 {
		fmt.Fprintln(w, "Nothing in the ledger yet.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	header := "WHEN\tKIND\tMODEL\tTOKENS\tCACHE READ\tCACHE WRITE\tINPUT\tOUTPUT\tCOST\tCONTEXT"
	if showAgent {
		header = "AGENT\t" + header
	}
	fmt.Fprintln(tw, header)
	for _, l := range lines {
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s", l.At.Local().Format("Jan 2 15:04:05"), l.Kind, l.Model,
			humanTokens(l.Total), humanTokens(l.CacheRead), humanTokens(l.CacheWrite), humanTokens(l.Input),
			humanTokens(l.Output), usd(l.CostUSD), humanTokens(l.Context))
		if showAgent {
			row = l.Project + "/" + l.Agent + "\t" + row
		}
		fmt.Fprintln(tw, row)
	}
	return tw.Flush()
}

// humanTokens writes a count of tokens the way a context window is usually
// spoken of: 950, 12.4K, 1.3M.
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func usd(v float64) string {
	if v == 0 {
		return "-"
	}
	if v < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}
