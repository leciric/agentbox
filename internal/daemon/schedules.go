package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/schedule"
	"agentbox/internal/state"
)

// Scheduled tasks: something a project's lead is to do at a time the user
// chose — "tomorrow at 9 run the release checks", "every Monday at 8 triage
// new issues", "in 2 hours check CI on #384". The lead makes them with
// schedules (or the user does, in the app's Schedules or with `agentbox
// schedule add`), the daemon keeps them in state.db and runs them: a run
// wakes the lead with the task, the way an agent's finish does (tellLead),
// shown in its chat as a chip (chat.ShowScheduledRun); or, when the
// schedule says so, creates an agent with the task as its first message, or
// tells the agent the last run made.
//
// Each run is recorded, and the schedule moved on to its next time, in one
// transaction before it is delivered (state.ClaimScheduleRun), so a daemon
// that stops halfway never runs it twice. A run that fell due while the
// daemon or the VM wasn't running runs once when it is back, however many
// times it was due meanwhile, marked late with how many it stands for —
// unless the schedule has a grace period and the run is later than that,
// when it is skipped and the skip recorded. A precheck, a shell command run
// in the lead's checkout, can skip a run too, when it fails.

// lateAfter is how far past its time a run has to be to be called late: the
// runner wakes on time, so anything later is the daemon having been away.
const lateAfter = 2 * time.Minute

// maxScheduleIdle is the longest the runner sleeps without looking again, so
// a clock that jumped (a VM resumed from suspend) is noticed soon.
const maxScheduleIdle = time.Minute

// precheckTimeout is how long a precheck may take before it counts as failed.
const precheckTimeout = 2 * time.Minute

// maxScheduleTask is the longest task a schedule may carry, the same as a
// message between projects' leads.
const maxScheduleTask = 8000

// kickSchedules wakes the runner, for a schedule that was just made or
// changed and may now be due sooner than the runner is sleeping for.
func (s *Server) kickSchedules() {
	select {
	case s.scheduleKick <- struct{}{}:
	default:
	}
}

// runSchedules is the runner: it runs what is due, then sleeps until the next
// schedule is, or a minute, or until a schedule changes. Its first pass, as
// the daemon starts, is the one that catches up on what was missed.
func (s *Server) runSchedules(ctx context.Context) {
	for {
		s.fireDueSchedules(ctx, time.Now())
		wait := maxScheduleIdle
		if next, ok, err := s.store.NextScheduleTime(ctx); err == nil && ok {
			wait = min(wait, max(time.Until(next), 0)+50*time.Millisecond)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-s.scheduleKick:
			timer.Stop()
		}
	}
}

// fireDueSchedules runs every schedule due by now, oldest first.
func (s *Server) fireDueSchedules(ctx context.Context, now time.Time) {
	due, err := s.store.DueSchedules(ctx, now)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("scheduled tasks: %v", err)
		}
		return
	}
	for _, sc := range due {
		s.fireSchedule(ctx, sc, now)
	}
}

// fireSchedule claims and delivers one due run of sc.
func (s *Server) fireSchedule(ctx context.Context, sc state.Schedule, now time.Time) {
	spec, err := s.scheduleSpec(ctx, sc)
	if err != nil {
		s.logf("scheduled task %s of %s: %v", sc.ID, sc.Project, err)
		return
	}
	next, ok := spec.Next(now)
	if !ok {
		next = time.Time{}
	}
	run := state.ScheduleRun{
		ID: newID(), Schedule: sc.ID, Project: sc.Project, Trigger: state.ScheduleTriggerTime,
		DueAt: sc.NextRun, RanAt: now, Late: now.Sub(sc.NextRun) > lateAfter,
	}
	if run.Late {
		// How many more times it fell due while nobody was there to run it.
		for t, ok := spec.Next(sc.NextRun); ok && !t.After(now) && run.Missed < 10000; t, ok = spec.Next(t) {
			run.Missed++
		}
	}
	if sc.Grace > 0 && now.Sub(sc.NextRun) > sc.Grace {
		run.Outcome = state.ScheduleRunSkipped
		run.Error = fmt.Sprintf("missed by %s, more than its grace of %s", now.Sub(sc.NextRun).Round(time.Minute), schedule.FormatEvery(sc.Grace))
	}
	if err := s.store.ClaimScheduleRun(ctx, run, sc.NextRun, next); err != nil {
		if !errors.Is(err, state.ErrScheduleMoved) {
			s.logf("scheduled task %s of %s: %v", sc.ID, sc.Project, err)
		}
		return
	}
	if run.Outcome == state.ScheduleRunSkipped {
		s.logf("scheduled task %q of %s skipped: %s", sc.Title, sc.Project, run.Error)
		s.scheduleRan(ctx, sc, run)
		return
	}
	s.deliverSchedule(ctx, sc, run)
}

