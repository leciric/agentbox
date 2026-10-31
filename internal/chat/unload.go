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
// under way keeps its own: an adapter running (its session can speak at any
// time), a turn, a hidden prompt, a compaction, messages waiting, a held or
// scheduled resume, or changes not yet published or stored.
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
			n++
		}
		c.mu.Unlock()
	}
	return n
}

// unloadable is whether nothing in the conversation holds on to its items or
// is about to change them. c.mu is held.
func (c *conversation) unloadable() bool {
	return c.adapter == nil && c.turn == nil && !c.rolling && c.capture == nil &&
		c.compaction == nil && c.open == nil && c.plan == nil && len(c.tools) == 0 &&
		len(c.pending) == 0 && len(c.queued) == 0 && len(c.outbox) == 0 && !c.draining &&
		c.heldResume == nil && c.resumeTimer == nil &&
		len(c.appends) == 0 && len(c.dirty) == 0 && len(c.unsaved) == 0 && !c.sessionDirty && c.timer == nil
}
