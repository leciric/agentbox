package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/state"
)

// An agent's task, until its AI tool has it, is kept in the store
// (state.AgentTask) from the moment the agent is added: the daemon sends it
// as the chat's first message once the machine is made, and AgentBox
// stopping in between, or before that message's prompt reached the tool,
// used to lose it — the next message ("carry on") reached a session that had
// never been told what to do. So the first prompt any of the agent's
// sessions is sent carries the task when it doesn't already, and hands it
// over: the record is deleted as that prompt goes.

// taskNote heads a task that goes with a later turn's prompt.
const taskNote = "[AgentBox: this is your task. It was sent to you when you were made, but AgentBox stopped before it reached you, so act on it now.]"

// taskNotice is what the conversation says when that happens.
const taskNotice = "This agent's task hadn't reached %s: AgentBox stopped before it did. It went with this turn."

// withTask is the prompt text of t, whose prompt is about to go to the AI
// tool, with the agent's task in front when the tool hasn't had it and
// neither t's text nor handoff, which goes with it, carries it already. It
// reports whether the task is to be handed over as the prompt goes
// (handTaskOver). The conversation is locked.
func (c *conversation) withTask(t *turn, text, handoff string) (string, bool) {
	if c.taskSettled || c.agent.IsLead() || c.gone {
		return text, false
	}
	task, ok, err := c.m.Store.AgentTaskOf(context.Background(), c.agent.Project, c.agent.Name)
	if err != nil {
		c.m.logf("chat %s: reading its task: %v", c.agent.Ref(), err)
		return text, false // asked again with the next prompt
	}
	if !ok {
		c.taskSettled = true
		return text, false
	}
	if strings.Contains(text, task.Text) || strings.Contains(handoff, task.Text) {
		return text, true
	}
	it := c.add("notice", t.id)
	it.Text = fmt.Sprintf(taskNotice, ToolNames[c.agent.AI])
	it.Handoff = task.Text
	c.flush(true)
	if t.resumes > 0 && text == resumePrompt {
		// Carrying on a turn the task never reached (daemon.sendLeftTasks):
		// there is nothing to continue, only the task to start.
		return taskNote + "\n\n" + task.Text, true
	}
	return taskNote + "\n\n" + task.Text + "\n\n---\n\n" + text, true
}

// handTaskOver forgets the agent's task, which the prompt going now
// carries: from here on it is the session's.
func (c *conversation) handTaskOver(a state.Agent) {
	c.taskSettled = true
	if err := c.m.Store.DeleteAgentTask(context.Background(), a.Project, a.Name); err != nil {
		c.m.logf("chat %s: forgetting its task, which its AI tool has now: %v", a.Ref(), err)
	}
}

// RestoreTask puts an agent's task, which AgentBox stopped before sending,
// into its chat as the turn that stop cut short: its message, a notice
// saying so, and the record a turn running has (running_turns), so the
// daemon's continueTurns carries it on, holds it for the user or leaves it
// for the next message as it does any turn a restart cut short. Whichever
// turn runs next carries the task itself (withTask). It reports whether it
// did: a chat that holds the task already is left as it is.
func (m *Manager) RestoreTask(a state.Agent, text string) (bool, error) {
	c, err := m.conversation(a)
	if err != nil {
		return false, err
	}
	defer c.mu.Unlock()
	c.ensureLoaded()
	for _, it := range c.items {
		if it.Kind == "user" && strings.Contains(it.Text, text) {
			return false, nil
		}
	}
	user := c.add("user", "")
	user.Turn, user.Text = user.ID, text
	c.add("notice", user.ID).Text = restoredNotice
	c.flush(true)
	rt := state.RunningTurn{
		Project: a.Project, Agent: a.Name, Kind: state.TurnKind(a),
		Turn: user.ID, Prompt: headText(text, maxPromptRecord), StartedAt: time.Now(),
	}
	if err := m.Store.SaveRunningTurn(context.Background(), rt); err != nil {
		return true, err
	}
	return true, nil
}

// restoredNotice is what RestoreTask's notice says.
const restoredNotice = "AgentBox stopped after making this agent but before its task was sent. Here it is: it goes to the agent with its next turn."
