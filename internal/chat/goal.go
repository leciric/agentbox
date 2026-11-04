package chat

import (
	"strings"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Agent goals (Settings → Experimental, off by default). An AI tool with a
// /goal of its own — Claude Code, Codex, OpenCode from 2.2.1 — keeps working
// until its own check says the goal is met, and its ACP adapter reports the
// goal on session_info_update (acp.GoalSnapshot). This file turns those
// reports into the agent's goal (api.AgentGoal, source "tool"): a goal row
// in the chat, kept up to date, and the daemon told so it shows on the
// agent's row. The "Goal set: …" line the tool prints in answer to /goal
// becomes that row rather than a message of its own.
//
// Claude Code's adapter reports the goal as it is set and as it is cleared,
// and its check's verdicts only when the CLI emits them (iterations,
// lastReason). Its check runs as a Stop hook inside the one prompt: a turn
// that ends on its own while the goal is active ended because the check
// found it met, which is how met is known (goalTurnEnded). Codex says
// "complete" itself.
//
// Nothing here runs while agent goals are off: the tool's /goal then works
// exactly as before, its "Goal set" line an ordinary message.

// goalsOn says whether agent goals are on. Caller may hold c.mu.
func (m *Manager) goalsOn() bool { return m.GoalsOn != nil && m.GoalsOn() }

// goalCommand says whether a prompt is the tool's /goal command.
func goalCommand(text string) bool {
	text = strings.TrimSpace(text)
	return text == "/goal" || strings.HasPrefix(text, "/goal ") || strings.HasPrefix(text, "/goal\n")
}

// goalNow is the tool goal the conversation knows of, read from the daemon
// the first time it is asked (a chat loaded after a restart). Caller holds
// c.mu.
func (c *conversation) goalNow() *api.AgentGoal {
	if !c.goalRead {
		c.goalRead = true
		if c.m.GoalOf != nil {
			if g, ok := c.m.GoalOf(c.agent); ok && g.Source == api.GoalSourceTool {
				c.goal = &g
			}
		}
	}
	return c.goal
}

// toolGoal takes the goal an adapter reported: snap, or nil for cleared.
// Caller holds c.mu.
func (c *conversation) toolGoal(snap *acp.GoalSnapshot) {
	cur := c.goalNow()
	now := time.Now()
	if snap == nil {
		if cur == nil || cur.Done() {
			return
		}
		g := *cur
		g.Status, g.UpdatedAt = api.GoalCleared, now
		c.setToolGoal(g, "cleared")
		return
	}
	status, why := goalStatusOf(snap.Status)
	if cur == nil || cur.Objective != snap.Objective || (cur.Done() && status == api.GoalActive) {
		g := api.AgentGoal{
			Objective: snap.Objective, Source: api.GoalSourceTool, Status: status,
			Checked: snap.Iterations, LastUnmet: cmp(snap.LastReason, why), UpdatedAt: now,
		}
		c.setToolGoal(g, "set")
		return
	}
	g := *cur
	changed := g.Status != status
	g.Status, g.UpdatedAt = status, now
	if snap.Iterations > g.Checked {
		g.Checked = snap.Iterations
	}
	if r := cmp(snap.LastReason, why); r != "" {
		g.LastUnmet = r
	}
	if !changed && g.Checked == cur.Checked && g.LastUnmet == cur.LastUnmet {
		return
	}
	event := "checked"
	if changed {
		event = g.Status
	}
	c.setToolGoal(g, event)
}

// goalStatusOf maps an adapter's goal status to AgentGoal's, and says why
// for one that stops it without meeting it.
func goalStatusOf(status string) (string, string) {
	switch status {
	case "complete", "met":
		return api.GoalMet, ""
	case "blocked":
		return api.GoalBlocked, "The tool says it is blocked"
	case "limited":
		return api.GoalBlocked, "The tool's budget for it ran out"
	case "paused":
		return api.GoalBlocked, "Paused"
	default:
		return api.GoalActive, ""
	}
}

// goalTurnEnded is a turn's end as a Claude Code tool goal sees it: one that
// completed on its own while the goal was active got past the goal's Stop
// hook, which only lets a turn end once its check finds the goal met.
// Caller holds c.mu.
func (c *conversation) goalTurnEnded(t *turn, completed bool) {
	if !c.m.goalsOn() || c.agent.AI != "claude" {
		return
	}
	g := c.goalNow()
	if g == nil || g.Done() || !completed || t.cancelled {
		return
	}
	if goalCommand(t.text) && strings.HasPrefix(strings.TrimSpace(t.text), "/goal clear") {
		return
	}
	next := *g
	next.Status, next.UpdatedAt = api.GoalMet, time.Now()
	if said := strings.ToLower(strings.TrimSpace(c.lastSaid(t.id))); strings.HasPrefix(said, "goal paused") || strings.HasPrefix(said, "goal could not be achieved") {
		next.Status, next.LastUnmet = api.GoalBlocked, firstLine(c.lastSaid(t.id))
	}
	c.setToolGoal(next, next.Status)
}

// setToolGoal records the tool goal g, adds or updates its row, and tells the
// daemon, off the lock. event is what happened: set, checked, met, blocked or
// cleared. Caller holds c.mu.
func (c *conversation) setToolGoal(g api.AgentGoal, event string) {
	c.goal, c.goalRead = &g, true
	c.showGoal(g, event)
	if c.m.GoalChanged != nil {
		agent := c.agent
		c.m.background.Go(func() { c.m.GoalChanged(agent, g) })
	}
}

// showGoal adds the goal row for event, or, for a check that changes
// nothing but the count, updates the row the goal already has. Caller holds
// c.mu.
func (c *conversation) showGoal(g api.AgentGoal, event string) *api.ChatItem {
	if event == "checked" {
		for i := len(c.items) - 1; i >= 0; i-- {
			it := c.items[i]
			if it.Kind == "goal" && it.Goal != nil && it.Goal.Objective == g.Objective && it.Goal.Status == api.GoalActive {
				it.Goal = &g
				it.Text = goalText(g, event)
				c.touch(it)
				c.flush(true)
				return it
			}
		}
	}
	c.closeOpen()
	turn := c.lastTurn()
	if c.turn != nil {
		turn = c.turn.id
	}
	it := c.add("goal", turn)
	it.Goal, it.Text = &g, goalText(g, event)
	c.flush(true)
	return it
}

// goalText is a goal row's words, for whoever reads the chat as text (the
// lead's read_chat, a search); the app draws the row from Goal.
func goalText(g api.AgentGoal, event string) string {
	switch event {
	case "set":
		return "Goal set: " + g.Objective
	case "met":
		return "Goal met: " + g.Objective
	case "blocked":
		return "Goal blocked: " + cmp(g.LastUnmet, g.Objective)
	case "cleared":
		return "Goal cleared: " + g.Objective
	default:
		if g.LastUnmet != "" {
			return "Goal not met yet: " + g.LastUnmet
		}
		return "Goal checked: " + g.Objective
	}
}

// goalEcho says whether text, a message the tool started in a /goal turn, is
// its own echo of the command ("Goal set: …", "Goal cleared…"), which the
// goal row stands for. Caller holds c.mu.
func (c *conversation) goalEcho(text string) bool {
	if c.turn == nil || !goalCommand(c.turn.text) || !c.m.goalsOn() {
		return false
	}
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "Goal set: ") || strings.HasPrefix(text, "Goal cleared")
}

// ShowGoal adds a goal row to an agent's chat for a goal the daemon drives
// (source "agentbox"): set, checked, met, blocked. It starts no turn.
func (m *Manager) ShowGoal(a state.Agent, g api.AgentGoal, event string) (api.ChatItem, error) {
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatItem{}, err
	}
	defer c.mu.Unlock()
	it := c.showGoal(g, event)
	return clone(*it), nil
}
