package chat

import (
	"context"
	"fmt"
	"slices"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The context window, a Claude Code chat's own choice beside its model and
// effort (D91). Claude Code has no ACP option for it: what it moves is
// autoCompactWindow in settings.json, which is read once, when the adapter
// starts. So it is AgentBox's option, added to the adapter's own, and a change
// to it restarts the adapter before the next turn, resuming the same session —
// the conversation carries on, on the new window.

// windows reads what the store knows about Claude model windows, and the
// installation's compact window. Caller holds c.mu.
func (c *conversation) windows() (state.ClaudeWindows, int64) {
	ctx := context.Background()
	w, err := c.m.Store.ClaudeWindows(ctx)
	if err != nil {
		c.m.logf("chat %s: reading the model windows: %v", c.agent.Ref(), err)
	}
	installation, err := c.m.Store.ClaudeCompactWindow(ctx)
	if err != nil {
		installation = state.DefaultClaudeCompactWindow
	}
	return w, installation
}

// windowModel is the model the context window is chosen for: the session's,
// once it has one, else the stored one. Caller holds c.mu.
func (c *conversation) windowModel(w state.ClaudeWindows) string {
	model := optionValueOf(c.session.Options, "model")
	if model == "" {
		model = w.NormalizeClaudeModel(c.stored.Options["model"])
	}
	if model == "" {
		model = "default"
	}
	return model
}

// compactWindow is the compact window this chat should be running with now.
// Caller holds c.mu.
func (c *conversation) compactWindow() int64 {
	w, installation := c.windows()
	if c.agent.AI != "claude" {
		return installation
	}
	return w.CompactWindow(c.windowModel(w), c.stored.Options[state.ChatOptionContextWindow], installation)
}

// launchSettings are the model and compact window an adapter starts with.
// A stored "opus[1m]" starts as "opus" (NormalizeClaudeModel), and a model
// whose own window is short starts as its "[1m]" variant when the long window
// was chosen. Caller holds c.mu.
func (c *conversation) launchSettings() (model string, window int64) {
	model = c.stored.Options["model"]
	if c.agent.AI != "claude" {
		_, installation := c.windows()
		return model, installation
	}
	w, _ := c.windows()
	window = c.compactWindow()
	return w.Model(w.NormalizeClaudeModel(model), window), window
}

// refreshWindowOption puts the context window among the session's options, or
// takes it out when the model has only one. It needs no live session: the
// window is AgentBox's option, chosen for the stored model when the adapter
// hasn't reported one — before the chat has ever started, and while it
// connects or restarts — and stored, so the adapter that starts next is
// launched on it. Caller holds c.mu.
func (c *conversation) refreshWindowOption() {
	c.session.Options = slices.DeleteFunc(c.session.Options, func(o api.ChatOption) bool { return o.ID == state.ChatOptionContextWindow })
	if c.agent.AI != "claude" {
		return
	}
	w, installation := c.windows()
	option, ok := state.ContextWindowOption(w.ContextWindows(c.windowModel(w), installation), c.stored.Options[state.ChatOptionContextWindow])
	if !ok {
		return
	}
	// Beside the model, where the composer looks for it.
	i := slices.IndexFunc(c.session.Options, func(o api.ChatOption) bool { return o.ID == "model" }) + 1
	c.session.Options = slices.Insert(c.session.Options, i, option)
}

// setContextWindow is SetOption for the context window. Caller holds c.mu, and
// this unlocks it.
func (m *Manager) setContextWindow(c *conversation, value string) (api.ChatSession, error) {
	defer c.mu.Unlock()
	w, installation := c.windows()
	model := c.windowModel(w)
	stored, err := w.ContextWindowChoice(model, value, installation)
	if err != nil {
		return api.ChatSession{}, err
	}
	c.stored.Options[state.ChatOptionContextWindow] = stored
	if err := m.Store.SaveChat(context.Background(), c.agent.Project, c.agent.Name, c.stored); err != nil {
		return api.ChatSession{}, err
	}
	c.refreshWindowOption()
	c.restartIfWindowMoved()
	c.markSession()
	c.flush(false)
	return clone(c.session), nil
}

// restartIfWindowMoved notes that the running adapter was started on another
// compact window than the chat now wants, so the next turn restarts it, and
// says so. Caller holds c.mu.
func (c *conversation) restartIfWindowMoved() {
	ad := c.adapter
	if ad == nil || !ad.ready {
		return // the adapter that starts next reads the new window anyway
	}
	want := c.compactWindow()
	if want == ad.window {
		if c.windowRestart {
			c.windowRestart = false
			c.add("notice", c.lastTurn()).Text = "Back on the window this session started with: nothing to restart."
		}
		return
	}
	if c.windowRestart {
		return
	}
	c.windowRestart = true
	name := "the model's whole window"
	if want > 0 {
		name = state.FormatContextWindow(want)
	}
	c.add("notice", c.lastTurn()).Text = fmt.Sprintf(
		"The %s context window applies from your next message: %s restarts then and resumes this conversation, since it reads the window only when it starts.",
		name, ToolNames[c.agent.AI])
}

// contextSize is the window a usage_update reading is measured against: the
// model's, or the compact window the adapter started with when that is
// smaller, since that is where the chat really compacts. Caller holds c.mu.
func (c *conversation) contextSize(ad *adapter, size int64) int64 {
	if c.agent.AI == "claude" && ad.window > 0 && ad.window < size {
		return ad.window
	}
	return size
}
