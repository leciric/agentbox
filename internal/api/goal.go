package api

import "time"

// A goal is what an agent works towards across turns, and comes from one of
// two places:
//
//   - the AI tool's own /goal (GoalSourceTool), typed in the agent's chat:
//     Claude Code and Codex keep working until their own check says it is
//     met, and AgentBox only shows what their ACP adapter reports of it;
//   - one AgentBox gives it (GoalSourceAgentBox) — create_agent's goal, or
//     set later on a running agent (SetGoalRequest) — with checks the daemon runs
//     itself after each of the agent's turns: when one fails, the agent is
//     sent back with what is missing, until they all pass or the budget of
//     continuations runs out.

// Where a goal comes from (AgentGoal.Source).
const (
	GoalSourceTool     = "tool"
	GoalSourceAgentBox = "agentbox"
)

// A goal's status (AgentGoal.Status). Blocked is the only one that needs the
// user: the budget ran out, or the agent said it can't go on without them.
const (
	GoalActive  = "active"
	GoalMet     = "met"
	GoalBlocked = "blocked"
	GoalCleared = "cleared"
)

// The checks the daemon can verify itself (GoalCheck.Kind).
const (
	// GoalCheckPROpen passes when the agent has an open pull request.
	GoalCheckPROpen = "pr_open"
	// GoalCheckCIGreen passes when the agent's pull request has checks and
	// every one of them passed.
	GoalCheckCIGreen = "ci_green"
	// GoalCheckCommand passes when Command exits 0, run in the agent's
	// worktree in its own machine.
	GoalCheckCommand = "command"
)

// DefaultGoalBudget is how many times an agent is sent back to its goal when
// create_agent names no budget; MaxGoalBudget caps what it may name.
const (
	DefaultGoalBudget = 10
	MaxGoalBudget     = 50
)

// GoalSpec is the goal create_agent gives a new agent.
type GoalSpec struct {
	// Objective is what done looks like, in words: what the agent is told it
	// is working towards, and what each continuation reminds it of.
	Objective string `json:"objective"`
	// Checks are what the daemon verifies after each turn. None leaves the
	// objective alone: the agent is then done when it says it is.
	Checks []GoalCheck `json:"checks,omitempty"`
	// Budget is the most continuations it gets: 0 is DefaultGoalBudget.
	Budget int `json:"budget,omitempty"`
}

// SetGoal (PUT …/agents/{project}/{agent}/goal, and the lead's
// /v1/project/agents/{agent}/goal) takes a GoalSpec: the agent's goal from
// now on, replacing whatever it had, with its count of continuations back at
// 0. The agent is told it, and one that had finished or was stopped goes back
// to work towards it. DELETE clears it.

// GoalResult is what setting or clearing an agent's goal did.
type GoalResult struct {
	// Goal is the agent's goal now: cleared after a DELETE.
	Goal *AgentGoal `json:"goal,omitempty"`
	// Woke is "started" or "resumed" when the agent's machine had to be
	// started or resumed to tell it, as TellResult.Woke.
	Woke string `json:"woke,omitempty"`
	// Working says the agent was already in a turn: it hears of the goal
	// mid-work, and is checked against it when that turn ends.
	Working bool `json:"working,omitempty"`
}

// GoalCheck is one thing the daemon verifies of a goal.
type GoalCheck struct {
	Kind string `json:"kind"` // pr_open, ci_green or command
	// Command is a command's shell line.
	Command string `json:"command,omitempty"`
	// Met is how it went the last time it ran; absent before it has.
	Met *bool `json:"met,omitempty"`
	// Detail is what was found the last time: the pull request, the checks
	// still running, the end of the command's output.
	Detail string `json:"detail,omitempty"`
}

// AgentGoal is an agent's goal as the daemon keeps it (Agent.Goal), and as a
// goal row in its chat (ChatItem.Goal) shows it.
type AgentGoal struct {
	Objective string `json:"objective"`
	Source    string `json:"source"` // tool or agentbox
	Status    string `json:"status"` // active, met, blocked or cleared
	// Checks are an AgentBox goal's, with how each went last.
	Checks []GoalCheck `json:"checks,omitempty"`
	// Checked is how many times the goal was checked and found not yet met:
	// the tool's own iterations, or the daemon's verdicts.
	Checked int `json:"checked"`
	// LastUnmet is why the last check found it not met, or why it is blocked.
	LastUnmet string `json:"lastUnmet,omitempty"`
	// Budget and Continuations are an AgentBox goal's: the most times the
	// agent is sent back, and how many times it has been.
	Budget        int       `json:"budget,omitempty"`
	Continuations int       `json:"continuations,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Done says whether a goal no longer drives anything.
func (g AgentGoal) Done() bool { return g.Status != GoalActive }
