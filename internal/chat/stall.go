package chat

import (
	"time"

	"agentbox/internal/state"
)

// A turn in progress can go on looking alive after it has stopped getting
// anywhere: its prompt is a call with no deadline, answered only when the AI
// tool ends the turn or its adapter's output closes, and an adapter behind a
// hung `incus exec` or in a frozen machine does neither. The chat keeps the
// clock (Progress) and the mark (SetStalled); the daemon's stall watch decides
// when the one means the other.

// Progress is how a running turn is getting on, for the daemon's stall watch.
type Progress struct {
	Turn      string // the turn's user message: another one is another turn
	StartedAt time.Time
	// LastProgress is when the adapter last sent anything while the turn ran
	// — an update, a subagent's, a permission request — or, failing that,
	// when the turn began or somebody last answered one of its requests.
	LastProgress time.Time
	// ToolRunning says one of the turn's tool calls hasn't ended, so the
	// adapter may rightly have nothing to say until it does.
	ToolRunning bool
	// Waiting says the turn is waiting on a person: a permission request.
	Waiting bool
	// Stalled is when the turn was marked stalled (SetStalled), from.
	Stalled *time.Time
}

// Progress says how an agent's running turn is getting on; ok is false when
// no turn runs.
func (m *Manager) Progress(ref string) (p Progress, ok bool) {
	c := m.existing(ref)
	if c == nil {
		return Progress{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.turn
	if t == nil {
		return Progress{}, false
	}
	p = Progress{Turn: t.id, StartedAt: t.startedAt, LastProgress: t.progressAt, Waiting: len(c.pending) > 0}
	for _, it := range c.tools {
		if it.Tool.Status == "pending" || it.Tool.Status == "in_progress" {
			p.ToolRunning = true
			break
		}
	}
	if c.session.StalledSince != nil {
		since := *c.session.StalledSince
		p.Stalled = &since
	}
	return p, true
}

// SetStalled marks turn stalled since the moment it last showed progress, or
// clears the mark with a nil since. It reports whether that changed anything:
// a turn that has already ended, or has since been replaced, is left alone.
func (m *Manager) SetStalled(a state.Agent, turn string, since *time.Time) bool {
	c := m.existing(a.Ref())
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turn == nil || c.turn.id != turn || (since == nil) == (c.session.StalledSince == nil) {
		return false
	}
	c.session.StalledSince = since
	c.markSession()
	c.flush(false)
	return true
}

// Error is why an agent's chat stopped, while it is stopped by an error.
func (m *Manager) Error(ref string) string {
	c := m.existing(ref)
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session.Error
}

// progressed restarts the running turn's clock, and takes back a stall the
// turn has just shown it's over. The conversation is locked.
func (c *conversation) progressed() {
	if c.turn == nil {
		return
	}
	c.turn.progressAt = c.m.now()
	if c.session.StalledSince != nil {
		c.session.StalledSince = nil
		c.markSession()
	}
}
