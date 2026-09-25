package chat

import (
	"strings"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// A subagent is shown as what it is (D86): a card in the conversation, with
// what it did nested under it.
//
// The chat tells claude-agent-acp it can show subagents as sessions of their
// own (acp.ClientCapabilities.Subagents). The adapter then announces each one
// on the session that started it (subagent_spawned, with a name and the task
// it was given), sends everything the subagent does under the subagent's own
// session id, and says how it ended (subagent_state_update). Without it, a
// subagent's tool calls arrived flattened into the agent's own, its words
// never arrived at all, and nothing said where one subagent ended and the
// agent's own work began.
//
// A card is an item of kind "subagent"; the subagent's messages, thoughts and
// tool calls are ordinary items whose Parent is the card, stored and published
// like every other item. A subagent can outlive the turn that started it — the
// Agent tool can run one in the background — so what it does is recorded
// whether or not a turn is running, against the turn that started it.

// subagent is one subagent session of an adapter.
type subagent struct {
	card *api.ChatItem
	// Its own streamed text, and its own tool calls: it streams alongside the
	// agent, and can go on after the turn whose tool calls are cleared.
	open        *api.ChatItem
	openMessage string
	tools       map[string]*api.ChatItem
}

// Subagent states a card can be in. The adapter's own are completed, failed,
// disconnected and cancelled; these two are AgentBox's.
const (
	subagentRunning = "running"
	subagentStopped = "stopped" // its session ended before it said how it did
)

// spawnSubagent puts a card in the conversation for a subagent the adapter
// announced. parent is the subagent that started it, nil for the agent
// itself. An announcement repeated for a subagent that already has a card
// renames it. The conversation is locked.
func (c *conversation) spawnSubagent(ad *adapter, parent *subagent, u acp.SessionUpdate) {
	id := u.SubagentSessionID
	if id == "" {
		return
	}
	if sa := ad.subagents[id]; sa != nil {
		sa.card.Subagent.Name, sa.card.Subagent.Task = cmp(u.Name, sa.card.Subagent.Name), cmp(u.Task, sa.card.Subagent.Task)
		c.touch(sa.card)
		return
	}
	turn := c.lastTurn()
	var card *api.ChatItem
	if parent == nil {
		// The agent's own text stops where the subagent starts, so what it
		// says after the subagent answers reads after the card.
		c.closeOpen()
		card = c.add("subagent", turn)
	} else {
		parent.closeOpen(c)
		card = c.add("subagent", parent.card.Turn)
		card.Parent = parent.card.ID
	}
	card.Subagent = &api.ChatSubagent{Name: cmp(u.Name, "Subagent"), Task: u.Task, State: subagentRunning}
	if ad.subagents == nil {
		ad.subagents = map[string]*subagent{}
	}
	ad.subagents[id] = &subagent{card: card, tools: map[string]*api.ChatItem{}}
}

// endSubagent records how a subagent ended.
func (c *conversation) endSubagent(ad *adapter, u acp.SessionUpdate) {
	sa := ad.subagents[u.SubagentSessionID]
	if sa == nil || u.State == "" {
		return
	}
	sa.closeOpen(c)
	c.stopTools(sa.tools)
	sa.card.Subagent.State = u.State
	c.touch(sa.card)
}

// subagentUpdate takes an update sent under a subagent's own session.
func (c *conversation) subagentUpdate(ad *adapter, sa *subagent, u acp.SessionUpdate) {
	switch u.SessionUpdate {
	case "agent_message_chunk":
		sa.appendText(c, "assistant", u.MessageID, u.Text())
	case "agent_thought_chunk":
		sa.appendText(c, "thought", u.MessageID, u.Text())
	case "tool_call", "tool_call_update":
		sa.updateTool(c, u)
	case "subagent_spawned":
		c.spawnSubagent(ad, sa, u)
	case "subagent_state_update":
		c.endSubagent(ad, u)
	}
}

// appendText is the conversation's appendText, for the subagent's own
// stream: its text goes in items nested under its card.
func (sa *subagent) appendText(c *conversation, kind, messageID, text string) {
	if text == "" {
		return
	}
	it := sa.open
	if it != nil && (it.Kind != kind || (messageID != "" && sa.openMessage != "" && messageID != sa.openMessage)) {
		sa.closeOpen(c)
		it = nil
	}
	if it == nil {
		if strings.TrimSpace(text) == "" {
			return
		}
		it = c.add(kind, sa.card.Turn)
		it.Parent = sa.card.ID
		it.Text, it.Streaming = text, true
		sa.open, sa.openMessage = it, messageID
		return
	}
	if kind == "thought" && len(it.Text) >= maxThought {
		return
	}
	it.Text += text
	it.UpdatedAt = time.Now()
	c.unsaved[it.ID] = true
	if !c.dirty[it.ID] {
		c.appends = append(c.appends, api.ChatAppend{ID: it.ID, Text: text})
	}
	c.schedule(flushDelay)
}

func (sa *subagent) closeOpen(c *conversation) {
	if sa.open == nil {
		return
	}
	sa.open.Streaming = false
	c.touch(sa.open)
	sa.open, sa.openMessage = nil, ""
}

// updateTool is the conversation's updateTool, for a tool call the subagent
// made.
func (sa *subagent) updateTool(c *conversation, u acp.SessionUpdate) {
	if u.ToolCallID == "" {
		return
	}
	it := sa.tools[u.ToolCallID]
	if it == nil {
		sa.closeOpen(c)
		it = c.add("tool", sa.card.Turn)
		it.Parent = sa.card.ID
		it.Tool = &api.ChatTool{CallID: u.ToolCallID, Kind: "other", Status: "pending"}
		sa.tools[u.ToolCallID] = it
	}
	applyTool(it.Tool, u)
	c.touch(it)
}

// stopTools marks tool calls that never finished as stopped.
func (c *conversation) stopTools(tools map[string]*api.ChatItem) {
	for _, it := range tools {
		if it.Tool.Status == "pending" || it.Tool.Status == "in_progress" {
			it.Tool.Status = "stopped"
			c.touch(it)
		}
	}
}

// stopSubagents ends every subagent an adapter still had running: its process
// is going, and nothing more will be heard from them.
func (c *conversation) stopSubagents(ad *adapter) {
	for _, sa := range ad.subagents {
		sa.closeOpen(c)
		c.stopTools(sa.tools)
		if sa.card.Subagent.State == subagentRunning {
			sa.card.Subagent.State = subagentStopped
			c.touch(sa.card)
		}
	}
	ad.subagents = nil
}