// runScheduleNow is Run now, and Rerun in a schedule's history: one run
// straight away, leaving its next time as it was.
func (s *Server) runScheduleNow(ctx context.Context, sc state.Schedule) (state.ScheduleRun, error) {
	run := state.ScheduleRun{ID: newID(), Schedule: sc.ID, Project: sc.Project, Trigger: state.ScheduleTriggerManual, RanAt: time.Now()}
	if err := s.store.ClaimScheduleRun(ctx, run, time.Time{}, time.Time{}); err != nil {
		return run, err
	}
	return s.deliverSchedule(ctx, sc, run), nil
}

// deliverSchedule does what a claimed run is for, and records how it went.
func (s *Server) deliverSchedule(ctx context.Context, sc state.Schedule, run state.ScheduleRun) state.ScheduleRun {
	if strings.TrimSpace(sc.Precheck) != "" {
		if out, err := s.precheck(ctx, sc); err != nil {
			run.Outcome, run.Error = state.ScheduleRunSkipped, fmt.Sprintf("precheck failed: %v%s", err, tailOf(out))
			s.logf("scheduled task %q of %s skipped: %s", sc.Title, sc.Project, run.Error)
			s.scheduleRan(ctx, sc, run)
			return run
		}
	}
	var err error
	if sc.Target == state.ScheduleTargetAgent {
		err = s.scheduleToAgent(ctx, sc, &run)
	} else {
		err = s.scheduleToLead(ctx, sc, &run)
	}
	if err != nil {
		run.Outcome, run.Error = state.ScheduleRunFailed, err.Error()
		s.logf("scheduled task %q of %s: %v", sc.Title, sc.Project, err)
	}
	s.scheduleRan(ctx, sc, run)
	return run
}

// scheduleRan stores how a run went, tells the app, and keeps it in the
// project's memory.
func (s *Server) scheduleRan(ctx context.Context, sc state.Schedule, run state.ScheduleRun) {
	if err := s.store.FinishScheduleRun(ctx, run); err != nil {
		s.logf("scheduled task %s: recording its run: %v", sc.ID, err)
	}
	s.events.publish(api.EventSchedule, api.ScheduleChange{Project: sc.Project})
	s.captureEvent(ctx, sc.Project, run.Agent, "schedule_ran", map[string]any{
		"schedule": sc.ID, "title": sc.Title, "trigger": run.Trigger, "late": run.Late, "missed": run.Missed,
		"outcome": run.Outcome, "agent": run.Agent, "error": run.Error,
	}, "")
}

// precheck runs a schedule's precheck in its lead's checkout, and returns
// what it printed.
func (s *Server) precheck(ctx context.Context, sc state.Schedule) (string, error) {
	lead, err := s.manager(nil).EnsureLead(ctx, sc.Project)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, precheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", sc.Precheck)
	cmd.Dir = lead.Worktree
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("it took longer than %s", precheckTimeout)
	}
	return string(out), err
}

// tailOf is the last line of a command's output, for a skipped run's reason.
func tailOf(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return ""
	}
	if len(last) > 200 {
		last = last[:200] + "…"
	}
	return ": " + last
}

func chatRun(sc state.Schedule, run state.ScheduleRun) api.ChatScheduledRun {
	out := api.ChatScheduledRun{
		Schedule: sc.ID, Run: run.ID, Title: sc.Title, Trigger: run.Trigger,
		Late: run.Late, Missed: run.Missed, Target: sc.Target, Agent: run.Agent, Event: run.Event, EventURL: run.EventURL,
	}
	if !run.DueAt.IsZero() {
		due := run.DueAt
		out.DueAt = &due
	}
	return out
}

