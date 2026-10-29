package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"agentbox/internal/opencode"
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
	// Model is the model the lead's own session runs, when the agent takes the
	// lead's tool: passing it on keeps an agent off the adapter's default,
	// which may not be enabled for the account. "" when there is none.
	Model string
	// Fallback says why the agent runs Claude Code rather than the lead's
	// tool; "" when it doesn't.
	Fallback string
}

// DefaultTool is the installation's AI tool: the one the app's first run
// chose, or the New agent dialog last created an agent with, both kept as
// state.SettingLastAgentPick, which the dialog opens on; Claude Code when
// neither has said, or said a tool no chat runs ("none", a shell). A chat that
// has never run starts on it (chatTool).
func (m *Manager) DefaultTool(ctx context.Context) string {
	raw, err := m.Store.Setting(ctx, state.SettingLastAgentPick)
	if err != nil {
		m.logf("Reading the default AI tool: %v; it is Claude Code", err)
		return "claude"
	}
	var pick struct {
		AI string `json:"ai"`
	}
	if raw == "" || json.Unmarshal([]byte(raw), &pick) != nil || !slices.Contains(LeadTools, pick.AI) {
		return "claude"
	}
	return pick.AI
}

// chatTool is the tool a chat runs: the one it chose, else the installation's
// default for a chat that has never run, else Claude Code, which a chat that
// ran without choosing ran on. A chat stores the tool it starts on (the chat
// package's load), so a default changed later moves only chats yet to start.
func (m *Manager) chatTool(ctx context.Context, c state.Chat) string {
	switch {
	case c.Tool != "":
		return c.Tool
	case c.SessionID != "":
		return "claude"
	}
	return m.DefaultTool(ctx)
}

// LeadToolDefault is the tool and accounts a new agent of p gets when it
// names none: its lead's. A project whose chat never chose a tool, or has no
// chat, gets the installation's default (DefaultTool).
func (m *Manager) LeadToolDefault(ctx context.Context, p state.Project, onCluster bool) ToolDefault {
	choice, err := m.LeadChoice(ctx, p.Name)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			m.logf("Reading the %s chat's tool for a new agent: %v; it runs Claude Code", p.Name, err)
		}
		return ToolDefault{AI: "claude"}
	}
	tool := cmp.Or(choice.NextTool, choice.Tool)
	want := leadAgentLogins(tool, choice)
	err = m.agentsCanRun(ctx, p, tool, want, onCluster)
	if err != nil && want.Claude+want.Codex+want.Cursor != "" {
		// The lead's own account for it is gone, or no longer allowed: the
		// project's, as for the lead itself (leadLogins).
		want = Logins{}
		err = m.agentsCanRun(ctx, p, tool, want, onCluster)
	}
	if err != nil {
		return ToolDefault{AI: "claude", Fallback: fmt.Sprintf("This agent runs Claude Code, not %s like you: %v.", ChatAdapters[tool].Tool, err)}
	}
	return ToolDefault{AI: tool, Logins: want, Model: choice.Model}
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
// when it can. On this host Codex and OpenCode must be in the base image too;
// a cluster agent's pod carries its own image, so the runtime check there is
// Create's, not the base image's. Cursor's SDK is in every image (CursorReady).
func (m *Manager) agentsCanRun(ctx context.Context, p state.Project, tool string, want Logins, onCluster bool) error {
	if tool == "claude" && want.Claude == "" {
		// Claude Code is every agent's tool: a missing login is the same
		// failure it has always been, reported by Create as before.
		return nil
	}
	if (tool == "codex" || tool == "opencode") && !onCluster {
		// The component chosen for the base image, not the image itself: a
		// rebuild is already asked for when a component is turned on, and
		// Create's own check is what refuses an agent whose machine lacks the
		// tool (checkImageTool). A cluster agent's machine is a pod from the
		// cluster's image, which already carries the tool or refuses it when
		// the session starts.
		flag := map[string]string{"codex": state.SettingImageCodex, "opencode": state.SettingImageOpenCode}[tool]
		skip := false
		if tool == "opencode" {
			version, err := m.openCodeVersion(ctx)
			if err != nil {
				return err
			}
			// OpenCode 2 is installed by each agent on demand, not by the image.
			skip = version == opencode.V2
		}
		if !skip {
			on, err := m.Store.Flag(ctx, flag)
			if err != nil {
				return err
			}
			if !on {
				return fmt.Errorf("the base image has no %s (agentbox image build --%s)", ChatAdapters[tool].Tool, tool)
			}
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
