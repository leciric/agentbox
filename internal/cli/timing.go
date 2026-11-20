package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// timingWindow is what `agentbox timing` looks back over when not told.
const timingWindow = 24 * time.Hour

func newTimingCmd(a *app) *cobra.Command {
	var since, agentName, project string
	var top int
	cmd := &cobra.Command{
		Use:   "timing",
		Short: "Show where agents' time went: the model, tools and heavy commands",
		Long: `Shows where agents' chat turns spent their time: waiting on the model (from each
request to its last token, and the wait for its first), running tools, and the
rest; then the slowest tools, tool calls and heavy commands (tests and builds,
with the worker count each was given and how it exited).

A turn's tool time counts calls run in parallel once, and a subagent's own
calls are inside its Agent call. Heavy commands run inside tool calls or a
terminal, so their time is part of the tools', not apart from it. Timings are
kept for 30 days.

  agentbox timing                          # every agent, the last 24 hours
  agentbox timing --agent acme/agent-12 --since 1h
  agentbox timing --agent agent-12         # that name in every project
  agentbox timing --project acme --since 7d --top 20`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			q := api.TimingQuery{Project: project, Agent: agentName, Top: top}
			if p, name, ok := strings.Cut(agentName, "/"); ok {
				if project != "" && project != p {
					return fmt.Errorf("--agent %s isn't in --project %s", agentName, project)
				}
				q.Project, q.Agent = p, name
			}
			switch since {
			case "all":
			case "":
				q.Since = time.Now().Add(-timingWindow)
			default:
				d, err := parseStretch(since)
				if err != nil {
					return err
				}
				q.Since = time.Now().Add(-d)
			}
			r, err := c.Timing(cmd.Context(), q)
			if err != nil {
				return err
			}
			renderTiming(cmd.OutOrStdout(), r)
			return nil
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "one agent: project/agent, or a name alone for that name in every project")
	cmd.Flags().StringVar(&project, "project", "", "one project's agents")
	cmd.Flags().StringVar(&since, "since", "", `how far back to look, like 1h, 90m or 7d, or "all" (default 24h)`)
	cmd.Flags().IntVar(&top, "top", 10, "how many of the slowest tools, calls and commands to list")
	return cmd
}

