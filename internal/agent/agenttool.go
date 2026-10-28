package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"agentbox/internal/state"
)

// A new agent that names no AI tool runs the one its project's lead runs, on
// the account the lead runs it on: a user who moves the lead to Cursor
// because their Claude quota ran out moves its agents with it. What decides
// is the lead's composer (LeadChoice), its pending switch first, since that is
// the user's latest word. A tool the agents can't run here — not built into
// the base image, or with no login for agents — gives Claude Code instead,
// with a sentence saying why, rather than an agent that can only fail.

// ToolDefault is what a new agent that names no tool runs.
type ToolDefault struct {
	// AI is the tool, and Logins the accounts the lead runs it on that the
	// agent takes ("" for the project's own).
	AI     string
	Logins Logins
	// Fallback says why the agent runs Claude Code rather than the lead's
	// tool; "" when it doesn't.
	Fallback string
}

// LeadToolDefault is the tool and accounts a new agent of p gets when it
// names none: its lead's. A project whose chat never chose a tool, or has no
// chat, gets Claude Code, as it always has.
func (m *Manager) LeadToolDefault(ctx context.Context, p state.Project) ToolDefault {
	choice, err := m.LeadChoice(ctx, p.Name)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			m.logf("Reading the %s chat's tool for a new agent: %v; it runs Claude Code", p.Name, err)
		}
		return ToolDefault{AI: "claude"}
	}
	tool := cmp.Or(choice.NextTool, choice.Tool)
	want := leadAgentLogins(tool, choice)
	err = m.agentsCanRun(ctx, p, tool, want)
	if err != nil && want.Claude+want.Codex+want.Cursor != "" {
		// The lead's own account for it is gone, or no longer allowed: the
		// project's, as for the lead itself (leadLogins).
		want = Logins{}
		err = m.agentsCanRun(ctx, p, tool, want)
	}
	if err != nil {
		return ToolDefault{AI: "claude", Fallback: fmt.Sprintf("This agent runs Claude Code, not %s like you: %v.", ChatAdapters[tool].Tool, err)}
	}
	return ToolDefault{AI: tool, Logins: want}
}

// leadAgentLogins is the account the lead's composer chose for tool, as an
// agent's Logins: Claude Code's, Codex's or Cursor's. OpenCode's are always
// the project's, for the lead too.
func leadAgentLogins(tool string, choice LeadChoice) Logins {
	switch tool {
	case "claude":
		return Logins{Claude: choice.Account}
	case "codex":
		return Logins{Codex: choice.ToolAccounts["codex"]}
	case "cursor":
		return Logins{Cursor: choice.ToolAccounts["cursor"]}
	}
	return Logins{}
}

// agentsCanRun says why an agent of p can't run tool on want's accounts: nil
// when it can. Codex and OpenCode must be in the base image too; Cursor's SDK
// is in every one (CursorReady).
func (m *Manager) agentsCanRun(ctx context.Context, p state.Project, tool string, want Logins) error {
	if tool == "claude" && want.Claude == "" {
		// Claude Code is every agent's tool: a missing login is the same
		// failure it has always been, reported by Create as before.
		return nil
	}
	if flag := map[string]string{"codex": state.SettingImageCodex, "opencode": state.SettingImageOpenCode}[tool]; flag != "" {
		on, err := m.Store.Flag(ctx, flag)
		if err != nil {
			return err
		}
		if !on {
			return fmt.Errorf("the base image has no %s (agentbox image build --%s)", ChatAdapters[tool].Tool, tool)
		}
	}
	_, err := m.CheckLogin(tool, p, want)
	return err
}

// claudeAliases are the model names Claude Code resolves itself, which no
// other tool uses: Cursor's and OpenCode's ids name their vendor.
var claudeAliases = []string{"opus", "sonnet", "haiku", "fable", "best", "opusplan"}

// NamesClaudeModel reports whether model is one of Claude Code's own names:
// an alias it resolves, or a model on the menu it last offered. "default"
// isn't one, since Cursor's Auto is called that too. A lead that names one
// with no tool meant Claude Code, whatever it runs itself: its brief and
// Settings → Models speak in these names.
func (m *Manager) NamesClaudeModel(ctx context.Context, model string) bool {
	name, _ := state.SplitClaudeModel(strings.ToLower(strings.TrimSpace(model)))
	if name == "" || name == "default" {
		return false
	}
	if slices.Contains(claudeAliases, name) {
		return true
	}
	raw, err := m.Store.Setting(ctx, state.SettingClaudeModelChoices)
	if err != nil {
		return false
	}
	for _, offered := range state.ChoiceValues(raw) {
		if base, _ := state.SplitClaudeModel(strings.ToLower(offered)); base == name {
			return true
		}
	}
	return false
}
