package chat

import (
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// The compactions Claude Code runs itself, and a turn that goes silent after
// one.
//
// claude-agent-acp reports a compaction, to a client without ACP's
// compaction updates, as a tool call of its own: "Compact conversation",
// kind think. Most come inside a turn, when the context reaches the compact
// window. But the adapter also compacts an idle session by itself, shortly
// before its one-hour prompt cache runs out (its idle-compaction.ts, since
// 0.9x): it prompts its own session with /compact, outside any session/prompt
// of AgentBox's. That call arrived with no turn running and woke one
// (background.go), and nothing ended it: the result of a /compact carries no
// cost, so its usage_update isn't the one that ends a woken turn. The chat
// read as running, a message sent to it was steered, answered "no running
// turn" and waited behind that turn, which only wokenQuiet took as ended —
// so for ten minutes and more the agent looked wedged right after "Compact
// conversation (completed)". It is a card now, under the last turn, and
// starts nothing.
//
// A turn that does go silent after a compaction, whatever the reason, is
// stopped after compactStuckAfter and told to carry on, rather than wait for
// the stall watch and then for somebody to notice.

const compactionTitle = "Compact conversation"

// compactStuckAfter is how long a turn may go without a word from the model
// after a compaction in it ended, with no tool call or subagent running and
// nobody asked anything, before it is taken to be stuck. Claude Code goes on
// with its next request at once after compacting, on a context that is small
// again: its first words come in seconds.
const compactStuckAfter = 5 * time.Minute

// compactStuckAfter is the package's compactStuckAfter, or the Manager's
// own, shorter one in a test.
func (m *Manager) compactStuckAfter() time.Duration {
	if m.compactStuck > 0 {
		return m.compactStuck
	}
	return compactStuckAfter
}

// compactCarries is how many times in a row a stuck turn is carried on: a
// session that sticks after every compaction is left to the stall watch.
const compactCarries = 2

// carryOnPrompt is what the turn carrying a stuck one on says to the model,
// and carryOnNotice what the chat shows of it.
const (
	carryOnPrompt = "Your turn went silent right after your conversation was compacted, so AgentBox stopped it. Carry on where you left off."
	carryOnNotice = "The turn went silent after the conversation was compacted. AgentBox stopped it and told the agent to carry on."
)

// isCompaction says whether a tool call is claude-agent-acp's report of a
// compaction.
func isCompaction(t *api.ChatTool) bool {
	return t != nil && t.Title == compactionTitle && t.Kind == "think"
}

// ownCompaction takes the updates of a compaction the adapter started with no
// turn running: the tool_call that opens one, and every update to a card it
// opened that way, whenever it comes. It reports whether u was one of those.
// The conversation is locked.
func (c *conversation) ownCompaction(ad *adapter, u acp.SessionUpdate) bool {
	if ad.replaying || u.ToolCallID == "" {
		return false
	}
	switch u.SessionUpdate {
	case "tool_call":
		if c.turn != nil || u.Title == nil || *u.Title != compactionTitle || u.Kind != "think" {
			return false
		}
		it := c.add("tool", c.lastTurn())
		it.Tool = &api.ChatTool{CallID: u.ToolCallID, Kind: "think", Status: "pending"}
		if ad.compactions == nil {
			ad.compactions = map[string]*api.ChatItem{}
		}
		ad.compactions[u.ToolCallID] = it
		c.m.logf("chat %s: its AI tool compacted the idle session by itself", c.agent.Ref())
	case "tool_call_update":
		if ad.compactions[u.ToolCallID] == nil {
			return false
		}
	default:
		return false
	}
	it := ad.compactions[u.ToolCallID]
	applyTool(it.Tool, u)
	c.touch(it)
	c.flush(true)
	return true
}

// afterCompaction keeps the clock of a turn's silence after a compaction:
// started when a compaction in it ends, stopped by anything the model says
// or does. The conversation is locked and t is the running turn.
func (t *turn) afterCompaction(c *conversation, ad *adapter, u acp.SessionUpdate) {
	switch u.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk", "plan":
		t.compactedAt = time.Time{}
	case "tool_call", "tool_call_update":
		it := c.tools[u.ToolCallID]
		if it == nil && u.SessionUpdate == "tool_call" && (u.Title == nil || *u.Title != compactionTitle) {
			t.compactedAt = time.Time{}
			return
		}
		if it != nil && !isCompaction(it.Tool) {
			t.compactedAt = time.Time{}
			return
		}
		if u.Status == "completed" || u.Status == "failed" {
			if it == nil || (it.Tool.Status != "completed" && it.Tool.Status != "failed") {
				t.compactedAt = c.m.now()
				c.watchCompacted(ad, t, c.m.compactStuckAfter())
			}
		}
	}
}

// watchCompacted checks on t after wait, and carries it on if it has said
// nothing since its compaction ended.
func (c *conversation) watchCompacted(ad *adapter, t *turn, wait time.Duration) {
	c.m.after(wait, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.turn != t || c.adapter != ad || t.cancelled || t.compactedAt.IsZero() {
			return
		}
		if left := c.m.compactStuckAfter() - c.m.now().Sub(t.compactedAt); left > 0 {
			// A later compaction moved the clock.
			c.watchCompacted(ad, t, left)
			return
		}
		if len(c.pending) > 0 || runningTool(c.tools) != nil || subagentsRunning(ad) || t.carries >= compactCarries {
			return
		}
		c.m.logf("chat %s: its turn has said nothing for %s since its conversation was compacted; stopping it and carrying it on", c.agent.Ref(), c.m.compactStuckAfter())
		t.carryOn = true
		c.cancelTurn()
	})
}

// subagentsRunning says whether any of ad's subagents hasn't ended.
func subagentsRunning(ad *adapter) bool {
	for _, sa := range ad.subagents {
		if sa.card != nil && sa.card.Subagent != nil && sa.card.Subagent.State == subagentRunning {
			return true
		}
	}
	return false
}

// carryOnCompacted starts the turn that goes on with t, which was stopped for going
// silent after a compaction. Messages that waited on t are given to it
// instead. The conversation is locked and no turn runs.
func (c *conversation) carryOnCompacted(t *turn) {
	if c.gone || c.turn != nil || c.rolling || c.compaction != nil {
		return
	}
	c.add("notice", t.id).Text = carryOnNotice
	user := c.add("user", "")
	user.Turn, user.Text, user.Hidden = user.ID, carryOnPrompt, true
	c.beginTurn(user, carryOnPrompt, attached{})
	if next := c.turn; next != nil {
		next.carries = t.carries + 1
		for _, o := range c.outbox {
			if o.turn == t {
				o.turn = next
			}
		}
	}
}