// scheduleToLead wakes the project's lead with the task.
func (s *Server) scheduleToLead(ctx context.Context, sc state.Schedule, run *state.ScheduleRun) error {
	lead, err := s.manager(nil).EnsureLead(ctx, sc.Project)
	if err != nil {
		return err
	}
	item, err := s.chat.ShowScheduledRun(lead, sc.Task, chatRun(sc, *run))
	if err != nil {
		return err
	}
	run.ChatItem, run.Outcome = item.ID, state.ScheduleRunSent
	s.tellLead(ctx, sc.Project, scheduleNotice(sc, *run, s.describeSchedule(ctx, sc)), true)
	return nil
}

// scheduleToAgent gives the task to an agent: the last run's, when the
// schedule reuses it and it is still there, or a new one.
func (s *Server) scheduleToAgent(ctx context.Context, sc state.Schedule, run *state.ScheduleRun) error {
	if sc.Session == state.ScheduleSessionReuse {
		if a, ok := s.lastScheduleAgent(ctx, sc); ok {
			s.leadAsked(a)
			if _, err := s.tellAgent(ctx, a, sc.Task); err != nil {
				return err
			}
			run.Agent, run.Outcome = a.Name, state.ScheduleRunAgent
			s.showAgentRun(ctx, sc, run)
			return nil
		}
	}
	req := api.CreateAgentRequest{Project: sc.Project, Title: sc.Title, Task: sc.Task}
	if sc.Model != "" {
		req.Model = &sc.Model
	}
	if sc.Effort != "" {
		req.Effort = &sc.Effort
	}
	job, err := s.createAgentJob(ctx, req, true)
	if err != nil {
		return err
	}
	run.Job, run.Outcome = job.ID, state.ScheduleRunAgent
	s.showAgentRun(ctx, sc, run)
	go s.nameScheduleAgent(s.background(), sc.Project, job.ID)
	return nil
}

// showAgentRun puts a run that went to an agent in the lead's chat, and lets
// the lead know without starting a turn: the agent's finish will.
func (s *Server) showAgentRun(ctx context.Context, sc state.Schedule, run *state.ScheduleRun) {
	lead, err := s.manager(nil).EnsureLead(ctx, sc.Project)
	if err != nil {
		return
	}
	if item, err := s.chat.ShowScheduledRun(lead, sc.Task, chatRun(sc, *run)); err == nil {
		run.ChatItem = item.ID
	}
	s.tellLead(ctx, sc.Project, scheduleNotice(sc, *run, s.describeSchedule(ctx, sc)), false)
}

// lastScheduleAgent is the agent the schedule's last run went to, if it is
// still there to be told.
func (s *Server) lastScheduleAgent(ctx context.Context, sc state.Schedule) (state.Agent, bool) {
	runs, err := s.store.ScheduleRuns(ctx, sc.Project, sc.ID, 20)
	if err != nil {
		return state.Agent{}, false
	}
	for _, r := range runs {
		if r.Agent == "" {
			continue
		}
		a, err := s.store.Agent(ctx, sc.Project, r.Agent)
		if err != nil || a.IsLead() || a.Status == state.AgentDestroying {
			return state.Agent{}, false
		}
		return a, true
	}
	return state.Agent{}, false
}

// nameScheduleAgent waits for the job creating a run's agent, and records
// which agent it made, for the run's link.
func (s *Server) nameScheduleAgent(ctx context.Context, project, id string) {
	j, ok := s.jobs.get(id)
	if !ok {
		return
	}
	info, err := j.follow(ctx, 0, func(string) error { return nil })
	if err != nil || info.Status != api.JobSucceeded {
		return
	}
	var made struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(info.Result, &made) != nil || made.Name == "" {
		return
	}
	if err := s.store.SetScheduleRunAgent(ctx, id, made.Name); err == nil {
		s.events.publish(api.EventSchedule, api.ScheduleChange{Project: project})
	}
}

