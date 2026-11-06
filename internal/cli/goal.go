package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newGoalCmd is `agentbox goal`: an agent's goal, shown, set, changed or
// cleared on an agent that already runs (create's --goal sets one at the
// start).
func newGoalCmd(a *app) *cobra.Command {
	var (
		checks []string
		budget int
		clear  bool
	)
	cmd := &cobra.Command{
		Use:   "goal <project/agent> [what done looks like]",
		Short: "Show, set, change or clear an agent's goal: what AgentBox checks after each of its turns, sending it back until it is met",
		Long: "With only the agent, shows its goal. With an objective, gives it that goal from now on, replacing the one it had:\n" +
			"it is told, and goes back to work towards it if it had finished. After each of its turns AgentBox runs the\n" +
			"--check checks and, while one fails, sends it back with what is missing, at most --budget times.",
		Example: "  agentbox goal pawly/agent-03 \"the reminders page is merged-ready\" --check pr_open --check ci_green\n" +
			"  agentbox goal pawly/agent-03 \"tests pass\" --check \"go test ./...\" --budget 5\n" +
			"  agentbox goal pawly/agent-03 --clear",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			objective := strings.TrimSpace(strings.Join(args[1:], " "))
			switch {
			case clear && objective != "":
				return errors.New("--clear takes no objective")
			case objective == "" && (len(checks) > 0 || budget != 0):
				return errors.New("--check and --budget go with an objective: what done looks like")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			ref := args[0]
			switch {
			case clear:
				res, err := c.ClearAgentGoal(cmd.Context(), ref)
				if err != nil {
					return err
				}
				if res.Goal == nil {
					_, _ = fmt.Fprintf(out, "%s has no goal\n", ref)
				} else {
					_, _ = fmt.Fprintf(out, "Cleared the goal of %s\n", ref)
				}
				return nil
			case objective == "":
				ag, err := c.Agent(cmd.Context(), ref)
				if err != nil {
					return err
				}
				if ag.Goal == nil {
					_, _ = fmt.Fprintf(out, "%s has no goal. Set one with: agentbox goal %s \"<what done looks like>\"\n", ref, ref)
					return nil
				}
				printGoal(out, *ag.Goal)
				return nil
			}
			res, err := c.SetAgentGoal(cmd.Context(), ref, *goalSpec(objective, checks, budget))
			if err != nil {
				return err
			}
			if res.Goal != nil {
				printGoal(out, *res.Goal)
			}
			switch {
			case res.Working:
				_, _ = fmt.Fprintf(out, "%s hears it mid-work, and is checked when that turn ends.\n", ref)
			case res.Woke != "":
				_, _ = fmt.Fprintf(out, "Its machine was %s; %s is working towards it.\n", res.Woke, ref)
			default:
				_, _ = fmt.Fprintf(out, "%s is working towards it.\n", ref)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&checks, "check", nil, "pr_open, ci_green (its pull request's checks all passed), or a shell command that must exit 0 in its worktree; repeat it for several")
	f.IntVar(&budget, "budget", 0, fmt.Sprintf("the most times the agent is sent back (default %d, at most %d)", api.DefaultGoalBudget, api.MaxGoalBudget))
	f.BoolVar(&clear, "clear", false, "clear its goal: nothing is checked after its turns any more")
	return cmd
}

// printGoal writes a goal as `agentbox goal` shows it.
func printGoal(w io.Writer, g api.AgentGoal) {
	_, _ = fmt.Fprintf(w, "Goal: %s\n", g.Objective)
	state := g.Status
	if g.Source == api.GoalSourceTool {
		state += " (the AI tool's own /goal)"
	}
	_, _ = fmt.Fprintf(w, "State: %s\n", state)
	if g.LastUnmet != "" && g.Status != api.GoalMet && g.Status != api.GoalCleared {
		_, _ = fmt.Fprintf(w, "Last: %s\n", g.LastUnmet)
	}
	for _, c := range g.Checks {
		mark := "· "
		switch {
		case c.Met == nil:
		case *c.Met:
			mark = "✓ "
		default:
			mark = "✗ "
		}
		name := c.Kind
		if c.Kind == api.GoalCheckCommand {
			name = c.Command
		}
		line := mark + name
		if c.Detail != "" {
			line += ": " + c.Detail
		}
		_, _ = fmt.Fprintln(w, line)
	}
	if g.Source == api.GoalSourceAgentBox && g.Budget > 0 {
		_, _ = fmt.Fprintf(w, "Sent back %d of %d times\n", g.Continuations, g.Budget)
	}
}

// goalColumn is a goal in `agentbox list`'s GOAL column: its state, and how
// many of its checks passed, with no spaces, since scripts split the columns.
func goalColumn(g *api.AgentGoal) string {
	if g == nil || g.Status == api.GoalCleared {
		return "-"
	}
	if len(g.Checks) == 0 {
		return g.Status
	}
	passed := 0
	for _, c := range g.Checks {
		if c.Met != nil && *c.Met {
			passed++
		}
	}
	return fmt.Sprintf("%s:%d/%d", g.Status, passed, len(g.Checks))
}
