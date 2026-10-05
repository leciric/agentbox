package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Delegation is a task an agent handed to a sub-agent of its own: a real
// agent, on a branch from its parent's, that is retired once it is done
// unless the parent asked to keep it.
type Delegation struct {
	ID string `json:"id"`
	// Agent is the sub-agent, by name; Parent the agent that delegated.
	Agent  string `json:"agent"`
	Parent string `json:"parent"`
	Title  string `json:"title"`
	Task   string `json:"task"`
	AI     string `json:"ai"`
	Branch string `json:"branch,omitempty"`
	Keep   bool   `json:"keep"`
	// Status is running, done, failed or cancelled.
	Status string `json:"status"`
	// AgentState is the sub-agent's own state (Agent.State): queued while it
	// waits for memory, running, stopped once retired, and "gone" once it
	// was destroyed or never made.
	AgentState string `json:"agentState"`
	// Result is what it came back with: its last message once it finished,
	// or why it failed or was cancelled.
	Result string `json:"result,omitempty"`
	// Report is the report it filed, its newest, if it filed one.
	Report     *AgentReport `json:"report,omitempty"`
	CreatedAt  time.Time    `json:"createdAt"`
	FinishedAt *time.Time   `json:"finishedAt,omitempty"`
}

// DelegateRequest starts a sub-agent on a task (POST /v1/self/delegations).
type DelegateRequest struct {
	Title string `json:"title"`
	Task  string `json:"task"`
	// AI is claude (the default), codex or opencode.
	AI     string  `json:"ai,omitempty"`
	Model  *string `json:"model,omitempty"`
	Effort *string `json:"effort,omitempty"`
	Size   string  `json:"size,omitempty"`
	Branch string  `json:"branch,omitempty"`
	// Keep leaves the sub-agent as it is once it is done, rather than
	// retiring it.
	Keep bool `json:"keep,omitempty"`
}

// Delegation statuses.
const (
	DelegationRunning   = "running"
	DelegationDone      = "done"
	DelegationFailed    = "failed"
	DelegationCancelled = "cancelled"
)

// Delegate starts a sub-agent on a task, from inside an agent.
func (c *Client) Delegate(ctx context.Context, req DelegateRequest) (Delegation, error) {
	var out Delegation
	return out, c.do(ctx, http.MethodPost, "/v1/self/delegations", req, &out)
}

// Delegations lists what this agent delegated, newest first.
func (c *Client) Delegations(ctx context.Context) ([]Delegation, error) {
	var out []Delegation
	return out, c.do(ctx, http.MethodGet, "/v1/self/delegations", nil, &out)
}

