package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSchedules(t *testing.T) {
	s := openSearchStore(t)
	ctx := context.Background()
	now := time.UnixMilli(time.Now().UnixMilli())
	due := now.Add(-time.Minute)
	sc := Schedule{ID: "s1", Project: "p", Title: "release checks", Task: "run them", Kind: "cron", Cron: "0 9 * * *",
		Timezone: "Europe/Lisbon", Target: ScheduleTargetLead, Precheck: "true", Grace: 30 * time.Minute, Model: "opus", NextRun: due, CreatedBy: "lead", CreatedAt: now, UpdatedAt: now}
	if err := s.AddSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	later := Schedule{ID: "s2", Project: "p", Title: "later", Task: "x", Kind: "once", At: now.Add(time.Hour), NextRun: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
	if err := s.AddSchedule(ctx, later); err != nil {
		t.Fatal(err)
	}
	got, err := s.Schedule(ctx, "p", "s1")
	if err != nil || got.Cron != "0 9 * * *" || !got.NextRun.Equal(due) || got.Timezone != "Europe/Lisbon" || got.Grace != 30*time.Minute || got.Precheck != "true" || got.Model != "opus" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := s.Schedule(ctx, "other", "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another project's schedule: %v", err)
	}
	dueList, err := s.DueSchedules(ctx, now)
	if err != nil || len(dueList) != 1 || dueList[0].ID != "s1" {
		t.Fatalf("due: %+v %v", dueList, err)
	}
	if next, ok, err := s.NextScheduleTime(ctx); err != nil || !ok || !next.Equal(due) {
		t.Fatalf("next: %v %v %v", next, ok, err)
	}

	run := ScheduleRun{ID: "r1", Schedule: "s1", Project: "p", Trigger: ScheduleTriggerTime, DueAt: due, RanAt: now}
	if err := s.ClaimScheduleRun(ctx, run, due, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The same due time can't be claimed twice.
	run.ID = "r2"
	if err := s.ClaimScheduleRun(ctx, run, due, now.Add(24*time.Hour)); !errors.Is(err, ErrScheduleMoved) {
		t.Fatalf("second claim: %v", err)
	}
	// A manual run leaves next_run alone.
	manual := ScheduleRun{ID: "r3", Schedule: "s1", Project: "p", Trigger: ScheduleTriggerManual, RanAt: now.Add(time.Second)}
	if err := s.ClaimScheduleRun(ctx, manual, time.Time{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Schedule(ctx, "p", "s1"); !got.NextRun.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("next run after claims: %v", got.NextRun)
	}
	manual.Outcome, manual.Job = ScheduleRunAgent, "job-1"
	if err := s.FinishScheduleRun(ctx, manual); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScheduleRunAgent(ctx, "job-1", "agent-7"); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ScheduleRuns(ctx, "p", "s1", 10)
	if err != nil || len(runs) != 2 || runs[0].ID != "r3" || runs[0].Agent != "agent-7" || runs[1].Outcome != "" {
		t.Fatalf("runs: %+v %v", runs, err)
	}

	got.Paused, got.Title = true, "renamed"
	if err := s.UpdateSchedule(ctx, got); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.DueSchedules(ctx, now.Add(48*time.Hour)); len(list) != 1 || list[0].ID != "s2" {
		t.Fatalf("paused schedule due: %+v", list)
	}
	if err := s.DeleteSchedule(ctx, "p", "s1"); err != nil {
		t.Fatal(err)
	}
	if runs, _ := s.ScheduleRuns(ctx, "p", "", 10); len(runs) != 0 {
		t.Fatalf("runs left: %+v", runs)
	}
	if err := s.DeleteSchedule(ctx, "p", "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting again: %v", err)
	}
}
