package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// The Home chat has sessions as a project's chat does, under its own key: a
// new installation's Home opens on one, untitled; each new one has its own
// lead, HOME and socket; a notice goes to the session it names, else to the
// one most recently active; and deleting one takes its HOME with it.
func TestHomeChatHasSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}

	sessions, err := d.client.LeadSessions(ctx, api.HomeProject)
	if err != nil || len(sessions) != 1 || sessions[0].ID != state.MigratedSession || sessions[0].Ref != "_home/lead" || !sessions[0].Untitled {
		t.Fatalf("LeadSessions(_home) = %+v, %v; want the one untitled session a new Home has", sessions, err)
	}
	main, err := d.srv.sessionLead(ctx, api.HomeProject, state.MigratedSession)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := d.client.NewLeadSession(ctx, api.HomeProject, "Weekly planning")
	if err != nil {
		t.Fatal(err)
	}
	if plan.ID != "weekly-planning" || plan.Ref != "_home/lead.weekly-planning" {
		t.Fatalf("NewLeadSession(_home) = %+v", plan)
	}
	planLead, err := d.srv.sessionLead(ctx, api.HomeProject, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !planLead.IsHome() || planLead.Name != "lead.weekly-planning" || planLead.Worktree != main.Worktree {
		t.Errorf("the session's lead = %+v, want a Home lead of its own in Home's folder", planLead)
	}
	home := d.paths.LeadSessionHome(api.HomeProject, plan.ID)
	brief, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), "Session: **Weekly planning**") || !strings.Contains(string(brief), "beside New session") {
		t.Errorf("the session's brief doesn't name it and the other one:\n%s", brief)
	}
	// Each session has a socket of its own, serving the Home chat's routes.
	for _, session := range []string{state.MigratedSession, plan.ID} {
		if _, err := api.NewClient(d.srv.sessionSocketPath(api.HomeProject, session)).Projects(ctx); err != nil {
			t.Fatalf("the %s session's socket: %v", session, err)
		}
	}
	// Its chat is reached by its ref, as a project session's is.
	if th, err := d.client.Chat(ctx, plan.Ref); err != nil || th.Agent != plan.Ref {
		t.Errorf("Chat(%s) = %+v, %v; want that session's chat", plan.Ref, th.Agent, err)
	}

	// A notice for a session goes to it; one for none, to the session that
	// spoke last.
	d.srv.tellSession(ctx, api.HomeProject, state.MigratedSession, "for the first session", false)
	saidLast(t, d, planLead, "Planned.")
	d.srv.tellSession(ctx, api.HomeProject, "", "for whoever spoke last", false)
	mainHeard, planHeard := strings.Join(noticesOf(t, d, main), "\n"), strings.Join(noticesOf(t, d, planLead), "\n")
	if !strings.Contains(mainHeard, "for the first session") || strings.Contains(mainHeard, "whoever") {
		t.Errorf("the first session heard:\n%s\nwant only its own notice", mainHeard)
	}
	if !strings.Contains(planHeard, "whoever spoke last") || strings.Contains(planHeard, "first session") {
		t.Errorf("the planning session heard:\n%s\nwant only the notice nobody owns", planHeard)
	}

	if err := d.client.DeleteLeadSession(ctx, api.HomeProject, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("the deleted session's HOME is still there (stat: %v)", err)
	}
	if sessions, err := d.client.LeadSessions(ctx, api.HomeProject); err != nil || len(sessions) != 1 {
		t.Errorf("after deleting one, LeadSessions(_home) = %+v, %v; want the first left", sessions, err)
	}

	// Resetting the Home chat forgets every session: it shows its empty state.
	if err := d.client.ResetProjectChat(ctx, api.HomeProject); err != nil {
		t.Fatal(err)
	}
	if sessions, err := d.client.LeadSessions(ctx, api.HomeProject); err != nil || len(sessions) != 0 {
		t.Errorf("after a reset, LeadSessions(_home) = %+v, %v; want none", sessions, err)
	}
}