// renderTiming writes a timing report: the totals, each agent, and the
// slowest tools, tool calls and commands.
func renderTiming(w io.Writer, r api.TimingReport) {
	span := "since the oldest kept"
	if r.Since != nil {
		span = "since " + r.Since.Local().Format("Jan 2 15:04")
	}
	if r.Turns == 0 && r.Commands == 0 {
		_, _ = fmt.Fprintf(w, "Nothing timed %s.\n", span)
		return
	}
	var parts []string
	if r.Turns > 0 {
		parts = append(parts, countOf(r.Turns, "chat turn")+" over "+humanMS(r.TurnMS))
	}
	if r.Commands > 0 {
		parts = append(parts, countOf(r.Commands, "heavy command")+" over "+humanMS(r.CommandMS))
	}
	_, _ = fmt.Fprintf(w, "%s: %s.\n\n", capitalize(span), strings.Join(parts, ", "))

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if r.Turns > 0 {
		other := max(r.TurnMS-r.ModelMS-r.ToolMS-r.StartMS, 0)
		_, _ = fmt.Fprintln(tw, "IN THE TURNS\tTIME\tSHARE\t")
		_, _ = fmt.Fprintf(tw, "Model\t%s\t%s\t%s\n", humanMS(r.ModelMS), shareOf(r.ModelMS, r.TurnMS), modelNote(r.TimingTotals))
		_, _ = fmt.Fprintf(tw, "Tools\t%s\t%s\t%s\n", humanMS(r.ToolMS), shareOf(r.ToolMS, r.TurnMS), countOf(r.ToolCalls, "call"))
		if r.StartMS > 0 {
			_, _ = fmt.Fprintf(tw, "Starting\t%s\t%s\tthe AI tool starting before the prompt reached it\n", humanMS(r.StartMS), shareOf(r.StartMS, r.TurnMS))
		}
		_, _ = fmt.Fprintf(tw, "Other\t%s\t%s\tpermissions, the adapter, and what nothing timed\n", humanMS(other), shareOf(other, r.TurnMS))
	}
	if r.Commands > 0 {
		note := countOf(r.Commands, "run")
		if r.FailedCommands > 0 {
			note += fmt.Sprintf(", %d failed", r.FailedCommands)
		}
		_, _ = fmt.Fprintf(tw, "Heavy commands\t%s\t\t%s; inside tool calls or a terminal\n", humanMS(r.CommandMS), note)
	}
	_ = tw.Flush()

	if len(r.Agents) > 1 || (len(r.Agents) == 1 && r.Agents[0].Commands > 0 && r.Agents[0].Turns > 0) {
		_, _ = fmt.Fprintln(w)
		tw = tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(tw, "AGENT\tTURNS\tWALL\tMODEL\tTOOLS\tCOMMANDS\tLAST")
		for _, a := range r.Agents {
			commands := "-"
			if a.Commands > 0 {
				commands = fmt.Sprintf("%s (%d)", humanMS(a.CommandMS), a.Commands)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n", a.Ref, a.Turns, humanMS(a.TurnMS), humanMS(a.ModelMS),
				humanMS(a.ToolMS), commands, a.LastAt.Local().Format("Jan 2 15:04"))
		}
		_ = tw.Flush()
	}

	if len(r.Tools) > 0 {
		_, _ = fmt.Fprintln(w)
		tw = tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(tw, "SLOWEST TOOLS\tCALLS\tTOTAL\tAVERAGE\tLONGEST\tFAILED")
		for _, t := range r.Tools {
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%d\n", toolLabel(t.Name, t.Subagent), t.Calls, humanMS(t.TotalMS),
				humanMS(t.TotalMS/max(t.Calls, 1)), humanMS(t.MaxMS), t.Failed)
		}
		_ = tw.Flush()
	}

	if len(r.SlowCalls) > 0 {
		_, _ = fmt.Fprintln(w)
		tw = tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(tw, "SLOWEST CALLS\tTOOL\tAGENT\tAT\tCALL")
		for _, c := range r.SlowCalls {
			title := cutLine(c.Title, 70)
			if c.Status != "" && c.Status != "completed" {
				title = strings.TrimSpace(title + " (" + c.Status + ")")
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", humanMS(c.MS), toolLabel(c.Name, c.Subagent), c.Agent, c.At.Local().Format("Jan 2 15:04"), title)
		}
		_ = tw.Flush()
	}

	if len(r.SlowCommands) > 0 {
		_, _ = fmt.Fprintln(w)
		tw = tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(tw, "SLOWEST COMMANDS\tJOBS\tEXIT\tAGENT\tAT\tCOMMAND")
		for _, c := range r.SlowCommands {
			jobs, exit := "-", "?"
			if c.Jobs > 0 {
				jobs = fmt.Sprint(c.Jobs)
			}
			if c.ExitCode >= 0 {
				exit = fmt.Sprint(c.ExitCode)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", humanMS(c.MS), jobs, exit, c.Agent, c.At.Local().Format("Jan 2 15:04"), cutLine(c.Command, 80))
		}
		_ = tw.Flush()
	}
}

// modelNote says how many requests the model answered and how long it took
// to start answering.
func modelNote(t api.TimingTotals) string {
	note := countOf(t.ModelCalls, "request")
	if t.ModelCalls > 0 && t.Answered > 0 {
		note += fmt.Sprintf("; first token in %s a turn, %s a request", humanMS(t.FirstTokenMS/t.Answered), humanMS(t.TTFTMS/t.ModelCalls))
	}
	return note
}

// toolLabel is a tool's name, with the subagent that called it.
func toolLabel(name, subagent string) string {
	if subagent != "" {
		return subagent + ": " + name
	}
	return name
}

// humanMS is a duration in milliseconds as people read one: 350ms, 8.2s,
// 6m12s, 1h02m.
func humanMS(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", ms)
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// shareOf is part as a percentage of whole.
func shareOf(part, whole int64) string {
	if whole <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d%%", (part*100+whole/2)/whole)
}

// countOf is n of what, made plural with an s when n isn't one.
func countOf(n int64, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// cutLine is text on one line, cut at n runes.
func cutLine(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return text
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
