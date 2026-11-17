package state_test

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/state"
)

// A snoozed session stays open but is hidden until its time: WakeLeadSessions
// brings it back then, and not before, counting its idle time from the wake.
func TestSnoozedSessionsWakeAtTheirTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	now := time.Now()
	ls, err := st.AddLeadSession(ctx, "web", "Prod", now)
	if err != nil {
		t.Fatal(err)
	}
	until := now.Add(2 * time.Hour)
	if err := st.SnoozeLeadSession(ctx, "web", ls.ID, until, now); err != nil {
		t.Fatal(err)
	}
	got, _ := st.LeadSession(ctx, "web", ls.ID)
	if !got.Snoozed(now) || got.Snoozed(until) || got.SnoozedUntil.UnixMilli() != until.UnixMilli() {
		t.Fatalf("snoozed = %+v", got)
	}
	if woke, err := st.WakeLeadSessions(ctx, now.Add(time.Hour)); err != nil || len(woke) != 0 {
		t.Errorf("an hour in, woke %v, %v", woke, err)
	}
	later := until.Add(time.Minute)
	woke, err := st.WakeLeadSessions(ctx, later)
	if err != nil || len(woke["web"]) != 1 || woke["web"][0] != ls.ID {
		t.Fatalf("at its time, woke %v, %v", woke, err)
	}
	got, _ = st.LeadSession(ctx, "web", ls.ID)
	if !got.SnoozedUntil.IsZero() || got.ReopenedAt.UnixMilli() != later.UnixMilli() {
		t.Errorf("after waking = %+v", got)
	}
	if woke, _ := st.WakeLeadSessions(ctx, later.Add(time.Hour)); len(woke) != 0 {
		t.Errorf("woke twice: %v", woke)
	}
	if err := st.CloseLeadSession(ctx, "web", ls.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.SnoozeLeadSession(ctx, "web", ls.ID, until, now); err == nil {
		t.Error("a closed session was snoozed")
	}
}

// A session that settles is in the history marked so; reopening it clears
// the mark and starts its idle time again.
func TestASettledSessionReopensUnsettled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	now := time.Now()
	ls, _ := st.AddLeadSession(ctx, "web", "Prod", now)
	if err := st.SnoozeLeadSession(ctx, "web", ls.ID, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if err := st.SettleLeadSession(ctx, "web", ls.ID, now); err != nil {
		t.Fatal(err)
	}
	got, _ := st.LeadSession(ctx, "web", ls.ID)
	if !got.Closed() || !got.Settled || !got.SnoozedUntil.IsZero() {
		t.Fatalf("settled = %+v", got)
	}
	if err := st.SettleLeadSession(ctx, "web", ls.ID, now); err == nil {
		t.Error("a closed session settled again")
	}
	if err := st.ReopenLeadSession(ctx, "web", ls.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = st.LeadSession(ctx, "web", ls.ID)
	if got.Closed() || got.Settled || got.ReopenedAt.IsZero() {
		t.Errorf("reopened = %+v", got)
	}
}

// A project's settle time is a day unless it says otherwise, and can be off.
func TestProjectSettleHours(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	read := func() time.Duration {
		p, err := st.Project(ctx, "web")
		if err != nil {
			t.Fatal(err)
		}
		return p.SettleAfter()
	}
	if got := read(); got != 24*time.Hour {
		t.Errorf("by default = %s", got)
	}
	for hours, want := range map[int]time.Duration{4: 4 * time.Hour, 0: 0, 168: 168 * time.Hour} {
		if err := st.SetProjectSettleHours(ctx, "web", hours); err != nil {
			t.Fatal(err)
		}
		if got := read(); got != want {
			t.Errorf("set to %d = %s, want %s", hours, got, want)
		}
	}
}

// An archived agent stays its session's, and leaves it when the session goes.
func TestArchivedAgentsKeepTheirSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	ls, _ := st.AddLeadSession(ctx, "web", "Prod", time.Now())
	a := state.Agent{Project: "web", Name: "agent-01", Instance: "web-agent-01", AI: "claude", Branch: "agentbox/x", CreatedAt: time.Now(), CreatedBySession: ls.ID}
	if err := st.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := st.ArchiveAgent(ctx, a, "do it", state.ArchivedPR{Number: 7, State: state.PRMerged}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := st.ArchivedOfSession(ctx, "web", ls.ID)
	if err != nil || len(got) != 1 || got[0].PR.Number != 7 {
		t.Fatalf("ArchivedOfSession() = %+v, %v", got, err)
	}
	if _, err := st.DeleteLeadSession(ctx, "web", ls.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ArchivedOfSession(ctx, "web", ls.ID); len(got) != 0 {
		t.Errorf("after deleting the session = %+v", got)
	}
}
