package chat

import "time"

// Putting an idle chat's AI tool to sleep. A project's lead keeps its adapter,
// and the AI tool behind it, running for as long as the daemon does: some
// 200–230 MB of the VM's memory each, for a chat that is idle most of the day.
// So once a chat has sat idle long enough (Manager.IdleStop) its adapter is
// stopped, and the conversation is asleep: it still reads as ready, keeps its
// settings, its context ring and its session id, and the next thing that
// starts a turn — a message, a notice an agent's finish or a schedule acts on,
// a project's message — starts the adapter again, which resumes the same
// session (connect) the way a reload of its tools or an account switch does.
// Nobody sees a difference but a few seconds of "Starting" on that turn.
//
// Only an idle chat sleeps: no turn, no permission waiting, no hidden prompt
// or compaction, nothing waiting to be sent, no background command or monitor
// that would wake it, no usage-limit wait, and an adapter that can resume.
// A schedule about to fire keeps it up too (Manager.StayUp).

// idleLook is the longest the idle wait goes without reading the setting
// again, so a change to it, or turning it back on, reaches chats that are
// already running within minutes rather than at the old wait's end.
const idleLook = 5 * time.Minute

// watchIdle starts the wait after which an adapter that has just become ready
// may sleep. The conversation is locked.
func (c *conversation) watchIdle(ad *adapter) {
	if c.m.IdleStop == nil {
		return
	}
	// The setting is read as the timer fires, off the lock, and idleDue
	// waits out the rest of it from there.
	c.armIdle(ad, time.Second)
}

// armIdle (re)sets the idle wait for ad, d from now. The conversation is
// locked; the timer fires off it.
func (c *conversation) armIdle(ad *adapter, d time.Duration) {
	if c.idleTimer != nil {
		c.idleTimer.Stop()
	}
	c.idleGen++
	gen := c.idleGen
	c.idleTimer = c.m.after(d, func() { c.idleDue(ad, gen) })
}

// idleDue is the idle wait ending: the adapter sleeps when the chat has been
// idle for the setting's time, and the wait starts again otherwise.
func (c *conversation) idleDue(ad *adapter, gen int64) {
	c.mu.Lock()
	if c.adapter != ad || c.idleGen != gen || c.gone {
		c.mu.Unlock()
		return
	}
	a := c.agent
	c.mu.Unlock()

	after, applies := c.m.IdleStop(a)
	stay := applies && after > 0 && c.m.StayUp != nil && c.m.StayUp(a)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.adapter != ad || c.idleGen != gen || c.gone {
		return
	}
	switch {
	case !applies:
		c.idleTimer = nil
		return
	case after <= 0:
		c.armIdle(ad, idleLook)
		return
	}
	if wait := c.activeAt.Add(after).Sub(c.m.now()); wait > 0 {
		c.armIdle(ad, min(wait, idleLook))
		return
	}
	if stay || !c.sleepable() {
		// Busy, or about to be: what it is doing ends with more activity,
		// from which the wait starts again.
		c.armIdle(ad, min(after, idleLook))
		return
	}
	c.idleTimer = nil
	c.m.logf("chat %s: idle for %s; stopping %s until the next turn, which resumes this session", a.Ref(), after, ToolNames[a.AI])
	c.stopAdapter()
	c.asleep, c.sleptOn = true, ad.login
	c.session.State = c.stateNow()
	c.markSession()
	c.flush(false)
}

// sleepable says nothing the adapter is doing would be lost with it, and the
// next adapter can carry its session on. The conversation is locked.
func (c *conversation) sleepable() bool {
	ad := c.adapter
	return ad != nil && ad.ready && ad.resumable && ad.err == nil &&
		c.turn == nil && len(c.pending) == 0 && !c.rolling && c.compaction == nil &&
		len(c.outbox) == 0 && !c.draining && len(c.queued) == 0 &&
		len(ad.background()) == 0 && c.resumeTimer == nil && c.heldResume == nil
}

// wakeForPrompt starts an asleep chat's adapter again for a hidden prompt
// (Ask, BeginCompact), which needs the session but heads no turn. It
// reports whether an adapter runs now. The conversation is locked.
func (c *conversation) wakeForPrompt() bool {
	if c.adapter == nil && c.asleep {
		c.startAdapter()
	}
	return c.adapter != nil
}

// Asleep says whether an agent's chat has put its AI tool to sleep while it is
// idle (idle.go): it reads as ready, and its next turn starts the tool again.
func (m *Manager) Asleep(ref string) bool {
	c := m.existing(ref)
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.asleep
}

// noteActive marks the conversation active now, for the idle wait. The
// conversation is locked.
func (c *conversation) noteActive() {
	if c.m.IdleStop != nil {
		c.activeAt = c.m.now()
	}
}
