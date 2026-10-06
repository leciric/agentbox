package chat

import (
	"context"
	"time"

	"agentbox/internal/state"
)

// Continue agents after restarts. AgentBox stopping — an update, a crash, a
// quit, the VM or the user's machine rebooting — ends every turn that was
// running, and settle marks each one failed on the next start. So that the
// work carries on instead, every turn is recorded in the store as it starts
// (running_turns) and forgotten once it ends on its own; one AgentBox cut
// short stays recorded, and the next daemon reads the record and calls
// Resume, which starts a turn telling the chat to carry on. Reading the
// record rather than a list written at shutdown is what makes a crash count
// as well as a clean quit.
//
// The model sees resumePrompt in a turn of its own, after the session is
// loaded again (connect) or, when it can't be, after a fresh one is told the
// conversation (the context transfer from rollbacks, timeline.go). The chat
// shows resumedNotice where the interrupted turn ended.

// resumePrompt is what a chat is told when its turn is carried on.
const resumePrompt = "AgentBox restarted while you were working; continue where you left off."

// resumedNotice is what the conversation says happened.
const resumedNotice = "AgentBox restarted while this turn was running, and resumed it."

// heldNotice is what it says while the agent's machine waits for memory.
const heldNotice = "AgentBox restarted while this turn was running. It resumes once the agent's machine has the memory to start."

// lostTurnGrace is how long a turn that failed because its AI tool went away
// stays recorded. AgentBox stopping can kill the tools a moment before the
// daemon itself, and that turn is one to carry on; a daemon still running
// once this has passed means the tool went on its own.
const lostTurnGrace = 30 * time.Second

// maxPromptRecord is how much of a turn's prompt the record keeps: enough to
// say what the turn was, which is all it is read for.
const maxPromptRecord = 2000

// closed is closed once Close has begun.
func (m *Manager) closed() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closedCh == nil {
		m.closedCh = make(chan struct{})
	}
	return m.closedCh
}

// recordTurn stores that t runs. The conversation is locked.
func (c *conversation) recordTurn(t *turn) {
	if c.gone {
		return
	}
	rt := state.RunningTurn{
		Project: c.agent.Project, Agent: c.agent.Name, Kind: state.TurnKind(c.agent),
		Turn: t.id, Prompt: headText(t.text, maxPromptRecord), StartedAt: t.startedAt, Resumes: t.resumes,
	}
	if err := c.m.Store.SaveRunningTurn(context.Background(), rt); err != nil {
		c.m.logf("chat %s: recording the running turn: %v", c.agent.Ref(), err)
	}
}

// unrecordTurn forgets t, which has ended, unless AgentBox stopping is what
// ended it. err is why it failed, if it did. The conversation is locked.
func (c *conversation) unrecordTurn(t *turn, err error) {
	if c.m.closing.Load() {
		return
	}
	project, name := c.agent.Project, c.agent.Name
	end := func() {
		if c.m.closing.Load() {
			return
		}
		if err := c.m.Store.EndRunningTurn(context.Background(), project, name, t.id); err != nil {
			c.m.logf("chat %s/%s: forgetting the running turn: %v", project, name, err)
		}
	}
	if err != nil && !t.cancelled {
		// Failed on its own, perhaps because AgentBox is going down and took
		// the AI tool first: decided once that has had time to show.
		// Not one of m.background's: Wait is for the chat's own work, not
		// this, and Close ends it.
		closed := c.m.closed()
		go func() {
			select {
			case <-time.After(lostTurnGrace):
				end()
			case <-closed:
			}
		}()
		return
	}
	end()
}