// Delegation is one of this agent's delegations. A wait above zero waits up
// to that long for it to end, and answers as it is then either way.
func (c *Client) Delegation(ctx context.Context, id string, wait time.Duration) (Delegation, error) {
	var out Delegation
	path := "/v1/self/delegations/" + url.PathEscape(id)
	if wait > 0 {
		path += fmt.Sprintf("?wait=%d", int(wait.Seconds()))
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// CancelDelegation stops a sub-agent's work and retires it.
func (c *Client) CancelDelegation(ctx context.Context, id string) (Delegation, error) {
	var out Delegation
	return out, c.do(ctx, http.MethodPost, "/v1/self/delegations/"+url.PathEscape(id)+"/cancel", nil, &out)
}

// Schedule is a project's scheduled task: an agent run on a cron schedule,
// like a nightly build, a weekly dependency bump or a daily report.
type Schedule struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Name    string `json:"name"`
	// Cron is when it runs: five fields (minute hour day-of-month month
	// day-of-week), or @hourly, @daily, @weekly, @monthly, in the VM's time
	// zone unless it starts with CRON_TZ=.
	Cron string `json:"cron"`
	// When is Cron said in words, like "every day at 03:00".
	When   string `json:"when"`
	Task   string `json:"task"`
	AI     string `json:"ai"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Size   string `json:"size,omitempty"`
	// Outcome is what its result goes to: "pr", a pull request, or
	// "report", the project's chat.
	Outcome   string     `json:"outcome"`
	Paused    bool       `json:"paused"`
	CreatedAt time.Time  `json:"createdAt"`
	NextRun   *time.Time `json:"nextRun,omitempty"`
	LastRun   *time.Time `json:"lastRun,omitempty"`
	// Running is its run still going, if one is; Last its newest run.
	Running *ScheduleRun `json:"running,omitempty"`
	Last    *ScheduleRun `json:"last,omitempty"`
}

// ScheduleRun is one run of a scheduled task.
type ScheduleRun struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule"`
	// Agent is the agent made for it, once it has a name.
	Agent string `json:"agent,omitempty"`
	// Trigger is schedule, catch-up (it fell due while AgentBox wasn't
	// running) or manual.
	Trigger string `json:"trigger"`
	// Status is running, done, failed or skipped (it fell due while the
	// last run was still going).
	Status     string     `json:"status"`
	Result     string     `json:"result,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// AddScheduleRequest adds a scheduled task to a project.
type AddScheduleRequest struct {
	Name    string `json:"name"`
	Cron    string `json:"cron"`
	Task    string `json:"task"`
	AI      string `json:"ai,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Size    string `json:"size,omitempty"`
	Outcome string `json:"outcome,omitempty"` // pr by default
	Paused  bool   `json:"paused,omitempty"`
}

// UpdateScheduleRequest changes what is set of a scheduled task. Pausing it
// stops it falling due; resuming it makes it next due at its next time from
// now, not at the times it missed while paused.
type UpdateScheduleRequest struct {
	Name    *string `json:"name,omitempty"`
	Cron    *string `json:"cron,omitempty"`
	Task    *string `json:"task,omitempty"`
	AI      *string `json:"ai,omitempty"`
	Model   *string `json:"model,omitempty"`
	Effort  *string `json:"effort,omitempty"`
	Size    *string `json:"size,omitempty"`
	Outcome *string `json:"outcome,omitempty"`
	Paused  *bool   `json:"paused,omitempty"`
}

// What a scheduled task's result goes to.
const (
	ScheduleOutcomePR     = "pr"
	ScheduleOutcomeReport = "report"
)

func schedulesPath(project string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/schedules"
}

// Schedules lists a project's scheduled tasks.
func (c *Client) Schedules(ctx context.Context, project string) ([]Schedule, error) {
	var out []Schedule
	return out, c.do(ctx, http.MethodGet, schedulesPath(project), nil, &out)
}

// AddSchedule adds a scheduled task to a project.
func (c *Client) AddSchedule(ctx context.Context, project string, req AddScheduleRequest) (Schedule, error) {
	var out Schedule
	return out, c.do(ctx, http.MethodPost, schedulesPath(project), req, &out)
}

// UpdateSchedule changes a scheduled task: pausing and resuming it among others.
func (c *Client) UpdateSchedule(ctx context.Context, project, id string, req UpdateScheduleRequest) (Schedule, error) {
	var out Schedule
	return out, c.do(ctx, http.MethodPatch, schedulesPath(project)+"/"+url.PathEscape(id), req, &out)
}

// RemoveSchedule deletes a scheduled task. A run still going is left to finish.
func (c *Client) RemoveSchedule(ctx context.Context, project, id string) error {
	return c.do(ctx, http.MethodDelete, schedulesPath(project)+"/"+url.PathEscape(id), nil, nil)
}

// RunSchedule starts a run of a scheduled task now, refused while one is
// still going.
func (c *Client) RunSchedule(ctx context.Context, project, id string) (ScheduleRun, error) {
	var out ScheduleRun
	return out, c.do(ctx, http.MethodPost, schedulesPath(project)+"/"+url.PathEscape(id)+"/run", nil, &out)
}

// ScheduleRuns lists a scheduled task's runs, newest first.
func (c *Client) ScheduleRuns(ctx context.Context, project, id string) ([]ScheduleRun, error) {
	var out []ScheduleRun
	return out, c.do(ctx, http.MethodGet, schedulesPath(project)+"/"+url.PathEscape(id)+"/runs", nil, &out)
}