// scheduleNotice is what the lead reads when one of its scheduled tasks runs.
func scheduleNotice(sc state.Schedule, run state.ScheduleRun, when string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Scheduled task %q (schedule %s, %s) ", sc.Title, sc.ID, when)
	switch {
	case run.Trigger == state.ScheduleTriggerManual:
		b.WriteString("was run by hand just now.")
	case run.Trigger == state.ScheduleTriggerEvent:
		fmt.Fprintf(&b, "fired: %s.", run.Event)
	case run.Late:
		fmt.Fprintf(&b, "runs now, late: it was due at %s, while AgentBox wasn't running.", run.DueAt.Format(time.RFC3339))
		if run.Missed > 0 {
			fmt.Fprintf(&b, " It was due %d more time(s) since; this one run stands for them all.", run.Missed)
		}
	default:
		b.WriteString("is due now.")
	}
	if sc.Target == state.ScheduleTargetAgent {
		if run.Agent != "" {
			fmt.Fprintf(&b, " Its task went to %s, the agent its last run made; you hear when it finishes.", run.Agent)
		} else {
			fmt.Fprintf(&b, " An agent is being created for it (job %s), with the task below as its first message; you hear when it finishes. Nothing to do now.", run.Job)
		}
		b.WriteString("\n\nThe task:\n\n" + sc.Task)
		return b.String()
	}
	if run.Trigger == state.ScheduleTriggerEvent {
		b.WriteString(" The user set it up for you to do when this happens, as if they were asking you now:\n\n" + sc.Task +
			"\n\nDo it now. If it no longer makes sense (dealt with already), say so briefly instead. " +
			"edit_schedule, pause_schedule and delete_schedule change it when the user asks.")
		return b.String()
	}
	b.WriteString(" The user set it up for you to do at this time, as if they were asking you now:\n\n" + sc.Task +
		"\n\nDo it now. If it no longer makes sense (done already, or overtaken), say so briefly instead. " +
		"schedules (edit, pause or delete) changes it when the user asks.")
	return b.String()
}

// userLocation is where times are read when a schedule names no timezone:
// the app's, which it reports (Settings.Timezone), else the daemon's.
func (s *Server) userLocation(ctx context.Context) *time.Location {
	name, _ := s.store.Setting(ctx, state.SettingTimezone)
	loc, err := schedule.LoadLocation(name, time.Local)
	if err != nil {
		return time.Local
	}
	return loc
}

func (s *Server) scheduleSpec(ctx context.Context, sc state.Schedule) (schedule.Spec, error) {
	loc, err := schedule.LoadLocation(sc.Timezone, s.userLocation(ctx))
	if err != nil {
		return schedule.Spec{}, err
	}
	return schedule.Spec{Kind: sc.Kind, At: sc.At, Every: sc.Every, Cron: sc.Cron, Location: loc, Event: sc.Event, Label: sc.EventLabel}, nil
}

func (s *Server) describeSchedule(ctx context.Context, sc state.Schedule) string {
	spec, err := s.scheduleSpec(ctx, sc)
	if err != nil {
		return sc.Kind
	}
	return spec.Describe()
}

