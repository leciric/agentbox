package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/trace"
)

func newTraceCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace",
		Short: "Read the daemon's performance trace",
	}
	cmd.AddCommand(newTraceSummaryCmd(a))
	return cmd
}

func newTraceSummaryCmd(a *app) *cobra.Command {
	var since string
	var limit int
	var stalls bool
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Sum up how long the daemon's work took: per span, counts and latency percentiles",
		Long: `Reads the daemon's span files next to state.db directly, so it works while the
daemon is hung or stopped. Each line is one kind of span — an API route, a job, an
Incus or git command, an ACP turn, a SQLite transaction, or one the app sent — with
how many ended in the window, how often per minute, how many failed, and their
p50, p95 and longest durations, busiest (most total time) first. Below it, the
stalls in the window: requests answered after 2s, SQLite waits past 1s on its
lock, and the VM's memory or CPU stalling.

  agentbox trace summary                       # the last 30 minutes
  agentbox trace summary --since 2h --limit 40`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := parseStretch(since)
			if err != nil {
				return err
			}
			dir := filepath.Dir(a.paths.StateDB())
			from := time.Now().Add(-d)
			spans, err := trace.Read(dir, trace.SpansFile, from)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			writeTraceSummary(w, trace.Summarize(spans, 0), limit, d)
			if stalls {
				found, err := trace.Read(dir, trace.StallsFile, from)
				if err != nil {
					return err
				}
				writeStalls(w, found, limit)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "30m", "how far back to look: 30m, 2h, 7d")
	cmd.Flags().IntVar(&limit, "limit", 30, "the most span names (and stalls) to list")
	cmd.Flags().BoolVar(&stalls, "stalls", true, "list the stalls in the window too")
	return cmd
}

func writeTraceSummary(w io.Writer, sum trace.Summary, limit int, window time.Duration) {
	if len(sum.Stats) == 0 {
		_, _ = fmt.Fprintf(w, "No spans ended in the last %s.\n", window)
		return
	}
	minutes := sum.Last.Sub(sum.First).Minutes()
	count := 0
	for _, s := range sum.Stats {
		count += s.Count
	}
	_, _ = fmt.Fprintf(w, "%d spans ended between %s and %s\n\n", count, sum.First.Local().Format("15:04:05"), sum.Last.Local().Format("15:04:05"))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	_, _ = fmt.Fprintln(tw, leftCell("SPAN", 48)+"\tCOUNT\t/MIN\tFAILED\tP50\tP95\tMAX\tTOTAL\t")
	for i, s := range sum.Stats {
		if limit > 0 && i == limit {
			_, _ = fmt.Fprintf(tw, "… %d more\t\t\t\t\t\t\t\t\n", len(sum.Stats)-limit)
			break
		}
		rate := "-"
		if minutes >= 1 {
			rate = strconv.FormatFloat(float64(s.Count)/minutes, 'f', 1, 64)
		}
		failed := "-"
		if s.Errors > 0 {
			failed = strconv.Itoa(s.Errors)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t\n", leftCell(s.Name, 48), s.Count, rate, failed,
			formatMS(s.P50MS), formatMS(s.P95MS), formatMS(s.MaxMS), formatMS(s.TotalMS))
	}
	_ = tw.Flush()
}

func writeStalls(w io.Writer, stalls []trace.Span, limit int) {
	if len(stalls) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\n%d stall(s), newest first:\n", len(stalls))
	for i := len(stalls) - 1; i >= 0; i-- {
		if limit > 0 && len(stalls)-1-i == limit {
			_, _ = fmt.Fprintf(w, "  … %d more\n", i+1)
			break
		}
		s := stalls[i]
		reason, _ := s.Attrs["stall"].(string)
		_, _ = fmt.Fprintf(w, "  %s  %-8s  %s", s.Start.Local().Format("Jan 02 15:04:05"), formatMS(s.DurationMS), s.Name)
		if reason != "" {
			_, _ = fmt.Fprintf(w, " (%s)", reason)
		}
		_, _ = fmt.Fprintln(w)
	}
}

// leftCell pads a name for a right-aligned tabwriter, so the first column
// still reads left to right, cutting one too long to fit.
func leftCell(s string, width int) string {
	if r := []rune(s); len(r) > width {
		s = string(r[:width-1]) + "…"
	}
	return s + strings.Repeat(" ", max(0, width-len([]rune(s))))
}

// formatMS is a duration in milliseconds the way a person reads it.
func formatMS(ms float64) string {
	switch {
	case ms < 1:
		return fmt.Sprintf("%.2fms", ms)
	case ms < 1000:
		return fmt.Sprintf("%.0fms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", ms/1000)
	default:
		return (time.Duration(ms) * time.Millisecond).Round(time.Second).String()
	}
}
