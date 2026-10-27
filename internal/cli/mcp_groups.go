package cli

import (
	"fmt"
	"strings"
	"time"

	"agentbox/internal/api"
)

// variantSettings are what one agent of a group chooses for itself: its tool,
// model, effort, window and account. Permissions aren't among them: a group's
// agents work unattended, as create_agent's default. The descriptions point
// at create_agent's rather than repeat them, and the effort keeps its menu.
func variantSettings(settings map[string]any, ready readyTools) map[string]any {
	tools := []string{"claude", "codex"}
	if ready.openCode {
		tools = append(tools, "opencode")
	}
	if ready.cursor {
		tools = append(tools, "cursor")
	}
	effort := str("as create_agent's effort")
	if e, ok := settings["effort"].(map[string]any); ok && e["enum"] != nil {
		effort["enum"] = e["enum"]
	}
	out := map[string]any{
		"ai": choiceOf("your own tool by default, as create_agent; codex runs on the model its login has, so give it no model or effort, when this "+
			"machine is signed in to Codex", tools...),
		"model":          str("as create_agent's model, for this agent's tool"),
		"effort":         effort,
		"context_window": str("as create_agent's context_window"),
		"claude_account": str("as create_agent's claude_account"),
		"codex_account":  str("for codex: which Codex account (list_resources accounts)"),
	}
	if ready.cursor {
		out["cursor_account"] = str("as create_agent's cursor_account")
	}
	if ready.openCode {
		out["opencode_accounts"] = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
			"description": "as create_agent's opencode_accounts"}
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
