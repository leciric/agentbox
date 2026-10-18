package cli

import (
	"fmt"
	"strings"
	"time"

	"agentbox/internal/api"
)

// variantSettings are create_agent's settings that one agent of a group can
// choose for itself: its tool, model, effort and window. Permissions aren't
// among them: a group's agents work unattended, as create_agent's default.
func variantSettings(settings map[string]any) map[string]any {
	out := map[string]any{}
	for _, name := range []string{"ai", "model", "effort", "context_window"} {
		if schema, ok := settings[name]; ok {
			out[name] = schema
		}
	}
	return out
}

// runsOn is what a member runs, like "claude opus at high effort".
func runsOn(m api.GroupMember) string {
	what := m.AI
	if m.Model != "" {
		what += " " + m.Model
	}
	if m.Effort != "" {
		what += " at " + m.Effort + " effort"
	}
	return what
}

// describeGroup is a group in a few lines: what it is, where it stands, and
// each agent.
func describeGroup(g api.Group, prefix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s %q, %d agents, %d finished", prefix, g.ID, g.Title, len(g.Members), g.Finished)
	switch {
	case g.Kept != "":
		fmt.Fprintf(&b, "; the user kept %s", g.Kept)
	case g.SettledAt != nil:
		b.WriteString("; closed with none kept")
	}
	if g.Recommended != "" {
		fmt.Fprintf(&b, "; you recommended %s", g.Recommended)
	}
	b.WriteString(".")
	for _, m := range g.Members {
		who := m.Agent
		switch {
		case m.Error != "":
			who = "couldn't be made: " + m.Error
		case who == "":
			who = "being made (job " + m.Job + ")"
		}
		fmt.Fprintf(&b, "\n  %s: %s, on %s", m.Label, who, runsOn(m))
	}
	return b.String()
}

// compareText is compare_group's answer: each agent side by side, as the
// Compare view has them.
func compareText(g api.Group) string {
	var b strings.Builder
	b.WriteString(describeGroup(g, "Group "))
	b.WriteString("\n")
	for _, m := range g.Members {
		fmt.Fprintf(&b, "\n%s — %s", m.Label, runsOn(m))
		if m.Agent == "" {
			if m.Error != "" {
				fmt.Fprintf(&b, ": couldn't be made: %s\n", m.Error)
			} else {
				b.WriteString(": still being made\n")
			}
			continue
		}
		fmt.Fprintf(&b, ": %s (%s)", m.Agent, m.State)
		if m.Busy {
			b.WriteString(", working")
		}
		b.WriteString("\n")
		if m.FinishedAt != nil && m.StartedAt != nil {
			fmt.Fprintf(&b, "  took %s\n", m.FinishedAt.Sub(*m.StartedAt).Round(time.Second))
		}
		if c := m.Changes; c != nil {
			fmt.Fprintf(&b, "  changed %d files, +%d −%d", c.Files, c.Insertions, c.Deletions)
			if c.Dirty {
				b.WriteString(", with uncommitted work")
			}
			b.WriteString("\n")
		}
		if m.PR != nil {
			fmt.Fprintf(&b, "  pull request #%d, %s", m.PR.Number, m.PR.State)
			if m.PR.Checks != "" {
				fmt.Fprintf(&b, ", checks %s", m.PR.Checks)
			}
			b.WriteString("\n")
		}
		if m.Tokens > 0 {
			fmt.Fprintf(&b, "  %d tokens, $%.2f\n", m.Tokens, m.CostUSD)
		}
		if m.Media > 0 {
			fmt.Fprintf(&b, "  kept %d media items\n", m.Media)
		}
		if m.ReportStatus != "" {
			fmt.Fprintf(&b, "  reported %s: %s\n", m.ReportStatus, headLine(m.ReportSummary, 600))
		}
	}
	if g.Recommendation != "" {
		fmt.Fprintf(&b, "\nYou recommended %s: %s\n", g.Recommended, g.Recommendation)
	}
	return strings.TrimRight(b.String(), "\n")
}

// headLine is text cut to about n bytes at a space.
func headLine(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= n {
		return text
	}
	cut := text[:n]
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}
