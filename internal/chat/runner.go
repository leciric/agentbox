package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Switching a lead's AI tool and account from its composer.
//
// A choice applies from the next message, never to a turn already running,
// and how depends on what changed:
//
//   - The model, within one tool, is an option like any other (SetOption):
//     set_config_option on the session there is, which carries on.
//   - The Claude Code account, within Claude Code, is the daemon's to store
//     (the agent's row, or the Home chat's), and AccountChanged restarts the
//     adapter on it, resuming the same session: Claude Code keeps the
//     conversation in the lead's HOME, whichever account logs in, so nothing
//     is lost. A session that can't be found under the new one starts afresh
//     and is told the conversation (connect's handoff).
//   - The tool can't resume another tool's session, so the session is rolled
//     over (stored.SessionID cleared, D73). When the old one is running and
//     idle, the daemon first asks it for the rollover summary that goes into
//     the project's memory and the next brief (BeginSwitch); otherwise, or
//     when that fails, the new session's first prompt carries the
//     conversation itself (Handoff), as after a rollback.
//
// The choice is the chat's (state.Chat), not the project's: it survives a
// restart, the Home chat has one too, and the project's settings stay what a
// reset chat and new agents start from.

// SetRunner stores the AI tool and account a lead's composer chose: a Claude
// Code, Codex or Cursor account, "" for the project's; OpenCode takes none. Choosing
// the tool in effect calls off a pending switch; a chat with no session to
// roll switches at once. The caller has checked that the tool is signed in and
// the account allowed, and moves the account itself (AccountChanged).
func (m *Manager) SetRunner(a state.Agent, tool, account string) (api.ChatSession, error) {
	if !a.IsLead() {
		return api.ChatSession{}, fmt.Errorf("%s runs the AI tool it was created with: only a project's chat or Home switches", a.Ref())
	}
	if _, ok := ToolNames[tool]; !ok {
		return api.ChatSession{}, fmt.Errorf("unknown AI tool %q", tool)
	}
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatSession{}, err
	}
	defer c.mu.Unlock()
	switch tool {
	case "claude":
		c.stored.Account = account
	case "codex", "cursor":
		chosen := c.stored.ToolAccounts.Clean()
		chosen[tool] = account
		c.stored.ToolAccounts = chosen.Clean()
	}
	switch {
	case tool == c.agent.AI:
		c.stored.NextTool = ""
	case c.stored.SessionID == "" && c.adapter == nil && !c.rolling:
		// Nothing to roll: the next message starts the new tool anyway.
		c.applyTool(tool)
	default:
		c.stored.NextTool = tool
	}
	if err := c.m.Store.SaveChat(context.Background(), c.agent.Project, c.agent.Name, c.stored); err != nil {
		return api.ChatSession{}, err
	}
	c.syncRunner()
	c.markSession()
	c.flush(true)
	return c.view(), nil
}

// Session is the state of a chat's session, as the conversation holds it.
func (m *Manager) Session(a state.Agent) (api.ChatSession, error) {
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatSession{}, err
	}
	defer c.mu.Unlock()
	return c.view(), nil
}

// PendingTool is the tool a lead's chat switches to at its next message, or
// "" when it stays on the one it runs.
func (m *Manager) PendingTool(a state.Agent) string {
	if !a.IsLead() {
		return ""
	}
	c, err := m.conversation(a)
	if err != nil {
		return ""
	}
	defer c.mu.Unlock()
	return c.stored.NextTool
}

// BeginSwitch is BeginCompact for a pending tool switch: the session that is
// about to go is asked for its summary (ask), settle writes it into the
// project's memory and the brief, and the fresh session that follows runs the
// chosen tool. When the summary didn't come, the new tool is told the
// conversation instead. ErrBusy and errNoSession leave the switch to the next
// turn's start (switchToolBeforeTurn).
func (m *Manager) BeginSwitch(ctx context.Context, a state.Agent, ask string, settle func(answer string, askErr error) error) (finish func() error, err error) {
	if m.PendingTool(a) == "" {
		return nil, errNoSwitch
	}
	return m.BeginCompact(ctx, a, ask, func(answer string, askErr error) error {
		c := m.existing(a.Ref())
		if c != nil {
			c.mu.Lock()
			c.switching = true
			c.mu.Unlock()
		}
		return settle(answer, askErr)
	})
}

// errNoSwitch says a chat has no tool switch pending.
var errNoSwitch = errors.New("no AI tool switch is pending")

// IsNoSession reports whether err says a chat had no running session to
// compact or switch: not a failure, as its next turn starts one anyway.
func IsNoSession(err error) bool { return errors.Is(err, errNoSession) || errors.Is(err, errNoSwitch) }

// switchRolled finishes a switch whose session has just rolled over (see
// rollover): the chosen tool takes over, told the conversation when its
// summary couldn't be saved. The conversation is locked.
func (c *conversation) switchRolled(unsaved error) {
	if !c.switching {
		return
	}
	c.switching = false
	tool := c.stored.NextTool
	if tool == "" {
		return
	}
	from := ToolNames[c.agent.AI]
	if unsaved != nil {
		c.stored.Handoff = Handoff(c.items, switchIntro(from, ToolNames[tool]))
	}
	c.applyTool(tool)
	c.add("notice", c.lastTurn()).Text = switchNotice(from, ToolNames[tool], unsaved == nil)
	c.saveStored()
}