// applyScheduleRequest changes sc as req asks, and works out when it runs
// next. Creating, it needs a task and a time.
func (s *Server) applyScheduleRequest(ctx context.Context, sc *state.Schedule, req api.ScheduleRequest, now time.Time, creating bool) error {
	text := func(p *string) string { return strings.TrimSpace(*p) }
	if req.Task != nil {
		sc.Task = text(req.Task)
	}
	if sc.Task == "" {
		return errors.New("a scheduled task needs a task: what to do when it runs")
	}
	if len(sc.Task) > maxScheduleTask {
		return fmt.Errorf("a scheduled task is at most %d characters", maxScheduleTask)
	}
	if req.Title != nil {
		sc.Title = text(req.Title)
	}
	if sc.Title == "" {
		sc.Title = titleFrom(sc.Task)
	}
	if req.Target != nil {
		switch t := text(req.Target); t {
		case "", state.ScheduleTargetLead:
			sc.Target = state.ScheduleTargetLead
		case state.ScheduleTargetAgent:
			sc.Target = t
		default:
			return fmt.Errorf("a schedule's run goes to the lead or to an agent, not %q", t)
		}
	}
	if sc.Target == "" {
		sc.Target = state.ScheduleTargetLead
	}
	if req.Session != nil {
		switch v := text(req.Session); v {
		case "", "fresh":
			sc.Session = ""
		case state.ScheduleSessionReuse:
			sc.Session = v
		default:
			return fmt.Errorf("session is fresh or reuse, not %q", v)
		}
	}
	if req.Model != nil {
		sc.Model = text(req.Model)
	}
	if req.Effort != nil {
		sc.Effort = text(req.Effort)
	}
	if req.Precheck != nil {
		sc.Precheck = text(req.Precheck)
	}
	if req.GraceMinutes != nil {
		if *req.GraceMinutes < 0 {
			return errors.New("a grace period can't be negative")
		}
		sc.Grace = time.Duration(*req.GraceMinutes) * time.Minute
	}
	timing := false
	if req.Timezone != nil {
		name := text(req.Timezone)
		if _, err := schedule.LoadLocation(name, time.Local); err != nil {
			return err
		}
		sc.Timezone, timing = name, true
	}
	if sc.Timezone == "" {
		// Kept as it was when it was made, so moving the app to another
		// timezone doesn't move the schedules already set.
		if loc := s.userLocation(ctx); loc != time.Local {
			sc.Timezone = loc.String()
		}
	}
	loc, err := schedule.LoadLocation(sc.Timezone, time.Local)
	if err != nil {
		return err
	}
	set := func(p *string) bool { return p != nil && text(p) != "" }
	if set(req.On) {
		if set(req.At) || set(req.In) || set(req.Every) || set(req.Cron) {
			return errors.New("an event trigger runs when its event happens: leave at, in, every and cron out")
		}
		timing = true
		sc.Kind, sc.Event, sc.At, sc.Every, sc.Cron = schedule.OnEvent, strings.ReplaceAll(text(req.On), "-", "_"), time.Time{}, 0, ""
		if sc.Event != schedule.EventIssueLabeled {
			sc.EventLabel = ""
		}
	}
	if req.Label != nil && sc.Kind == schedule.OnEvent && sc.Event == schedule.EventIssueLabeled {
		sc.EventLabel, timing = text(req.Label), true
	}
	if !set(req.On) && (set(req.At) || set(req.In) || set(req.Every) || set(req.Cron)) {
		timing = true
		var at time.Time
		if set(req.At) && set(req.In) {
			return errors.New("give a schedule either at or in, not both")
		}
		if set(req.At) {
			if at, err = schedule.ParseAt(text(req.At), loc, now); err != nil {
				return err
			}
		}
		if set(req.In) {
			d, err := schedule.ParseEvery(text(req.In))
			if err != nil {
				return err
			}
			at = now.Add(d)
		}
		switch {
		case set(req.Cron):
			if set(req.Every) || !at.IsZero() {
				return errors.New("a cron schedule says when it runs by itself: leave at, in and every out")
			}
			sc.Kind, sc.Cron, sc.At, sc.Every = schedule.Cron, text(req.Cron), time.Time{}, 0
		case set(req.Every):
			d, err := schedule.ParseEvery(text(req.Every))
			if err != nil {
				return err
			}
			if at.IsZero() {
				at = now.Add(d)
			}
			sc.Kind, sc.Every, sc.At, sc.Cron, sc.Event, sc.EventLabel = schedule.Every, d, at, "", "", ""
		default:
			sc.Kind, sc.At, sc.Every, sc.Cron, sc.Event, sc.EventLabel = schedule.Once, at, 0, "", "", ""
		}
	} else if creating && !set(req.On) {
		return errors.New("say when it runs: at a time, in a while, every so often, on a cron expression, or on an event")
	}
	unpaused := req.Paused != nil && !*req.Paused && sc.Paused
	if req.Paused != nil {
		sc.Paused = *req.Paused
	}
	if timing || unpaused || creating {
		spec := schedule.Spec{Kind: sc.Kind, At: sc.At, Every: sc.Every, Cron: sc.Cron, Location: loc, Event: sc.Event, Label: sc.EventLabel}
		if err := spec.Check(); err != nil {
			return err
		}
		next, ok := spec.Next(now)
		if !ok && sc.Kind == schedule.Once && timing {
			return fmt.Errorf("%s has already passed", sc.At.In(loc).Format("Mon 2 Jan 15:04 MST"))
		}
		if !ok {
			next = time.Time{}
		}
		// A schedule paused and resumed picks up from now: what it missed
		// while paused was skipped on purpose.
		sc.NextRun = next
	}
	sc.UpdatedAt = now
	return nil
}

