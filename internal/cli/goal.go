package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newGoalCmd is `agentbox goal`: an existing agent's goal shown, set, changed
// or cleared (internal/daemon/goals.go).
func newGoalCmd(a *app) *cobra.Command {
	var (
		checks []string
		budget int
		clear  bool
	)
	cmd := &cobra.Command{
		Use:   "goal <project/agent> [objective]",
		Short: "Show, set or clear an agent's goal: what done looks like, with checks AgentBox runs after each turn",
		Long: `Show an agent's goal, or set one on an agent that exists: AgentBox tells it the goal and, after each of
its turns, runs the --check checks, sending it back with what is missing until they pass or --budget runs
out. An idle or finished agent goes back to work on it; a working one reads it mid-turn. A new goal
replaces the one it had. --clear stops AgentBox checking it.`,
		Example: `  agentbox goal pawly/agent-01
  agentbox goal pawly/agent-01 "the reminders page is merged-ready" --check pr_open --check ci_green
  agentbox goal pawly/agent-01 "tests pass" --check "go test ./..." --budget 5
  agentbox goal pawly/agent-01 --clear`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, objective := args[0], ""
			if len(args) > 1 {
				objective = args[1]
			}
			if clear && (objective != "" || len(checks) > 0 || budget != 0) {
				return errors.New("--clear takes no objective, --check or --budget")
			}
			if objective == "" && !clear && (len(checks) > 0 || budget != 0) {
				return errors.New("--check and --budget go with an objective: agentbox goal <project/agent> \"<objective>\" --check …")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case clear:
				res, err := c.ClearGoal(cmd.Context(), ref)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(out, "Cleared the goal of %s: %s\n", ref, res.Goal.Objective)
				return nil
			case objective != "":
				res, err := c.SetGoal(cmd.Context(), ref, *goalSpec(objective, checks, budget))
				if err != nil {
					return err
				}
				how := "it was idle, so it is working towards it now"
				if res.Told == "aside" {
					how = "it reads it mid-work"
				}
				if res.Woke != "" {
					how += " (its machine was " + res.Woke + " first)"
				}
				_, _ = fmt.Fprintf(out, "Set the goal of %s; %s.\n", ref, how)
				printGoal(out, &res.Goal)
				return nil
			}
			ag, err := c.Agent(cmd.Context(), ref)
			if err != nil {
				return err
			}
			if ag.Goal == nil || ag.Goal.Status == api.GoalCleared {
				_, _ = fmt.Fprintf(out, "%s has no goal. Set one with: agentbox goal %s \"<what done looks like>\" --check pr_open\n", ref, ref)
				return nil
			}
			printGoal(out, ag.Goal)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&checks, "check", nil, "pr_open, ci_green (its pull request's checks all passed), or a shell command that must exit 0 in its worktree; repeat it for several")
	f.IntVar(&budget, "budget", 0, fmt.Sprintf("the most times the agent is sent back (default %d, at most %d)", api.DefaultGoalBudget, api.MaxGoalBudget))
	f.BoolVar(&clear, "clear", false, "clear its goal: AgentBox stops checking it")
	return cmd
}

// printGoal writes a goal out, a line a field.
func printGoal(w io.Writer, g *api.AgentGoal) {
	_, _ = fmt.Fprintf(w, "Goal:    %s\n", g.Objective)
	status := g.Status
	if g.Source == api.GoalSourceTool {
		status += " (its AI tool's own /goal)"
	}
	_, _ = fmt.Fprintf(w, "Status:  %s\n", status)
	for _, c := range g.Checks {
		mark := "·"
		if c.Met != nil {
			mark = map[bool]string{true: "✓", false: "✗"}[*c.Met]
		}
		what := c.Kind
		if c.Kind == api.GoalCheckCommand {
			what = "`" + c.Command + "`"
		}
		line := fmt.Sprintf("  %s %s", mark, what)
		if c.Detail != "" {
			line += ": " + strings.ReplaceAll(c.Detail, "\n", " ")
		}
		_, _ = fmt.Fprintln(w, line)
	}
	if g.Source == api.GoalSourceAgentBox && g.Budget > 0 {
		_, _ = fmt.Fprintf(w, "Budget:  %d of %d left\n", g.BudgetLeft(), g.Budget)
	}
	if g.LastUnmet != "" && g.Status != api.GoalMet {
		_, _ = fmt.Fprintf(w, "Missing: %s\n", g.LastUnmet)
	}
}

// goalColumn is an agent's goal in `agentbox list`'s GOAL column, one word:
// its status, with how many of its checks passed while it is active.
func goalColumn(g *api.AgentGoal) string {
	if g == nil || g.Status == api.GoalCleared {
		return "-"
	}
	if g.Status != api.GoalActive || len(g.Checks) == 0 {
		return g.Status
	}
	passed := 0
	for _, c := range g.Checks {
		if c.Met != nil && *c.Met {
			passed++
		}
	}
	return fmt.Sprintf("active:%d/%d", passed, len(g.Checks))
}
