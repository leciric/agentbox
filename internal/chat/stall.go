package chat

import (
	"fmt"
	"time"

	"agentbox/internal/api"
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
	// ToolRunning says one of the turn's tool calls hasn't ended, or one of
	// its subagents' has, so the adapter may rightly have nothing to say
	// until it does.
	ToolRunning bool
	// RunningTool names that call: the tool's own name, or its title, and
	// the subagent it runs in, if it does.
	RunningTool string
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
	// A subagent's call comes first: the agent's own running call is then
	// most likely the Agent call that started it, which says less. A
	// subagent in the background has no call of the agent's running at all.
	if c.adapter != nil {
		for _, sa := range c.adapter.subagents {
			if it := runningTool(sa.tools); it != nil {
				p.ToolRunning = true
				p.RunningTool = fmt.Sprintf("%s, in its subagent %q", toolLabel(it.Tool), sa.card.Subagent.Name)
				break
			}
		}
	}
	if it := runningTool(c.tools); it != nil && !p.ToolRunning {
		p.ToolRunning, p.RunningTool = true, toolLabel(it.Tool)
	}
	if c.session.StalledSince != nil {
		since := *c.session.StalledSince
		p.Stalled = &since
	}
	return p, true
}

// runningTool is one of tools that hasn't ended, or nil.
func runningTool(tools map[string]*api.ChatItem) *api.ChatItem {
	for _, it := range tools {
		if it.Tool != nil && (it.Tool.Status == "pending" || it.Tool.Status == "in_progress") {
			return it
		}
	}
	return nil
}

// toolLabel is what a tool call is called in a notice: the tool's own name,
// like mcp__playwright__browser_evaluate, or else its title.
func toolLabel(t *api.ChatTool) string {
	if t.Name != "" {
		return t.Name
	}
	title := []rune(cmp(t.Title, "a tool call"))
	if len(title) > 80 {
		return string(title[:79]) + "…"
	}
	return string(title)
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
