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

// scheduleTool is the lead's scheduled tasks (internal/daemon/schedules.go):
// something to do at a time the user chose, which wakes this same chat with
// the task when it falls due, or creates an agent with it.
func scheduleTool(ctx context.Context, c *api.Client) mcp.Tool {
	zone := "the user's timezone"
	if settings, err := c.ProjectSettings(ctx); err == nil && settings.Timezone != "" {
		zone = "the user's timezone, " + settings.Timezone
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
	read := func(raw json.RawMessage) (request, error) {
		var in request
		if err := decodeArgs(raw, &in); err != nil {
			return in, err
		}
		if in.ID == "" {
			return in, errors.New("name the schedule by its id, from list")
		}
		return in, nil
	}
	pause := func(paused bool) func(json.RawMessage) (string, error) {
		return func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			sc, err := c.LeadUpdateSchedule(ctx, in.ID, api.ScheduleRequest{Paused: &paused})
			if err != nil {
				return "", err
			}
			return describeSchedule(sc), nil
		}
	}
	return actionTool("schedules",
		"Tasks for later, when the user asks for one at a time (\"tomorrow at 9 run the release checks\", \"every Monday 8am "+
			"triage new issues\") or on a GitHub event (\"when CI goes red on main, have an agent fix it\"). When one falls due AgentBox wakes you in this chat with its task, as if the user had just asked "+
			"(or, with target agent, creates an agent with it); a run missed while AgentBox was off runs once when it is back, marked "+
			"late. Tell the user, in their words, when it will next run.",
		"create: task, with one of at, in, every, cron or on. list: each one, its next run, last run and id. edit: id's fields that "+
			"are given (one of at, in, every, cron or on replaces its timing). pause, resume: id; resumed, it carries on from its next "+
			"time, and what it missed doesn't run. delete: id and its runs' history, when the user asks",
		map[string]any{
			"id":       str("edit, pause, resume, delete: the schedule's id"),
			"task":     str("what to do when it runs, self-contained for a you who has forgotten this conversation: what to check or do, and what to report"),
			"at":       str("once at a time like \"2026-10-19 09:00\" (in " + zone + " unless timezone), RFC 3339, or \"09:00\" for the next time the clock shows it"),
			"in":       str("once after a while, like \"2h\""),
			"every":    str("repeat on an interval, like \"6h\" (5m at least), from at or in when given, else from now"),
			"cron":     str("repeat on a calendar: five-field cron, like \"0 8 * * 1\" for Mondays at 08:00, read in timezone"),
			"timezone": str("an IANA timezone like Europe/Lisbon; " + zone + " by default"),
			"on": choiceOf("instead of a time, each time this happens on GitHub: CI fails on the default branch, a review is asked "+
				"of the user, an open issue gets label, a pull request opens from a branch that isn't an agent's. Its details follow "+
				"the task", "ci_failed", "review_requested", "issue_labeled", "pr_opened"),
			"label":   str("for on issue_labeled: the label, like \"bug\""),
			"title":   str("a short name; the task's first line by default"),
			"target":  choiceOf("lead (the default) wakes you with the task; agent creates an agent with it as its first message", "lead", "agent"),
			"session": choiceOf("target agent: fresh (the default) makes a new agent each run; reuse tells the last run's agent, while it is there", "fresh", "reuse"),
			"model":   str("target agent: its model; the project's default when left out"),
			"effort":  str("target agent: its effort; the project's default when left out"),
			"precheck": str("a shell command run in the project's checkout before each run, which skips the run when it exits non-zero " +
				"(e.g. `gh pr list --search 'is:open' | grep -q .`)"),
			"grace_minutes": map[string]any{"type": "integer", "description": "how late a missed run may still run; however late when left out"},
		},
		action{"create", func(raw json.RawMessage) (string, error) {
			var in request
			if err := decodeArgs(raw, &in); err != nil {
				return "", err
			}
			sc, err := c.LeadAddSchedule(ctx, toRequest(in))
			if err != nil {
				return "", err
			}
			return "Scheduled. " + describeSchedule(sc), nil
		}},
		action{"list", func(json.RawMessage) (string, error) {
			list, err := c.LeadSchedules(ctx)
			if err != nil {
				return "", err
			}
			if len(list) == 0 {
				return "This project has no scheduled tasks. create makes one.", nil
			}
			var b strings.Builder
			for _, sc := range list {
				b.WriteString("- " + describeSchedule(sc) + "\n")
			}
			fmt.Fprintf(&b, "\nNow: %s.", time.Now().Format("Mon 2 Jan 2006 15:04 MST"))
			return b.String(), nil
		}},
		action{"edit", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			sc, err := c.LeadUpdateSchedule(ctx, in.ID, toRequest(in))
			if err != nil {
				return "", err
			}
			return "Changed. " + describeSchedule(sc), nil
		}},
		action{"pause", pause(true)},
		action{"resume", pause(false)},
		action{"delete", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if err := c.LeadRemoveSchedule(ctx, in.ID); err != nil {
				return "", err
			}
			return "Deleted.", nil
		}},
	)
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
