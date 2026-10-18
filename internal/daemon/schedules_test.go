package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// oneProject is a daemon with one project, web, whose lead can chat and has
// its socket up.
func oneProject(t *testing.T) (testDaemon, *api.Client) {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if _, err := d.client.AddProject(context.Background(), api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack"), Name: "web"}); err != nil {
		t.Fatal(err)
	}
	leadReadyToChat(t, d, "web")
	if err := d.srv.serveLeadAPI("web"); err != nil {
		t.Fatal(err)
	}
	return d, api.NewClient(d.srv.leadSocketPath("web"))
}

func scheduleChips(t *testing.T, d testDaemon, project string) []api.ChatItem {
	t.Helper()
	lead, err := d.srv.manager(nil).Lead(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	th, err := d.srv.chat.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	var chips []api.ChatItem
	for _, it := range th.Items {
		if it.ScheduledRun != nil {
			chips = append(chips, it)
		}
	}
	return chips
}

func str(s string) *string { return &s }

// A lead schedules a task for later; when it falls due the run is recorded,
// the lead's chat shows a chip with the task, and the lead is woken with it.
func TestScheduleFromTheLeadFires(t *testing.T) {
	t.Parallel()
	d, lead := oneProject(t)
	ctx := context.Background()
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{Timezone: str("America/Sao_Paulo")}); err != nil {
		t.Fatal(err)
	}
	sc, err := lead.LeadAddSchedule(ctx, api.ScheduleRequest{Task: str("Check CI on #384 and tell me how it went."), In: str("2h")})
	if err != nil {
		t.Fatal(err)
	}
	if sc.Kind != api.ScheduleOnce || sc.Title != "Check CI on #384 and tell me how it went." || sc.CreatedBy != "lead" ||
		sc.Timezone != "America/Sao_Paulo" || sc.NextRun == nil || time.Until(*sc.NextRun) < 119*time.Minute {
		t.Fatalf("schedule = %+v", sc)
	}
	list, err := d.client.Schedules(ctx, "web")
	if err != nil || len(list) != 1 || list[0].ID != sc.ID {
		t.Fatalf("Schedules() = %+v, %v", list, err)
	}

	// Nothing is due yet.
	d.srv.fireDueSchedules(ctx, time.Now())
	if chips := scheduleChips(t, d, "web"); len(chips) != 0 {
		t.Fatalf("ran early: %+v", chips)
	}
	d.srv.fireDueSchedules(ctx, sc.NextRun.Add(time.Second))
	chips := scheduleChips(t, d, "web")
	if len(chips) != 1 || chips[0].Text != sc.Task || chips[0].ScheduledRun.Schedule != sc.ID || chips[0].ScheduledRun.Late {
		t.Fatalf("chips = %+v", chips)
	}
	notices := hiddenNotices(t, d, "web")
	if len(notices) == 0 || !strings.Contains(notices[len(notices)-1], "Check CI on #384") || !strings.Contains(notices[len(notices)-1], "is due now") {
		t.Fatalf("notices = %q", notices)
	}
	runs, err := d.client.ScheduleRuns(ctx, "web", sc.ID)
	if err != nil || len(runs) != 1 || runs[0].Outcome != "sent" || runs[0].ChatItem != chips[0].ID {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	// A one-off runs once.
	d.srv.fireDueSchedules(ctx, sc.NextRun.Add(time.Hour))
	if list, _ := d.client.Schedules(ctx, "web"); list[0].NextRun != nil || list[0].Runs != 1 || list[0].LastRun == nil {
		t.Fatalf("after its run: %+v", list[0])
	}
}

// A run missed while the daemon was down runs once, late, counting the
// others it stands for; with a grace period, one later than that is skipped.
func TestMissedRunsRunOnceLate(t *testing.T) {
	t.Parallel()
	d, lead := oneProject(t)
	ctx := context.Background()
	daily, err := lead.LeadAddSchedule(ctx, api.ScheduleRequest{Title: str("Release checks"), Task: str("Run the release checks."), Cron: str("0 9 * * *"), Timezone: str("Europe/Lisbon")})
	if err != nil {
		t.Fatal(err)
	}
	strict, err := d.client.AddSchedule(ctx, "web", api.ScheduleRequest{Task: str("Hourly CI check."), Cron: str("0 * * * *"), GraceMinutes: new(30)})
	if err != nil {
		t.Fatal(err)
	}
	// Three days and an hour on: the daily one was due four times.
	now := daily.NextRun.Add(3*24*time.Hour + time.Hour)
	d.srv.fireDueSchedules(ctx, now)

	runs, _ := d.client.ScheduleRuns(ctx, "web", daily.ID)
	if len(runs) != 1 || !runs[0].Late || runs[0].Missed != 3 || runs[0].Outcome != "sent" {
		t.Fatalf("daily runs = %+v", runs)
	}
	notices := hiddenNotices(t, d, "web")
	if last := notices[len(notices)-1]; !strings.Contains(last, "late") || !strings.Contains(last, "3 more time(s)") {
		t.Fatalf("notice = %q", last)
	}
	if chips := scheduleChips(t, d, "web"); len(chips) != 1 || !chips[0].ScheduledRun.Late {
		t.Fatalf("chips = %+v", chips)
	}
	runs, _ = d.client.ScheduleRuns(ctx, "web", strict.ID)
	if len(runs) != 1 || runs[0].Outcome != "skipped" || !strings.Contains(runs[0].Error, "grace") {
		t.Fatalf("strict runs = %+v", runs)
	}
	list, _ := d.client.Schedules(ctx, "web")
	for _, sc := range list {
		if sc.NextRun == nil || !sc.NextRun.After(now) {
			t.Errorf("%s next runs at %v, not after now", sc.Title, sc.NextRun)
		}
	}
}

// A precheck that fails skips the run; one that passes lets it through.
func TestPrecheckSkipsARun(t *testing.T) {
	t.Parallel()
	d, _ := oneProject(t)
	ctx := context.Background()
	sc, err := d.client.AddSchedule(ctx, "web", api.ScheduleRequest{Task: str("Triage new issues."), Every: str("1h"), Precheck: str("echo nothing new; exit 1")})
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.client.RunSchedule(ctx, "web", sc.ID)
	if err != nil || run.Outcome != "skipped" || !strings.Contains(run.Error, "nothing new") || run.Trigger != "manual" {
		t.Fatalf("RunSchedule() = %+v, %v", run, err)
	}
	if _, err := d.client.UpdateSchedule(ctx, "web", sc.ID, api.ScheduleRequest{Precheck: str("test -d .git || test -f .git")}); err != nil {
		t.Fatal(err)
	}
	if run, err := d.client.RunSchedule(ctx, "web", sc.ID); err != nil || run.Outcome != "sent" {
		t.Fatalf("RunSchedule() = %+v, %v", run, err)
	}
	// Running it by hand leaves its next time alone.
	if list, _ := d.client.Schedules(ctx, "web"); list[0].NextRun == nil || !list[0].NextRun.Equal(*sc.NextRun) {
		t.Fatalf("next run moved: %v, was %v", list[0].NextRun, sc.NextRun)
	}
}

// Pausing stops it; resuming picks up from now rather than catching up; it
// can be edited and deleted.
func TestPauseEditDelete(t *testing.T) {
	t.Parallel()
	d, lead := oneProject(t)
	ctx := context.Background()
	sc, err := lead.LeadAddSchedule(ctx, api.ScheduleRequest{Task: str("Triage."), Cron: str("0 8 * * 1")})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := lead.LeadUpdateSchedule(ctx, sc.ID, api.ScheduleRequest{Paused: new(true)})
	if err != nil || !paused.Paused || paused.NextRun != nil {
		t.Fatalf("paused = %+v, %v", paused, err)
	}
	d.srv.fireDueSchedules(ctx, sc.NextRun.Add(time.Minute))
	if runs, _ := d.client.ScheduleRuns(ctx, "web", ""); len(runs) != 0 {
		t.Fatalf("a paused schedule ran: %+v", runs)
	}
	edited, err := lead.LeadUpdateSchedule(ctx, sc.ID, api.ScheduleRequest{Paused: new(false), Cron: str("30 7 * * 1-5"), Title: str("Weekday triage"), Target: str("agent")})
	if err != nil || edited.Paused || edited.Cron != "30 7 * * 1-5" || edited.Target != "agent" || edited.Title != "Weekday triage" || edited.NextRun == nil {
		t.Fatalf("edited = %+v, %v", edited, err)
	}
	if err := lead.LeadRemoveSchedule(ctx, sc.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := lead.LeadSchedules(ctx); len(list) != 0 {
		t.Fatalf("left: %+v", list)
	}
}

func TestScheduleRequestsAreChecked(t *testing.T) {
	t.Parallel()
	d, _ := oneProject(t)
	ctx := context.Background()
	for name, req := range map[string]api.ScheduleRequest{
		"no task":      {In: str("1h")},
		"no time":      {Task: str("x")},
		"past":         {Task: str("x"), At: str("2020-01-01 09:00")},
		"at and in":    {Task: str("x"), At: str("09:00"), In: str("1h")},
		"too often":    {Task: str("x"), Every: str("1m")},
		"bad cron":     {Task: str("x"), Cron: str("every monday")},
		"cron and in":  {Task: str("x"), Cron: str("@daily"), In: str("1h")},
		"bad timezone": {Task: str("x"), In: str("1h"), Timezone: str("Mars/Olympus")},
		"bad target":   {Task: str("x"), In: str("1h"), Target: str("everyone")},
	} {
		if _, err := d.client.AddSchedule(ctx, "web", req); err == nil {
			t.Errorf("%s: made", name)
		}
	}
	if _, err := d.client.AddSchedule(ctx, "nope", api.ScheduleRequest{Task: str("x"), In: str("1h")}); err == nil {
		t.Error("made for a project that doesn't exist")
	}
}

func TestScheduleNoticeForAnAgent(t *testing.T) {
	sc := state.Schedule{ID: "s1", Title: "Audit", Task: "Audit the repo.", Target: state.ScheduleTargetAgent}
	got := scheduleNotice(sc, state.ScheduleRun{Trigger: state.ScheduleTriggerTime, Job: "j1"}, "every 1d")
	if !strings.Contains(got, "job j1") || !strings.Contains(got, "Audit the repo.") {
		t.Errorf("notice = %q", got)
	}
	got = scheduleNotice(sc, state.ScheduleRun{Trigger: state.ScheduleTriggerManual, Agent: "agent-03"}, "every 1d")
	if !strings.Contains(got, "agent-03") || !strings.Contains(got, "by hand") {
		t.Errorf("notice = %q", got)
	}
	if titleFrom(strings.Repeat("word ", 30)) == "" || len([]rune(titleFrom(strings.Repeat("a", 100)))) != 58 {
		t.Errorf("titleFrom = %q", titleFrom(strings.Repeat("a", 100)))
	}
}
