package daemon

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Only a Claude Code lead's chat sleeps, after the setting's time, and not at
// all once it is turned off; a schedule due within minutes keeps it up.
func TestLeadSleep(t *testing.T) {
	t.Parallel()
	d, lead := oneProject(t)
	ctx := context.Background()
	srv := d.srv
	claudeLead := state.Agent{Project: "web", Name: state.LeadName, Role: state.RoleLead, AI: "claude"}

	if after, applies := srv.leadSleepAfter(claudeLead); !applies || after != state.DefaultLeadSleep {
		t.Errorf("a Claude Code lead sleeps after %s (%v), want %s", after, applies, state.DefaultLeadSleep)
	}
	for _, a := range []state.Agent{
		{Project: "web", Name: "agent-1", AI: "claude"},
		{Project: "web", Name: state.LeadName, Role: state.RoleLead, AI: "codex"},
		{Project: "web", Name: state.LeadName, Role: state.RoleLead, AI: "opencode"},
	} {
		if _, applies := srv.leadSleepAfter(a); applies {
			t.Errorf("%s on %s sleeps", a.Name, a.AI)
		}
	}

	settings, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{LeadSleepMinutes: ptr(30)})
	if err != nil || !settings.LeadSleep || settings.LeadSleepMinutes != 30 {
		t.Fatalf("setting 30 minutes = %+v, %v", settings, err)
	}
	if after, _ := srv.leadSleepAfter(claudeLead); after != 30*time.Minute {
		t.Errorf("sleeps after %s, want 30m", after)
	}
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{LeadSleepMinutes: ptr(1)}); err == nil {
		t.Error("a minute was taken")
	}
	settings, err = d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{LeadSleep: ptr(false)})
	if err != nil || settings.LeadSleep {
		t.Fatalf("turning it off = %+v, %v", settings, err)
	}
	if after, applies := srv.leadSleepAfter(claudeLead); after != 0 || !applies {
		t.Errorf("turned off, it sleeps after %s (%v)", after, applies)
	}

	if srv.leadDueSoon(claudeLead) {
		t.Error("due soon with no schedule")
	}
	if _, err := lead.LeadAddSchedule(ctx, api.ScheduleRequest{Task: str("Check CI."), In: str("2h")}); err != nil {
		t.Fatal(err)
	}
	if srv.leadDueSoon(claudeLead) {
		t.Error("due soon with a schedule two hours away")
	}
	if _, err := lead.LeadAddSchedule(ctx, api.ScheduleRequest{Task: str("Check CI again."), In: str("3m")}); err != nil {
		t.Fatal(err)
	}
	if !srv.leadDueSoon(claudeLead) {
		t.Error("not due soon with a schedule three minutes away")
	}
}
