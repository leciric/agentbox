package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Scheduled tasks: something a project's lead does at a time the user chose,
// once ("tomorrow at 9 run the release checks") or again and again ("every
// Monday at 8 triage new issues"), or each time something happens on the
// project's GitHub repository (an event trigger: "when CI goes red on main,
// have an agent fix it"). Each run wakes the lead with the task, or creates
// an agent with it. The daemon owns them; the app, the command line
// and the lead's schedules tool all go through these routes.

// What a schedule's run does: Schedule.Target.
const (
	ScheduleTargetLead  = "lead"
	ScheduleTargetAgent = "agent"
)

// Schedule kinds: Schedule.Kind.
const (
	ScheduleOnce  = "once"
	ScheduleEvery = "every"
	ScheduleCron  = "cron"
	// ScheduleOnEvent runs each time Schedule.Event happens.
	ScheduleOnEvent = "event"
)

// Events an event trigger runs on: Schedule.Event.
const (
	// ScheduleEventCIFailed is the checks failing on the newest commit of the
	// repository's default branch, once per commit.
	ScheduleEventCIFailed = "ci_failed"
	// ScheduleEventReviewRequested is a review asked of the user on a pull
	// request; asked again after they answered, it is another event.
	ScheduleEventReviewRequested = "review_requested"
	// ScheduleEventIssueLabeled is an open issue getting Schedule.EventLabel.
	ScheduleEventIssueLabeled = "issue_labeled"
	// ScheduleEventPROpened is a pull request opened from a branch that
	// isn't an agent's.
	ScheduleEventPROpened = "pr_opened"
)

// EventSchedule says a project's schedules, or their runs, changed. Its data
// is a ScheduleChange.
const EventSchedule = "schedule"

// ScheduleChange is EventSchedule's data.
type ScheduleChange struct {
	Project string `json:"project"`
}

// Schedule is one scheduled task.
type Schedule struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Title   string `json:"title"`
	// Task is what the lead, or the agent a run creates, is told to do.
	Task string `json:"task"`
	// Kind is ScheduleOnce, ScheduleEvery, ScheduleCron or ScheduleOnEvent.
	// At is when a one-off runs, and when a repeating one started counting
	// from; EveryMinutes the interval; Cron the expression, read in
	// Timezone; Event what an event trigger runs on, and EventLabel the
	// label ScheduleEventIssueLabeled waits for.
	Kind         string     `json:"kind"`
	Event        string     `json:"event,omitempty"`
	EventLabel   string     `json:"eventLabel,omitempty"`
	At           *time.Time `json:"at,omitempty"`
	EveryMinutes int        `json:"everyMinutes,omitempty"`
	Cron         string     `json:"cron,omitempty"`
	Timezone     string     `json:"timezone"`
	// Describe is when it runs, in English, for the command line and the
	// lead; the app says it in its own words.
	Describe string `json:"describe"`
	Target   string `json:"target"`
	// Session, Model and Effort are for a run that creates an agent:
	// Session "reuse" tells the agent the last run made, while it is still
	// there, instead of making another ("" or "fresh"); Model and Effort are
	// what a new one starts on, "" for the project's defaults.
	Session string `json:"session,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	// Precheck is a shell command run in the project's checkout before each
	// run; when it exits non-zero the run is skipped.
	Precheck string `json:"precheck,omitempty"`
	// GraceMinutes is how late a run missed while AgentBox wasn't running
	// may still run; later than that it is skipped. 0 runs it however late.
	GraceMinutes int  `json:"graceMinutes,omitempty"`
	Paused       bool `json:"paused"`
	// NextRun is when it runs next: absent when paused, or when it never
	// runs again, a one-off that ran, and for an event trigger.
	NextRun *time.Time `json:"nextRun,omitempty"`
	// LastRun is its newest run, and Runs how many it has had.
	LastRun   *ScheduleRun `json:"lastRun,omitempty"`
	Runs      int          `json:"runs"`
	CreatedBy string       `json:"createdBy"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
}

