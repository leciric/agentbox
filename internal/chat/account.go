package chat

import (
	"fmt"

	"agentbox/internal/state"
)

// Following a Claude Code account change. The adapter reads its token once,
// when it starts: a lead's from its row (agent.LeadChatCommand), an agent's
// from the environment file its machine logs in with. So an agent or lead
// moved to another account — the project's account changed, its agents moved
// with it, or one agent set by hand — kept running on the old token until
// something happened to restart it, and a chat whose old account was at its
// usage limit stayed stuck there.
//
// AccountChanged restarts the adapter on the new account, resuming the
// session, the way a changed context window (window.go) or changed tools
// (tools.go) do: now when the chat is idle, before its next turn otherwise,
// and at once when a turn is waiting out the old account's usage limit, since
// the new account has no reason to wait.

// AccountChanged is told that an agent, or a project's lead, now runs on the
// Claude Code account a names. Call it once the change is stored, and for an
// agent once its machine's environment has the new token
// (agent.SetClaudeAccount). A chat already on that account, or with no
// adapter running, has nothing to do: the next adapter reads it as it starts.
func (m *Manager) AccountChanged(a state.Agent) {
	if a.AI != "claude" {
		return
	}
	c := m.existing(a.Ref())
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gone {
		return
	}
	c.agent.ClaudeAccount = a.ClaudeAccount
	ad := c.adapter
	if ad == nil || ad.account == a.ClaudeAccount {
		c.accountRestart = false
		return
	}
	if !ad.resumable && ad.ready {
		// Restarting would lose the session: the next one starts on it.
		if !c.accountRestart {
			c.accountRestart = true
			c.add("notice", c.lastTurn()).Text = fmt.Sprintf(
				"This chat moved to the Claude Code account %q. %s keeps the one it started with until the chat starts again, as its adapter can't resume a session.",
				a.ClaudeAccount, ToolNames[c.agent.AI])
			c.flush(true)
		}
		return
	}
	c.accountRestart = true
	tool := ToolNames[c.agent.AI]
	it := c.add("notice", c.lastTurn())
	switch {
	case c.reloadRefused() != nil:
		it.Text = fmt.Sprintf(
			"This chat moved to the Claude Code account %q: %s restarts on it before its next turn and resumes this conversation.",
			a.ClaudeAccount, tool)
	case c.resumeTimer != nil:
		// A turn waiting out the old account's usage limit carries on now,
		// on the new one: the turn restarts the adapter as it begins.
		it.Text = fmt.Sprintf(
			"This chat moved to the Claude Code account %q: %s restarts on it, resumes this conversation and carries on without waiting for the old account's limit.",
			a.ClaudeAccount, tool)
		c.resumeTry = 0
		c.resumeNow()
	default:
		it.Text = fmt.Sprintf(
			"This chat moved to the Claude Code account %q: %s restarts on it and resumes this conversation.",
			a.ClaudeAccount, tool)
		c.clearLimit()
		c.stopAdapter()
		c.accountRestart = false
		c.startAdapter()
	}
	c.markSession()
	c.flush(true)
}

// accountRestartable says the adapter can be restarted on the new account
// before a turn begins: it can resume, and nothing a restart would end is
// running beside the turn. Caller holds c.mu.
func (c *conversation) accountRestartable() bool {
	ad := c.adapter
	return c.accountRestart && ad != nil && ad.ready && ad.resumable && !c.rolling && c.compaction == nil && len(c.session.Background) == 0
}

// restartAccountBeforeTurn restarts the adapter before a turn begins, when the
// agent moved to another account since it started. Caller holds c.mu.
func (c *conversation) restartAccountBeforeTurn() {
	if !c.accountRestartable() {
		return
	}
	c.stopAdapter()
	c.accountRestart = false
}

// resumeNow carries a limited turn on now rather than when its wait ends.
// Caller holds c.mu, with a resume pending and no turn running.
func (c *conversation) resumeNow() {
	c.cancelResume()
	it := c.add("user", "")
	it.Turn, it.Text = it.ID, resumeNudge
	c.beginTurn(it, resumeNudge, nil)
}
