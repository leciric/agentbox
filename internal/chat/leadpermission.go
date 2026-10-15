package chat

import (
	"fmt"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// An agent's AI tool asking permission waits in the agent's chat, which the
// user isn't usually looking at: they work through the project's lead. So
// the request shows in the lead's chat too, as a card (ChatAgentPermission)
// the user answers on the agent's own route (Answer), and that the daemon
// keeps in step with the agent's request: PermissionChanged reports every
// change to it, and ShowAgentPermission puts it in the lead's chat.
//
// The lead's model is told about the requests in a line before its next
// prompt (permissionNote), so it can say what one is about. It never
// answers one: only the user's click does.

// permissionChanged reports a change to it, one of this agent's permission
// requests as just published. The conversation is locked.
func (c *conversation) permissionChanged(it *api.ChatItem) {
	if c.m.PermissionChanged == nil || it.Permission == nil || it.Permission.Approval != "" || c.agent.IsLead() {
		return
	}
	p := api.ChatAgentPermission{
		Agent: c.agent.Name, Ref: c.agent.Ref(), Title: c.agent.Title, AI: c.agent.AI,
		Item: it.ID, Permission: *it.Permission,
	}
	if tool := c.toolOf(it.Permission.CallID); tool != nil {
		t := clone(*tool)
		t.Output = ""
		p.Tool = &t
	}
	c.m.PermissionChanged(c.agent, p)
}

// toolOf is the tool call with callID, the latest made, or nil. The
// conversation is locked.
func (c *conversation) toolOf(callID string) *api.ChatTool {
	if callID == "" {
		return nil
	}
	if it := c.tools[callID]; it != nil && it.Tool != nil {
		return it.Tool
	}
	// A subagent's calls aren't in tools, and the turn may have ended.
	for i := len(c.items) - 1; i >= 0; i-- {
		if t := c.items[i].Tool; t != nil && t.CallID == callID {
			return t
		}
	}
	return nil
}

// ShowAgentPermission puts p, one of the project's agents' permission
// requests, in lead's chat: a new card while it waits, or the card it already
// has brought up to date. A request answered before it got here isn't shown
// at all.
func (m *Manager) ShowAgentPermission(lead state.Agent, p api.ChatAgentPermission) error {
	c, err := m.conversation(lead)
	if err != nil {
		return err
	}
	defer c.mu.Unlock()
	var it *api.ChatItem
	for i := len(c.items) - 1; i >= 0; i-- {
		if ap := c.items[i].AgentPermission; ap != nil && ap.Ref == p.Ref && ap.Item == p.Item {
			it = c.items[i]
			break
		}
	}
	if it == nil {
		if p.Permission.Outcome != "" {
			return nil
		}
		it = c.add("notice", c.lastTurn())
	} else if it.AgentPermission.Permission.Outcome != "" {
		return nil // settled for good
	} else {
		c.touch(it)
	}
	it.Text = agentPermissionLine(p)
	it.AgentPermission = &p
	c.flush(true)
	return nil
}

// agentPermissionLine is what a card says in words: what the chat's search
// finds, and the lead's model is told.
func agentPermissionLine(p api.ChatAgentPermission) string {
	who := p.Agent
	if p.Title != "" {
		who += " (" + p.Title + ")"
	}
	what := p.Permission.Title
	if t := p.Tool; t != nil && t.Command != "" && !strings.Contains(what, t.Command) {
		what += ": " + t.Command
	} else if t != nil && len(t.Paths) > 0 && !strings.Contains(what, t.Paths[0]) {
		what += ": " + t.Paths[0]
	}
	return fmt.Sprintf("%s asks permission to %s", who, strings.TrimSpace(what))
}

// outcomeWords says how a request was answered, for the lead's model.
func outcomeWords(p api.ChatAgentPermission) string {
	switch o := p.Permission.Outcome; o {
	case "":
		return "still waiting for the user"
	case "cancelled":
		return "no longer waiting: the agent's turn ended or it was cancelled"
	default:
		for _, opt := range p.Permission.Options {
			if opt.ID == o {
				if strings.HasPrefix(opt.Kind, "allow") {
					return "the user allowed it (" + opt.Name + ")"
				}
				return "the user denied it (" + opt.Name + ")"
			}
		}
		return "answered"
	}
}

// permissionNote is what a lead's model is told before its next prompt about
// the requests its chat showed since it was last told, as they stand now; ""
// for none. The conversation is locked.
func (c *conversation) permissionNote() string {
	if !c.agent.IsLead() {
		return ""
	}
	since := c.permissionsTold
	c.permissionsTold = time.Now()
	var lines []string
	for _, it := range c.items {
		if p := it.AgentPermission; p != nil && it.CreatedAt.After(since) {
			lines = append(lines, "- "+agentPermissionLine(*p)+" — "+outcomeWords(*p)+".")
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "[AgentBox: since your last message, agents asked the user's permission in their chats, and this chat showed each request as a card" +
		" with buttons. Only the user answers them, with those buttons; you can't, so don't say you have or will. If the user asks, say what a request" +
		" is about and what it would do.\n" + strings.Join(lines, "\n") + "]"
}
