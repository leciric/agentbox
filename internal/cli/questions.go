package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newQuestionsCmd shows what a project's agents are waiting on, and what its
// chat decided was the user's call rather than its own.
func newQuestionsCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "questions <project>",
		Short: "What this project's agents are waiting to be told",
		Long: `Lists the questions this project's agents asked.

An agent that asks is blocked until somebody answers. Its project's chat answers
what it can; what reaches you here is what the chat decided was your call.

Answer one with: agentbox answer <project> <id> "<answer>"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			questions, err := c.Questions(cmd.Context(), args[0], all)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(questions) == 0 {
				fmt.Fprintln(out, "No agent is waiting to be told anything.")
				return nil
			}
			for _, q := range questions {
				fmt.Fprintf(out, "\n%s  from %s  (%s)\n", q.ID, q.Agent, waitingFor(q))
				fmt.Fprintf(out, "  %s\n", q.Question)
				if q.Context != "" {
					fmt.Fprintf(out, "  while: %s\n", q.Context)
				}
				if q.Escalation != "" {
					fmt.Fprintf(out, "  the chat says: %s\n", q.Escalation)
				}
				if q.Answer != "" {
					fmt.Fprintf(out, "  answered by the %s: %s\n", q.AnsweredBy, q.Answer)
				}
			}
			if !all {
				fmt.Fprintf(out, "\nAnswer one with: agentbox answer %s <id> \"<answer>\"\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include the ones already answered")
	return cmd
}

func waitingFor(q api.Question) string {
	switch q.Status {
	case "pending":
		return "waiting for the project's chat"
	case "escalated":
		return "waiting for you"
	}
	return q.Status
}

// newAnswerCmd answers a question the project's chat passed to you.
func newAnswerCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "answer <project> <id> <answer>",
		Short:   "Answer a question an agent is waiting on",
		Example: `  agentbox answer pawly q7a2 "Paginate, 20 per page."`,
		Args:    cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			answer := strings.TrimSpace(strings.Join(args[2:], " "))
			if answer == "" {
				return errors.New("say what the agent should do")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			q, err := c.AnswerQuestion(cmd.Context(), args[0], args[1], answer)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Told %s. It was waiting on this and is carrying on.\n", q.Agent)
			return nil
		},
	}
}

// newAutonomyCmd sets how much a project's chat does without being asked.
func newAutonomyCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "autonomy <project> [ask|on]",
		Short: "How much a project's chat does on its own",
		Long: `Shows or sets how much a project's chat does without being asked.

  ask   it does the routine itself — retiring merged agents, answering
        agents, the obvious next agent — and proposes product decisions
        and anything costly (the default)
  on    it also makes the product calls a senior engineer would, and tells
        you what it did

Either way it wakes when one of its agents finishes or asks something, and
either way Stop ends what it is doing.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Name == args[0] {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, autonomyWords(p.Autonomy))
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			p, err := c.SetAutonomy(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, autonomyWords(p.Autonomy))
			return nil
		},
	}
}

func autonomyWords(autonomy string) string {
	if autonomy == "on" {
		return "on — its chat acts on what it decides, product calls included, and tells you"
	}
	return "ask — its chat does the routine itself, and proposes product decisions"
}
