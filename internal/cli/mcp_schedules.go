package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/mcp"
)

// scheduleTools are the lead's scheduled tasks (internal/daemon/schedules.go):
// something to do at a time the user chose, which wakes this same chat with
// the task when it falls due, or creates an agent with it.
func scheduleTools(ctx context.Context, c *api.Client, decode func(json.RawMessage, any) error) []mcp.Tool {
	zone := "the user's timezone"
	if settings, err := c.ProjectSettings(ctx); err == nil && settings.Timezone != "" {
		zone = "the user's timezone, " + settings.Timezone
	}
	when := map[string]any{
		"at": str("when to run it once: a date and time like \"2026-10-19 09:00\" (in " + zone +
			" unless timezone says otherwise), RFC 3339 with an offset, or just \"09:00\" for the next time the clock shows it"),
		"in":       str("instead of at: how long from now, like \"2h\", \"90m\" or \"1d\""),
		"every":    str("to repeat on an interval, like \"6h\" or \"1d\" (5m at least), counted from at or in when given, else from now"),
		"cron":     str("to repeat on a calendar: a five-field cron expression, like \"0 8 * * 1\" for Mondays at 08:00 or \"30 9 * * 1-5\" for weekdays at 09:30, read in timezone"),
		"timezone": str("an IANA timezone such as Europe/Lisbon; leave it out for " + zone),
		"on": choiceOf("instead of a time, run it each time this happens on the project's GitHub repository: "+
			"ci_failed (checks fail on the newest commit of the default branch), review_requested (a review is asked of the user on a pull request), "+
			"issue_labeled (an open issue gets label), pr_opened (a pull request is opened from a branch that isn't an agent's). "+
			"Each event fires once, with its details (links, failing checks, the issue's text) added after the task; what is already there when it is made doesn't fire",
			"ci_failed", "review_requested", "issue_labeled", "pr_opened"),
		"label": str("for on issue_labeled: the label to wait for, like \"bug\""),
	}
	options := map[string]any{
		"title":   str("a short name for the list, like \"Release checks\"; leave it out to use the task's first line"),
		"target":  choiceOf("lead (the default) wakes you with the task in this chat; agent creates an agent with the task as its first message", "lead", "agent"),
		"session": choiceOf("for target agent: fresh (the default) makes a new agent each run; reuse tells the agent the last run made, while it is still there", "fresh", "reuse"),
		"model":   str("for target agent: the model a new agent starts on; leave it out for the project's default"),
		"effort":  str("for target agent: its effort; leave it out for the project's default"),
		"precheck": str("a shell command run in the project's checkout before each run; when it exits non-zero the run is skipped " +
			"(e.g. `gh pr list --search 'is:open' | grep -q .`)"),
		"grace_minutes": map[string]any{"type": "integer", "description": "how late a run missed while AgentBox wasn't running may still run; later than that it is skipped. Leave it out to run it however late."},
	}
	type request struct {
		Title, Task, At, In, Every, Cron, On, Label, Timezone, Target, Session, Model, Effort, Precheck *string
		GraceMinutes                                                                                    *int `json:"grace_minutes"`
		ID                                                                                              string
	}
	toRequest := func(in request) api.ScheduleRequest {
		return api.ScheduleRequest{
			Title: in.Title, Task: in.Task, At: in.At, In: in.In, Every: in.Every, Cron: in.Cron, On: in.On, Label: in.Label, Timezone: in.Timezone,
			Target: in.Target, Session: in.Session, Model: in.Model, Effort: in.Effort, Precheck: in.Precheck, GraceMinutes: in.GraceMinutes,
		}
	}
	return []mcp.Tool{
		{
			Name: "schedule_task",
			Description: "Schedule something for later, when the user asks for it at a time: \"tomorrow at 9 run the " +
				"release checks\", \"every Monday 8am triage new issues\", \"in 2 hours check CI on #384\"; or each time " +
				"something happens on GitHub: \"when CI goes red on main, have an agent fix it\", \"when an issue is labelled bug, " +
				"triage it\". When it falls due, AgentBox wakes you in this chat with the task, as if the user had just asked " +
				"(or, with target agent, creates an agent with it). Give the task in full, for a you who has forgotten this " +
				"conversation; and one of at, in, every, cron or on. Times are in " + zone + ". A run missed while AgentBox wasn't running runs once " +
				"when it is back, marked late. The user sees and changes it in the project's Schedules. Tell them, in " +
				"their words, when it will next run.",
			Schema: object([]string{"task"}, merge(merge(map[string]any{
				"task": str("what to do when it runs, self-contained: what to check or do, and what to report"),
			}, when), options)),
			Run: func(args json.RawMessage) (string, error) {
				var in request
				if err := decode(args, &in); err != nil {
					return "", err
				}
				sc, err := c.LeadAddSchedule(ctx, toRequest(in))
				if err != nil {
					return "", err
				}
				return "Scheduled. " + describeSchedule(sc), nil
			},
		},
		{
			Name:        "list_schedules",
			Description: "This project's scheduled tasks: when each runs next, how its last run went, and its id for edit_schedule, pause_schedule and delete_schedule.",
			Run: func(json.RawMessage) (string, error) {
				list, err := c.LeadSchedules(ctx)
				if err != nil {
					return "", err
				}
				if len(list) == 0 {
					return "This project has no scheduled tasks. schedule_task makes one.", nil
				}
				var b strings.Builder
				for _, sc := range list {
					b.WriteString("- " + describeSchedule(sc) + "\n")
				}
				fmt.Fprintf(&b, "\nNow: %s.", time.Now().Format("Mon 2 Jan 2006 15:04 MST"))
				return b.String(), nil
			},
		},
		{
			Name: "edit_schedule",
			Description: "Change one of this project's scheduled tasks, when the user asks: its task, its title, when it " +
				"runs (one of at, in, every, cron or on replaces the old timing), or what a run does. Fields left out stay as they are.",
			Schema: object([]string{"id"}, merge(merge(map[string]any{
				"id":   str("the schedule's id, from list_schedules"),
				"task": str("what it should do instead"),
			}, when), options)),
			Run: func(args json.RawMessage) (string, error) {
				var in request
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if in.ID == "" {
					return "", errors.New("name the schedule by its id, from list_schedules")
				}
				sc, err := c.LeadUpdateSchedule(ctx, in.ID, toRequest(in))
				if err != nil {
					return "", err
				}
				return "Changed. " + describeSchedule(sc), nil
			},
		},
		{
			Name:        "pause_schedule",
			Description: "Pause one of this project's scheduled tasks, or resume it (paused false). Resumed, it carries on from its next time after now; what it missed while paused doesn't run.",
			Schema: object([]string{"id"}, map[string]any{
				"id":     str("the schedule's id, from list_schedules"),
				"paused": map[string]any{"type": "boolean", "description": "false resumes it; leave it out to pause"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				in := struct {
					ID     string
					Paused *bool
				}{}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				paused := in.Paused == nil || *in.Paused
				sc, err := c.LeadUpdateSchedule(ctx, in.ID, api.ScheduleRequest{Paused: &paused})
				if err != nil {
					return "", err
				}
				return describeSchedule(sc), nil
			},
		},
		{
			Name:        "delete_schedule",
			Description: "Delete one of this project's scheduled tasks, and the history of its runs, when the user asks. Say which it was, by its title.",
			Schema: object([]string{"id"}, map[string]any{
				"id": str("the schedule's id, from list_schedules"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ ID string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if err := c.LeadRemoveSchedule(ctx, in.ID); err != nil {
					return "", err
				}
				return "Deleted.", nil
			},
		},
	}
}

// describeSchedule is one schedule in a line, for the lead and the command
// line.
func describeSchedule(sc api.Schedule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q (id %s): %s", sc.Title, sc.ID, sc.Describe)
	if sc.Target == api.ScheduleTargetAgent {
		b.WriteString(", creating an agent")
		if sc.Session == "reuse" {
			b.WriteString(" (or telling the last one)")
		}
	}
	loc, err := time.LoadLocation(sc.Timezone)
	if err != nil {
		loc = time.Local
	}
	switch {
	case sc.Paused:
		b.WriteString("; paused")
	case sc.Kind == api.ScheduleOnEvent:
		// No next run: Describe said what it waits for.
	case sc.NextRun != nil:
		fmt.Fprintf(&b, "; next run %s", sc.NextRun.In(loc).Format("Mon 2 Jan 2006 15:04 MST"))
	default:
		b.WriteString("; won't run again")
	}
	if sc.Precheck != "" {
		fmt.Fprintf(&b, "; only when `%s` passes", sc.Precheck)
	}
	if r := sc.LastRun; r != nil {
		fmt.Fprintf(&b, "; last ran %s (%s", r.RanAt.In(loc).Format("2 Jan 15:04"), r.Outcome)
		if r.Late {
			b.WriteString(", late")
		}
		if r.Event != "" {
			b.WriteString(", for " + r.Event)
		}
		if r.Error != "" {
			b.WriteString(": " + r.Error)
		}
		b.WriteString(")")
	}
	return b.String()
}
