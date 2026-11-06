package cli

import (
	"strings"
	"testing"

	"agentbox/internal/api"
)

func TestGoalColumnAndPrint(t *testing.T) {
	yes, no := true, false
	g := &api.AgentGoal{Objective: "ship it", Source: api.GoalSourceAgentBox, Status: api.GoalActive, Budget: 10, Continuations: 2,
		Checks: []api.GoalCheck{{Kind: api.GoalCheckPROpen, Met: &yes, Detail: "#4 is open"}, {Kind: api.GoalCheckCommand, Command: "make test", Met: &no}}}
	for goal, want := range map[*api.AgentGoal]string{
		nil:                       "-",
		g:                         "active:1/2",
		{Status: api.GoalMet}:     "met",
		{Status: api.GoalCleared}: "-",
	} {
		if got := goalColumn(goal); got != want {
			t.Errorf("goalColumn(%+v) = %q, want %q", goal, got, want)
		}
	}
	var b strings.Builder
	printGoal(&b, *g)
	for _, want := range []string{"Goal: ship it", "State: active", "✓ pr_open: #4 is open", "✗ make test", "Sent back 2 of 10 times"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("printGoal wrote %q, without %q", b.String(), want)
		}
	}
}