// nowMilli is now as the store keeps it, so what a request answers with is
// what the next one reads back.
func nowMilli() time.Time { return time.UnixMilli(time.Now().UnixMilli()) }

// titleFrom is a schedule's title when it was given none: its task's first
// line, cut short.
func titleFrom(task string) string {
	line, _, _ := strings.Cut(task, "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > 60 {
		line = strings.TrimSpace(string(r[:57])) + "…"
	}
	return line
}

func toAPIScheduleRun(r state.ScheduleRun, title string) api.ScheduleRun {
	out := api.ScheduleRun{
		ID: r.ID, Schedule: r.Schedule, Title: title, Trigger: r.Trigger, RanAt: r.RanAt, Late: r.Late, Missed: r.Missed,
		Outcome: r.Outcome, Agent: r.Agent, Job: r.Job, ChatItem: r.ChatItem, Error: r.Error, Event: r.Event, EventURL: r.EventURL,
	}
	if !r.DueAt.IsZero() {
		due := r.DueAt
		out.DueAt = &due
	}
	return out
}

func (s *Server) toAPISchedule(ctx context.Context, sc state.Schedule, runs []state.ScheduleRun) api.Schedule {
	out := api.Schedule{
		ID: sc.ID, Project: sc.Project, Title: sc.Title, Task: sc.Task, Kind: sc.Kind, Event: sc.Event, EventLabel: sc.EventLabel, EveryMinutes: int(sc.Every / time.Minute),
		Cron: sc.Cron, Timezone: sc.Timezone, Describe: s.describeSchedule(ctx, sc), Target: sc.Target,
		Session: sc.Session, Model: sc.Model, Effort: sc.Effort, Precheck: sc.Precheck, GraceMinutes: int(sc.Grace / time.Minute),
		Paused: sc.Paused, CreatedBy: sc.CreatedBy, CreatedAt: sc.CreatedAt, UpdatedAt: sc.UpdatedAt,
	}
	if out.Timezone == "" {
		out.Timezone = s.userLocation(ctx).String()
	}
	if !sc.At.IsZero() {
		at := sc.At
		out.At = &at
	}
	if !sc.NextRun.IsZero() && !sc.Paused {
		next := sc.NextRun
		out.NextRun = &next
	}
	for _, r := range runs {
		if r.Schedule != sc.ID {
			continue
		}
		if out.LastRun == nil {
			last := toAPIScheduleRun(r, sc.Title)
			out.LastRun = &last
		}
		out.Runs++
	}
	return out
}

// maxScheduleRuns is how many runs a project's history lists.
const maxScheduleRuns = 500

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	list, err := s.store.Schedules(r.Context(), project)
	if err != nil {
		return err
	}
	runs, err := s.store.ScheduleRuns(r.Context(), project, "", maxScheduleRuns)
	if err != nil {
		return err
	}
	out := make([]api.Schedule, 0, len(list))
	for _, sc := range list {
		out = append(out, s.toAPISchedule(r.Context(), sc, runs))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) addSchedule(createdBy string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, err := s.store.Project(r.Context(), r.PathValue("project"))
		if err != nil {
			return err
		}
		var req api.ScheduleRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		now := nowMilli()
		sc := state.Schedule{ID: newID(), Project: p.Name, CreatedBy: createdBy, CreatedAt: now}
		if err := s.applyScheduleRequest(r.Context(), &sc, req, now, true); err != nil {
			return err
		}
		if err := s.store.AddSchedule(r.Context(), sc); err != nil {
			return err
		}
		s.scheduleChanged(r.Context(), sc, "schedule_added")
		return writeJSON(w, http.StatusCreated, s.toAPISchedule(r.Context(), sc, nil))
	}
}