// ScheduleRun is one time a schedule ran.
type ScheduleRun struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule"`
	// Title is the schedule's, as it is now.
	Title string `json:"title,omitempty"`
	// Trigger is "schedule", "manual" for Run now, or "event" for an event
	// trigger's run; Event then says what happened, in a line, and EventURL
	// where to see it.
	Trigger  string     `json:"trigger"`
	Event    string     `json:"event,omitempty"`
	EventURL string     `json:"eventUrl,omitempty"`
	DueAt    *time.Time `json:"dueAt,omitempty"`
	RanAt    time.Time  `json:"ranAt"`
	// Late is set when it ran later than it was due, because AgentBox wasn't
	// running then; Missed is how many more times it was due meanwhile, which
	// this one run stands for.
	Late   bool `json:"late,omitempty"`
	Missed int  `json:"missed,omitempty"`
	// Outcome is "sent" (into the lead's chat), "agent" (an agent was
	// created or told), "failed", "skipped" (its precheck failed, or it was
	// missed by more than its grace; Error says which), or "" while it is
	// being delivered.
	Outcome string `json:"outcome"`
	// Agent is the agent it created, once made; Job the job making it;
	// ChatItem the item it arrived as in the lead's chat.
	Agent    string `json:"agent,omitempty"`
	Job      string `json:"job,omitempty"`
	ChatItem string `json:"chatItem,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ScheduleRequest makes a schedule (POST) or changes one (PATCH, where an
// absent field stays as it is).
//
// When it runs is one of: At, a time (RFC 3339, or "2026-10-19 09:00" or
// "09:00" in Timezone); In, a delay from now ("2h", "90m", "1d"); Every, an
// interval ("1h", "1d") counted from At or from now; Cron, a five-field cron
// expression in Timezone; or On, an event (ScheduleEventCIFailed…), with
// Label for ScheduleEventIssueLabeled. Timezone is an IANA name, "" for the
// user's (Settings.Timezone).
type ScheduleRequest struct {
	Title    *string `json:"title,omitempty"`
	Task     *string `json:"task,omitempty"`
	At       *string `json:"at,omitempty"`
	In       *string `json:"in,omitempty"`
	Every    *string `json:"every,omitempty"`
	Cron     *string `json:"cron,omitempty"`
	On       *string `json:"on,omitempty"`
	Label    *string `json:"label,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
	Target   *string `json:"target,omitempty"`
	Session  *string `json:"session,omitempty"`
	Model    *string `json:"model,omitempty"`
	Effort   *string `json:"effort,omitempty"`
	Precheck *string `json:"precheck,omitempty"`
	// GraceMinutes is Schedule.GraceMinutes.
	GraceMinutes *int  `json:"graceMinutes,omitempty"`
	Paused       *bool `json:"paused,omitempty"`
}

// ChatScheduledRun, on a notice in a lead's chat, makes it a scheduled task
// arriving: the notice's Text is the task.
type ChatScheduledRun struct {
	Schedule string `json:"schedule"`
	Run      string `json:"run"`
	Title    string `json:"title"`
	Trigger  string `json:"trigger"`
	// Event is what an event trigger's run was for, and EventURL where to
	// see it.
	Event    string     `json:"event,omitempty"`
	EventURL string     `json:"eventUrl,omitempty"`
	DueAt    *time.Time `json:"dueAt,omitempty"`
	Late     bool       `json:"late,omitempty"`
	Missed   int        `json:"missed,omitempty"`
	Target   string     `json:"target"`
	// Agent is the agent a run for an agent went to, once known.
	Agent string `json:"agent,omitempty"`
}

func schedulesPath(project string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/schedules"
}

// Schedules lists a project's schedules.
func (c *Client) Schedules(ctx context.Context, project string) ([]Schedule, error) {
	var out []Schedule
	return out, c.do(ctx, http.MethodGet, schedulesPath(project), nil, &out)
}

// AddSchedule makes one.
func (c *Client) AddSchedule(ctx context.Context, project string, req ScheduleRequest) (Schedule, error) {
	var out Schedule
	return out, c.do(ctx, http.MethodPost, schedulesPath(project), req, &out)
}

// UpdateSchedule changes one.
func (c *Client) UpdateSchedule(ctx context.Context, project, id string, req ScheduleRequest) (Schedule, error) {
	var out Schedule
	return out, c.do(ctx, http.MethodPatch, schedulesPath(project)+"/"+url.PathEscape(id), req, &out)
}

// RemoveSchedule deletes one, and its runs.
func (c *Client) RemoveSchedule(ctx context.Context, project, id string) error {
	return c.do(ctx, http.MethodDelete, schedulesPath(project)+"/"+url.PathEscape(id), nil, nil)
}

// RunSchedule runs one now, whenever it is next due.
func (c *Client) RunSchedule(ctx context.Context, project, id string) (ScheduleRun, error) {
	var out ScheduleRun
	return out, c.do(ctx, http.MethodPost, schedulesPath(project)+"/"+url.PathEscape(id)+"/run", nil, &out)
}

// ScheduleRuns lists a project's runs, newest first; one schedule's when
// schedule isn't empty.
func (c *Client) ScheduleRuns(ctx context.Context, project, schedule string) ([]ScheduleRun, error) {
	var out []ScheduleRun
	path := schedulesPath(project) + "/runs"
	if schedule != "" {
		path += "?schedule=" + url.QueryEscape(schedule)
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// The lead's own: the same routes on its socket, which names the project.

func (c *Client) LeadSchedules(ctx context.Context) ([]Schedule, error) {
	var out []Schedule
	return out, c.do(ctx, http.MethodGet, c.leadPath("/schedules"), nil, &out)
}

func (c *Client) LeadAddSchedule(ctx context.Context, req ScheduleRequest) (Schedule, error) {
	var out Schedule
	return out, c.do(ctx, http.MethodPost, c.leadPath("/schedules"), req, &out)
}

func (c *Client) LeadUpdateSchedule(ctx context.Context, id string, req ScheduleRequest) (Schedule, error) {
	var out Schedule
	return out, c.do(ctx, http.MethodPatch, c.leadPath("/schedules/"+url.PathEscape(id)), req, &out)
}

func (c *Client) LeadRemoveSchedule(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, c.leadPath("/schedules/"+url.PathEscape(id)), nil, nil)
}
