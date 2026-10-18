package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

func newScheduleCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "schedule",
		Aliases: []string{"schedules"},
		Short:   "Tasks a project's chat runs at a time you choose",
		Long: `A scheduled task wakes a project's chat with the task when it falls due — once,
every so often, or on a cron expression — or creates an agent with it. A run
missed while AgentBox wasn't running runs once when it is back, marked late.
The project's chat makes them too, when you ask it to do something later.`,
	}
	cmd.AddCommand(newScheduleListCmd(a), newScheduleAddCmd(a), newScheduleRemoveCmd(a),
		newSchedulePauseCmd(a, true), newSchedulePauseCmd(a, false), newScheduleRunCmd(a))
	return cmd
}

func newScheduleListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "list <project>",
		Aliases: []string{"ls"},
		Short:   "List a project's scheduled tasks",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			list, err := c.Schedules(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(list) == 0 {
				_, _ = fmt.Fprintf(out, "%s has no scheduled tasks. Add one with: agentbox schedule add %s \"<task>\" --at 09:00\n", args[0], args[0])
				return nil
			}
			for _, sc := range list {
				_, _ = fmt.Fprintln(out, describeSchedule(sc))
			}
			return nil
		},
	}
}

func newScheduleAddCmd(a *app) *cobra.Command {
	var at, in, every, cron, timezone, title, precheck, session string
	var agent bool
	var grace int
	cmd := &cobra.Command{
		Use:   "add <project> <task>",
		Short: "Schedule a task for a project's chat",
		Long: `Give the task, and when: --at a time ("2026-10-19 09:00", or "09:00" for the
next time the clock shows it), --in a while ("2h"), --every an interval ("1d",
from --at or now), or --cron an expression ("0 8 * * 1" is Mondays at 08:00).
Times are in your timezone, as the app last reported it, unless --tz says.

    agentbox schedule add web "Run the release checks" --at "2026-10-19 09:00"
    agentbox schedule add web "Triage new issues" --cron "0 8 * * 1" --agent`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			req := api.ScheduleRequest{Task: &args[1]}
			for _, f := range []struct {
				name string
				into **string
				val  *string
			}{{"at", &req.At, &at}, {"in", &req.In, &in}, {"every", &req.Every, &every}, {"cron", &req.Cron, &cron},
				{"tz", &req.Timezone, &timezone}, {"title", &req.Title, &title}, {"precheck", &req.Precheck, &precheck}, {"session", &req.Session, &session}} {
				if cmd.Flags().Changed(f.name) {
					*f.into = f.val
				}
			}
			if agent {
				target := api.ScheduleTargetAgent
				req.Target = &target
			}
			if cmd.Flags().Changed("grace") {
				req.GraceMinutes = &grace
			}
			sc, err := c.AddSchedule(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Scheduled "+describeSchedule(sc))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&at, "at", "", "run once at this time")
	f.StringVar(&in, "in", "", "run once this long from now, like 2h")
	f.StringVar(&every, "every", "", "repeat on this interval, like 6h or 1d")
	f.StringVar(&cron, "cron", "", "repeat on this cron expression")
	f.StringVar(&timezone, "tz", "", "the IANA timezone its times are in, like Europe/Lisbon")
	f.StringVar(&title, "title", "", "a short name for it (default: the task's first line)")
	f.BoolVar(&agent, "agent", false, "create an agent with the task, instead of telling the chat")
	f.StringVar(&session, "session", "", "with --agent: reuse the last run's agent (reuse), or make a new one each time (fresh)")
	f.StringVar(&precheck, "precheck", "", "a shell command that must pass for a run to go ahead")
	f.IntVar(&grace, "grace", 0, "minutes a missed run may be late and still run (default: however late)")
	return cmd
}

func newScheduleRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <project> <id>",
		Aliases: []string{"remove"},
		Short:   "Delete a scheduled task and its history",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveSchedule(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s.\n", args[1])
			return nil
		},
	}
}

func newSchedulePauseCmd(a *app, pause bool) *cobra.Command {
	use, short := "pause", "Pause a scheduled task"
	if !pause {
		use, short = "resume", "Resume a paused scheduled task, from its next time after now"
	}
	return &cobra.Command{
		Use:   use + " <project> <id>",
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			sc, err := c.UpdateSchedule(cmd.Context(), args[0], args[1], api.ScheduleRequest{Paused: &pause})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), describeSchedule(sc))
			return nil
		},
	}
}

func newScheduleRunCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "run <project> <id>",
		Short: "Run a scheduled task now, leaving its next time as it is",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			run, err := c.RunSchedule(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			msg := "Ran it: " + run.Outcome
			if run.Error != "" {
				msg += " (" + strings.TrimSpace(run.Error) + ")"
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
}
