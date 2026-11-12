package chat

import (
	"time"

	"agentbox/internal/api"
)

// A conversation, once read, kept its whole transcript in memory for as long
// as the daemon ran: every agent whose chat was opened or that ran a turn,
// every lead, thousands of items each with up to 8 KiB of tool output and
// 128 KiB of diff. Over a few days that was most of the daemon's heap, for
// chats nobody was looking at. Unload lets those go; the store has them, and
// the next read loads them again (load), as it would after a daemon restart:
// what an idle chat holds only in memory a restart would lose as well.

// UnloadAfter is how long a conversation nobody has read or written stays in
// memory.
const UnloadAfter = 10 * time.Minute

// Unload lets go of the transcripts of the conversations nobody has touched
// for idle, and returns how many it let go. A conversation with anything
// under way keeps its own: a turn, a hidden prompt, a compaction, messages
// waiting, a held or scheduled resume, a subagent still running, an adapter
// still starting or replaying, or changes not yet published or stored.
//
// An adapter that is merely running between turns doesn't keep it. Every
// running agent's chat has one for as long as its machine runs, and so does
// every lead that doesn't sleep: those transcripts grew for the daemon's
// life, and they were most of what a daemon held after a day. What the
// adapter says between turns loads the transcript again first
// (ensureLoaded, in handler.Notify and add).
func (m *Manager) Unload(idle time.Duration) int {
	m.mu.Lock()
	convs := make([]*conversation, 0, len(m.convs))
	for _, c := range m.convs {
		convs = append(convs, c)
	}
	m.mu.Unlock()
	cutoff := m.now().Add(-idle)
	n := 0
	for _, c := range convs {
		c.mu.Lock()
		if c.loaded && c.usedAt.Before(cutoff) && c.unloadable() {
			c.loaded, c.items, c.byID = false, nil, map[string]*api.ChatItem{}
			if c.adapter != nil {
				// Every one has ended (adapterIdle): what they held were
				// items of the transcript let go just now.
				c.adapter.subagents = nil
			}
			n++
		}
		c.mu.Unlock()
	}
	return n
}

// unloadable is whether nothing in the conversation holds on to its items or
// is about to change them. c.mu is held.
func (c *conversation) unloadable() bool {
	return (c.adapter == nil || c.adapterIdle()) && c.turn == nil && !c.rolling && c.capture == nil &&
		c.compaction == nil && c.open == nil && c.plan == nil && len(c.tools) == 0 &&
		len(c.pending) == 0 && len(c.queued) == 0 && len(c.outbox) == 0 && !c.draining &&
		c.heldResume == nil && c.resumeTimer == nil &&
		len(c.appends) == 0 && len(c.dirty) == 0 && len(c.unsaved) == 0 && !c.sessionDirty && c.timer == nil &&
		!c.asksForAgent()
}

// asksForAgent is whether a lead's transcript shows an agent's permission
// request still unanswered, which Attention counts from the items.
func (c *conversation) asksForAgent() bool {
	for _, it := range c.items {
		if p := it.AgentPermission; p != nil && p.Permission.Outcome == "" {
			return true
		}
	}
	return false
}

// adapterIdle is whether the running adapter holds nothing of the transcript:
// its session is set up, not replaying history, and none of its subagents is
// still going (a subagent's card and items are pointers into c.items, which
// it would go on changing). c.mu is held.
func (c *conversation) adapterIdle() bool {
	ad := c.adapter
	if !ad.ready || ad.replaying || ad.replayTail {
		return false
	}
	for _, sa := range ad.subagents {
		if sa.card != nil && sa.card.Subagent != nil && sa.card.Subagent.State == subagentRunning {
			return false
		}
	}
	return true
}

// ensureLoaded loads a transcript Unload let go while its adapter ran, before
// anything reads or adds to it. c.mu is held.
func (c *conversation) ensureLoaded() {
	if c.loaded {
		return
	}
	if err := c.load(); err != nil {
		c.m.logf("chat %s: loading the transcript again: %v", c.agent.Ref(), err)
	}
}

// Sizes is how many conversations are in memory, how many of them have their
// transcript loaded, and how many items those hold (agentbox debug stats).
func (m *Manager) Sizes() (convs, loaded, items int) {
	m.mu.Lock()
	all := make([]*conversation, 0, len(m.convs))
	for _, c := range m.convs {
		all = append(all, c)
	}
	m.mu.Unlock()
	for _, c := range all {
		c.mu.Lock()
		if c.loaded {
			loaded++
			items += len(c.items)
		}
		c.mu.Unlock()
	}
	return len(all), loaded, items
}
