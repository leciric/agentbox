package agent

import (
	"context"
	"fmt"
	"strings"

	"agentbox/internal/state"
)

// What the lead may choose for the agents it creates, from Settings → Agents.
// The model and the context window chosen there — or the model the project
// names for its agents, which is the user's too — are either the only ones
// the lead may use (EnforceAgentDefaults) or its ceiling: a cheaper model or a
// shorter window for an easy task, never a dearer one. The app's dialog and
// the command line aren't held to either: there the user is choosing.

// LeadAgentDefaults are the model and window a Claude Code agent of project p
// starts on when nobody chooses, and whether Settings enforces them on the
// lead. window is in tokens.
func (m *Manager) LeadAgentDefaults(ctx context.Context, p state.Project) (model string, window int64, enforced bool, err error) {
	if model, err = m.chosenSetting(ctx, nil, p.DirectAgentModel(), state.SettingDefaultClaudeModel, state.DefaultClaudeModel); err != nil {
		return "", 0, false, err
	}
	w, err := m.Store.ClaudeWindows(ctx)
	if err != nil {
		return "", 0, false, err
	}
	installation, err := m.Store.ClaudeCompactWindow(ctx)
	if err != nil {
		return "", 0, false, err
	}
	model = w.NormalizeClaudeModel(model)
	def, err := m.Store.Setting(ctx, state.SettingDefaultAgentContextWindow)
	if err != nil {
		return "", 0, false, err
	}
	window = agentWindow(w, model, def, installation)
	enforced, err = m.Store.Flag(ctx, state.SettingEnforceAgentDefaults)
	return model, window, enforced, err
}

// agentWindow is the window, in tokens, an agent on model gets for chosen:
// what claudeChatDefaults stores and CompactWindow launches, with 0 (the
// installation's own "the model's whole window") read as that whole window.
func agentWindow(w state.ClaudeWindows, model, chosen string, installation int64) int64 {
	if n := w.CompactWindow(model, chosen, installation); n > 0 {
		return n
	}
	return w.Window(model)
}

// modelTier ranks Claude Code's models by what they cost, cheapest first:
// Haiku, Sonnet, Opus, Fable. 0 is a name it can't rank, like "default".
func modelTier(model string) int {
	m, _ := state.SplitClaudeModel(strings.ToLower(strings.TrimSpace(model)))
	switch {
	case strings.Contains(m, "haiku"):
		return 1
	case strings.Contains(m, "sonnet"):
		return 2
	case strings.Contains(m, "opus"), m == "best":
		return 3
	case strings.Contains(m, "fable"):
		return 4
	}
	return 0
}

// CheckLeadChoice checks the model and context window the lead asked
// create_agent for — nil for one it left out — against Settings → Agents, and
// says what to do instead when they break it. Only a Claude Code agent is
// checked: the others have no such settings.
func (m *Manager) CheckLeadChoice(ctx context.Context, p state.Project, ai string, model, window *string) error {
	if ai != "" && ai != "claude" || (model == nil && window == nil) {
		return nil
	}
	defModel, defWindow, enforced, err := m.LeadAgentDefaults(ctx, p)
	if err != nil {
		return err
	}
	w, err := m.Store.ClaudeWindows(ctx)
	if err != nil {
		return err
	}
	runsOn := defModel
	if model != nil {
		runsOn = w.NormalizeClaudeModel(strings.TrimSpace(*model))
	}
	var asked int64
	if window != nil {
		n, err := state.ParseContextWindow(*window)
		if err != nil {
			return err
		}
		asked = n
		if asked >= w.Window(runsOn) {
			asked = w.Window(runsOn)
		}
	}
	if enforced {
		if model != nil && runsOn != defModel {
			return fmt.Errorf("the user enforces %s at %s in Settings → Agents for every agent you create, so %s isn't allowed: "+
				"leave model and context_window out", defModel, state.FormatContextWindow(defWindow), runsOn)
		}
		if window != nil && asked != defWindow {
			return fmt.Errorf("the user enforces %s at %s in Settings → Agents for every agent you create, so a %s window isn't allowed: "+
				"leave model and context_window out", defModel, state.FormatContextWindow(defWindow), state.FormatContextWindow(asked))
		}
		return nil
	}
	if model != nil {
		if have, ceiling := modelTier(runsOn), modelTier(defModel); have > 0 && ceiling > 0 && have > ceiling {
			return fmt.Errorf("%s is above %s, the model chosen in Settings → Agents, which is the most you may use: "+
				"choose %s or a cheaper model, or leave model out", runsOn, defModel, defModel)
		}
	}
	if window != nil && asked > defWindow {
		return fmt.Errorf("a %s window is above %s, the context window chosen in Settings → Agents, which is the most you may use: "+
			"choose %s or less, or leave context_window out", state.FormatContextWindow(asked), state.FormatContextWindow(defWindow), state.FormatContextWindow(defWindow))
	}
	return nil
}