// switchToolBeforeTurn switches to a pending tool as a turn begins, when the
// summary path couldn't (the chat was off, busy, or the Home chat, which has
// no project memory): the old session ends, and the new tool's first prompt
// carries the conversation. The conversation is locked, and the turn is set.
func (c *conversation) switchToolBeforeTurn() {
	tool := c.stored.NextTool
	if tool == "" || c.rolling || tool == c.agent.AI {
		return
	}
	from := ToolNames[c.agent.AI]
	c.stopAdapter()
	c.stored.SessionID = ""
	c.stored.Handoff = c.handoffWith(switchIntro(from, ToolNames[tool]))
	c.applyTool(tool)
	c.add("notice", c.turn.id).Text = switchNotice(from, ToolNames[tool], false)
	c.saveStored()
	c.tools, c.plan, c.open, c.openMessage = map[string]*api.ChatItem{}, nil, nil, ""
	c.session.ContextUsed, c.session.ContextSize = 0, 0
}

// handoffWith is the conversation up to the running turn, introduced by intro.
// The conversation is locked.
func (c *conversation) handoffWith(intro string) string {
	items := c.items
	if c.turn != nil {
		for i, it := range items {
			if it.ID == c.turn.id {
				items = items[:i]
				break
			}
		}
	}
	return Handoff(items, intro)
}

func switchIntro(from, to string) string {
	return "The user switched this conversation from " + from + " to " + to + ", so this is a new session. Here is the conversation so far; carry on from it."
}

// switchNotice is what the conversation says where the tool changed.
func switchNotice(from, to string, summarised bool) string {
	if summarised {
		return "Switched from " + from + " to " + to + ": a new session, which starts from the summary " + from + " wrote into project memory."
	}
	return "Switched from " + from + " to " + to + ": a new session, which is told the conversation above."
}

// applyTool makes tool the one the chat runs. The settings chosen for the old
// tool are put aside under its name and the new tool's taken back, since a
// model or a mode means nothing to another tool. The caller has ended the old
// session. The conversation is locked.
func (c *conversation) applyTool(tool string) {
	old := c.agent.AI
	if c.stored.Options == nil {
		c.stored.Options = map[string]string{}
	}
	kept := map[string]string{}
	for k, v := range c.stored.Options {
		if strings.Contains(k, toolOptionSep) {
			kept[k] = v
			continue
		}
		kept[old+toolOptionSep+k] = v
	}
	options := map[string]string{}
	prefix := tool + toolOptionSep
	for k, v := range kept {
		if id, ok := strings.CutPrefix(k, prefix); ok {
			options[id] = v
			continue
		}
		options[k] = v
	}
	c.stored.Options = options
	c.stored.Tool, c.stored.NextTool = tool, ""
	c.agent.AI = tool
	c.agent.ClaudeAccount = ""
	c.agent.CodexAccount, c.agent.OpenCodeAccounts, c.agent.CursorAccount = "", nil, ""
	if tool == "claude" && c.m.LeadAccount != nil {
		account, err := c.m.LeadAccount(c.agent)
		if err != nil {
			c.m.logf("chat %s: the Claude Code account to switch to: %v", c.agent.Ref(), err)
		}
		c.agent.ClaudeAccount = account
	} else if tool != "claude" && c.m.LeadLogins != nil {
		if on, err := c.m.LeadLogins(c.agent); err != nil {
			c.m.logf("chat %s: the %s account to switch to: %v", c.agent.Ref(), ToolNames[tool], err)
		} else {
			c.agent.CodexAccount, c.agent.OpenCodeAccounts, c.agent.CursorAccount = on.CodexAccount, on.OpenCodeAccounts, on.CursorAccount
		}
	}
	c.session.Tool = tool
	c.session.Options, c.session.Commands = []api.ChatOption{}, []api.ChatCommand{}
	c.session.NoImages, c.session.NoResume = false, false
	c.syncRunner()
	c.markSession()
}

// toolOptionSep joins a tool's name to an option it put aside (applyTool):
// "codex/model". No option id has it.
const toolOptionSep = "/"

// syncRunner puts what the chat runs on, and what it is to switch to, on its
// session. The conversation is locked.
func (c *conversation) syncRunner() {
	if !c.agent.IsLead() {
		return
	}
	c.session.NextTool = c.stored.NextTool
	c.session.Account = runsOn(c.agent)
	if c.agent.AI == "opencode" {
		c.session.Account = strings.ReplaceAll(strings.ReplaceAll(c.session.Account, "=", ": "), ",", ", ")
	}
}

// leadTool is the tool a lead's chat runs: the one it switched to, else the
// one its record says.
func (c *conversation) leadTool(a state.Agent) string {
	if a.IsLead() {
		return cmp(c.stored.Tool, a.AI)
	}
	return a.AI
}

// saveStored stores the chat's session and choices, logging a failure: the
// chat carries on from what it holds. The conversation is locked.
func (c *conversation) saveStored() {
	if err := c.m.Store.SaveChat(context.Background(), c.agent.Project, c.agent.Name, c.stored); err != nil {
		c.m.logf("chat %s: %v", c.agent.Ref(), err)
	}
}