// Resume carries on a turn that AgentBox's restart cut short: it starts a
// turn of its own telling the chat to continue, and says so in the
// conversation. It reports whether it did.
//
// It doesn't when the turn is no longer the conversation's latest, or when
// something already runs on the chat: a message somebody sent since, a
// notice, a resume already made. That work carries on by itself, and a
// second turn would only do it twice. The record is then dropped.
func (m *Manager) Resume(a state.Agent, rt state.RunningTurn) (bool, error) {
	c, err := m.conversation(a)
	if err != nil {
		return false, err
	}
	defer c.mu.Unlock()
	c.heldResume = nil
	if !c.resumable(rt) {
		c.dropRecord(rt)
		return false, nil
	}
	c.carryOn(rt)
	return true, nil
}

// HoldResume is Resume for an agent whose machine waits for the memory to
// start: the conversation says so, and Release carries the turn on. Stop
// gives up on it.
func (m *Manager) HoldResume(a state.Agent, rt state.RunningTurn) error {
	c, err := m.conversation(a)
	if err != nil {
		return err
	}
	defer c.mu.Unlock()
	if !c.resumable(rt) {
		c.dropRecord(rt)
		return nil
	}
	held := rt
	c.heldResume = &held
	c.add("notice", rt.Turn).Text = heldNotice
	c.flush(true)
	return nil
}

// resumable reports whether rt is still the conversation's latest turn, and
// nothing runs. The conversation is locked.
func (c *conversation) resumable(rt state.RunningTurn) bool {
	if c.turn != nil || c.rolling || c.compaction != nil {
		return false
	}
	for i := len(c.items) - 1; i >= 0; i-- {
		if c.items[i].Kind == "user" {
			return c.items[i].ID == rt.Turn
		}
	}
	return false
}

// carryOn starts the turn that carries rt on. The conversation is locked and
// resumable(rt).
func (c *conversation) carryOn(rt state.RunningTurn) {
	c.add("notice", rt.Turn).Text = resumedNotice
	// The prompt is the model's to read; the notice is what the user reads.
	user := c.add("user", "")
	user.Turn, user.Text, user.Hidden = user.ID, resumePrompt, true
	c.beginResumedTurn(user, resumePrompt, nil, rt.Resumes+1)
}

// releaseResume carries on the turn HoldResume kept, if any, and reports
// whether it started one. The conversation is locked.
func (c *conversation) releaseResume() bool {
	rt := c.heldResume
	if rt == nil {
		return false
	}
	c.heldResume = nil
	if !c.resumable(*rt) {
		c.dropRecord(*rt)
		return false
	}
	c.carryOn(*rt)
	return true
}

// dropResume gives up on a held resume: the agent was stopped while its
// machine waited. The conversation is locked.
func (c *conversation) dropResume() {
	if rt := c.heldResume; rt != nil {
		c.heldResume = nil
		c.dropRecord(*rt)
	}
}

func (c *conversation) dropRecord(rt state.RunningTurn) {
	if err := c.m.Store.EndRunningTurn(context.Background(), rt.Project, rt.Agent, rt.Turn); err != nil {
		c.m.logf("chat %s: forgetting the running turn: %v", rt.Ref(), err)
	}
}

// NoteNotResumed tells a conversation that its turn, cut short by a restart,
// isn't carried on, and why.
func (m *Manager) NoteNotResumed(a state.Agent, rt state.RunningTurn, why string) {
	c, err := m.conversation(a)
	if err != nil {
		return
	}
	defer c.mu.Unlock()
	c.dropRecord(rt)
	if c.byID[rt.Turn] == nil {
		return
	}
	c.add("notice", rt.Turn).Text = "AgentBox restarted while this turn was running, and didn't resume it: " + why + "."
	c.flush(true)
}

// handoffBefore is the context transfer for a fresh session that replaces
// one which couldn't be resumed: the conversation up to the running turn,
// whose own prompt follows it. The conversation is locked.
func (c *conversation) handoffBefore(tool string) string {
	items := c.items
	if c.turn != nil {
		for i, it := range items {
			if it.ID == c.turn.id {
				items = items[:i]
				break
			}
		}
	}
	return Handoff(items, "Your earlier "+tool+" session couldn't be resumed, so this is a new one.")
}