func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) error {
	sc, err := s.store.Schedule(r.Context(), r.PathValue("project"), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req api.ScheduleRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	was := sc
	if err := s.applyScheduleRequest(r.Context(), &sc, req, nowMilli(), false); err != nil {
		return err
	}
	if err := s.store.UpdateSchedule(r.Context(), sc); err != nil {
		return err
	}
	if was.Kind != sc.Kind || was.Event != sc.Event || was.EventLabel != sc.EventLabel || (was.Paused && !sc.Paused) {
		// Waiting for something else now, or back from a pause: it starts
		// again from what is true now, rather than firing for all that
		// happened meanwhile.
		if err := s.store.ForgetScheduleEvents(r.Context(), sc.ID, nil); err != nil {
			return err
		}
	}
	s.scheduleChanged(r.Context(), sc, "schedule_changed")
	runs, _ := s.store.ScheduleRuns(r.Context(), sc.Project, sc.ID, maxScheduleRuns)
	return writeJSON(w, http.StatusOK, s.toAPISchedule(r.Context(), sc, runs))
}

func (s *Server) removeSchedule(w http.ResponseWriter, r *http.Request) error {
	sc, err := s.store.Schedule(r.Context(), r.PathValue("project"), r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := s.store.DeleteSchedule(r.Context(), sc.Project, sc.ID); err != nil {
		return err
	}
	s.scheduleChanged(r.Context(), sc, "schedule_removed")
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) runScheduleHandler(w http.ResponseWriter, r *http.Request) error {
	sc, err := s.store.Schedule(r.Context(), r.PathValue("project"), r.PathValue("id"))
	if err != nil {
		return err
	}
	run, err := s.runScheduleNow(s.background(), sc)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, toAPIScheduleRun(run, sc.Title))
}

func (s *Server) listScheduleRuns(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	runs, err := s.store.ScheduleRuns(r.Context(), project, r.URL.Query().Get("schedule"), maxScheduleRuns)
	if err != nil {
		return err
	}
	list, err := s.store.Schedules(r.Context(), project)
	if err != nil {
		return err
	}
	titles := map[string]string{}
	for _, sc := range list {
		titles[sc.ID] = sc.Title
	}
	out := make([]api.ScheduleRun, 0, len(runs))
	for _, run := range runs {
		out = append(out, toAPIScheduleRun(run, titles[run.Schedule]))
	}
	return writeJSON(w, http.StatusOK, out)
}

// scheduleChanged tells the app and the runner, and keeps it in memory.
func (s *Server) scheduleChanged(ctx context.Context, sc state.Schedule, event string) {
	s.events.publish(api.EventSchedule, api.ScheduleChange{Project: sc.Project})
	s.kickSchedules()
	if sc.Kind == schedule.OnEvent {
		// Its first look, which takes what is there already as seen, soon.
		s.prWatch.poke(sc.Project)
	}
	s.captureEvent(ctx, sc.Project, "", event, map[string]any{
		"schedule": sc.ID, "title": sc.Title, "when": s.describeSchedule(ctx, sc), "target": sc.Target, "paused": sc.Paused,
	}, "")
}

// scheduleRoutes are the same for the user, under /v1/projects/{project},
// and for a lead, under /v1/project; only who is recorded as having made one
// differs.
func (s *Server) scheduleRoutes(prefix, createdBy string, handle func(pattern string, fn func(http.ResponseWriter, *http.Request) error)) {
	handle("GET "+prefix+"/schedules", s.listSchedules)
	handle("POST "+prefix+"/schedules", s.addSchedule(createdBy))
	handle("GET "+prefix+"/schedules/runs", s.listScheduleRuns)
	handle("PATCH "+prefix+"/schedules/{id}", s.updateSchedule)
	handle("DELETE "+prefix+"/schedules/{id}", s.removeSchedule)
	handle("POST "+prefix+"/schedules/{id}/run", s.runScheduleHandler)
}
