package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agentbox/internal/state"
)

// A session's id is its topic's slug, numbered when another session of the
// project has it; its lead is named after it, which no agent can be.
func TestLeadSessionIDsComeFromTheirTopic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	now := time.Now()
	first, err := st.AddLeadSession(ctx, "web", "  Prod:   login 500s ", now)
	if err != nil || first.ID != "prod-login-500s" || first.Topic != "Prod: login 500s" {
		t.Fatalf("AddLeadSession() = %+v, %v", first, err)
	}
	second, err := st.AddLeadSession(ctx, "web", "Prod: login 500s!", now)
	if err != nil || second.ID != "prod-login-500s-2" {
		t.Errorf("a second one on the same topic = %+v, %v", second, err)
	}
	if odd, err := st.AddLeadSession(ctx, "web", "日本語", now); err != nil || odd.ID != "session" {
		t.Errorf("a topic with no slug = %+v, %v", odd, err)
	}
	if _, err := st.AddLeadSession(ctx, "web", "   ", now); err == nil {
		t.Error("a session with no topic was opened")
	}
	if got := state.LeadSessionName(first.ID); got != "lead.prod-login-500s" {
		t.Errorf("LeadSessionName() = %q", got)
	}
	if got := state.LeadSessionName(state.MigratedSession); got != state.LeadName {
		t.Errorf("the migrated session's lead = %q, want lead", got)
	}
	for name, want := range map[string]string{"lead": "main", "lead.prod": "prod", "lead.": "", "agent-01": ""} {
		if got, _ := state.SessionOfLead(name); got != want {
			t.Errorf("SessionOfLead(%q) = %q, want %q", name, got, want)
		}
	}
	if err := st.RenameLeadSession(ctx, "web", first.ID, "Prod: fixed"); err != nil {
		t.Fatal(err)
	}
	if got, err := st.LeadSession(ctx, "web", first.ID); err != nil || got.Topic != "Prod: fixed" {
		t.Errorf("after the rename = %+v, %v", got, err)
	}
	all, err := st.LeadSessions(ctx, "web")
	if err != nil || len(all) != 3 {
		t.Errorf("LeadSessions() = %+v, %v", all, err)
	}
}

// The session most recently active is the one whose conversation said
// something last, or the newest opened when none said anything.
func TestLastActiveLeadSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	if _, err := st.LastActiveLeadSession(ctx, "web"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("with no session: %v", err)
	}
	now := time.Now()
	old, _ := st.AddLeadSession(ctx, "web", "Old", now.Add(-time.Hour))
	newer, _ := st.AddLeadSession(ctx, "web", "Newer", now.Add(-time.Minute))
	if got, err := st.LastActiveLeadSession(ctx, "web"); err != nil || got.ID != newer.ID {
		t.Errorf("before anything was said = %+v, %v; want the newest", got, err)
	}
	item, _ := json.Marshal(map[string]any{"id": "a1", "kind": "assistant", "createdAt": now, "updatedAt": now})
	if err := st.SaveChatItems(ctx, "web", state.LeadSessionName(old.ID), []state.ChatItem{{ID: "a1", Position: 0, Data: item}}); err != nil {
		t.Fatal(err)
	}
	if got, err := st.LastActiveLeadSession(ctx, "web"); err != nil || got.ID != old.ID {
		t.Errorf("after the old one spoke = %+v, %v; want it", got, err)
	}
}

// Deleting a session takes its lead and its conversation, and leaves the
// agents it made to nobody; handing one over moves it.
func TestDeletingASessionLeavesItsAgentsToNobody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	ls, err := st.AddLeadSession(ctx, "web", "Prod", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lead := state.Agent{Project: "web", Name: state.LeadSessionName(ls.ID), Instance: "ab-web-lead.prod", AI: "claude", Status: state.AgentReady, Role: state.RoleLead, CreatedAt: time.Now()}
	if err := st.AddAgent(ctx, lead); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent-01", "agent-02"} {
		a := state.Agent{Project: "web", Name: name, Instance: "ab-web-" + name, AI: "claude", Status: state.AgentReady, CreatedAt: time.Now(), CreatedBySession: ls.ID}
		if err := st.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetAgentSession(ctx, "web", "agent-02", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentSession(ctx, "web", lead.Name, "x"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a lead was handed to a session: %v", err)
	}
	if err := st.SaveChatItems(ctx, "web", lead.Name, []state.ChatItem{{ID: "u1", Position: 0, Data: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	freed, err := st.DeleteLeadSession(ctx, "web", ls.ID)
	if err != nil || len(freed) != 1 || freed[0] != "agent-01" {
		t.Fatalf("DeleteLeadSession() = %v, %v; want agent-01 freed", freed, err)
	}
	if a, err := st.Agent(ctx, "web", "agent-01"); err != nil || a.Session() != "" {
		t.Errorf("agent-01 = %+v, %v; want it nobody's", a, err)
	}
	if _, err := st.Agent(ctx, "web", lead.Name); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the session's lead: %v", err)
	}
	if rows, err := st.ChatItems(ctx, "web", lead.Name); err != nil || len(rows) != 0 {
		t.Errorf("its conversation: %d rows, %v", len(rows), err)
	}
	if _, err := st.DeleteLeadSession(ctx, "web", ls.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("deleting it again: %v", err)
	}
	// Its id is never handed out again: a new session that took it would be
	// shown what the app still holds under its name.
	if again, err := st.AddLeadSession(ctx, "web", "Prod", time.Now()); err != nil || again.ID != "prod-2" {
		t.Errorf("a new Prod session = %+v, %v; want prod-2, not the deleted one's id", again, err)
	}
}

// Closing a session keeps it, its conversation, lead and agents, in the
// chat's history, out of the open ones; reopening puts it back as it was. No
// new session ever takes the id of one closed, deleted, or reset away.
func TestClosedSessionsGoToTheHistoryAndKeepTheirIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	now := time.Now()
	first, err := st.AddUntitledLeadSession(ctx, "web", now)
	if err != nil || first.ID != "session" {
		t.Fatalf("AddUntitledLeadSession() = %+v, %v", first, err)
	}
	lead := state.LeadSessionName(first.ID)
	if err := st.SaveChatItems(ctx, "web", lead, []state.ChatItem{{ID: "u1", Position: 0, Data: []byte(`{"text":"hello"}`)}}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{Project: "web", Name: "agent-01", Instance: "ab-web-agent-01", AI: "claude", Status: state.AgentReady, CreatedAt: now, CreatedBySession: first.ID}
	if err := st.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseLeadSession(ctx, "web", first.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseLeadSession(ctx, "web", first.ID, now); err == nil {
		t.Error("a closed session was closed again")
	}
	if open, err := st.LeadSessions(ctx, "web"); err != nil || len(open) != 0 {
		t.Errorf("open sessions = %+v, %v; want none", open, err)
	}
	if _, err := st.LastActiveLeadSession(ctx, "web"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a closed session is the most recently active: %v", err)
	}
	closed, err := st.ClosedLeadSessions(ctx, "web")
	if err != nil || len(closed) != 1 || closed[0].ID != first.ID || !closed[0].Closed() || closed[0].ClosedAt.UnixMilli() != now.Add(time.Minute).UnixMilli() {
		t.Fatalf("ClosedLeadSessions() = %+v, %v", closed, err)
	}
	if rows, err := st.ChatItems(ctx, "web", lead); err != nil || len(rows) != 1 {
		t.Errorf("its conversation = %d rows, %v; want it kept", len(rows), err)
	}
	if got, err := st.Agent(ctx, "web", "agent-01"); err != nil || got.Session() != first.ID {
		t.Errorf("its agent = %+v, %v; want it still the closed session's", got, err)
	}

	// Ctrl+T now: a session of its own, never the closed one's id.
	second, err := st.AddUntitledLeadSession(ctx, "web", now)
	if err != nil || second.ID != "session-2" {
		t.Fatalf("a new session after closing = %+v, %v; want session-2", second, err)
	}
	if rows, err := st.ChatItems(ctx, "web", state.LeadSessionName(second.ID)); err != nil || len(rows) != 0 {
		t.Errorf("the new session's conversation = %d rows, %v; want it empty", len(rows), err)
	}

	if err := st.ReopenLeadSession(ctx, "web", first.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.ReopenLeadSession(ctx, "web", first.ID); err == nil {
		t.Error("an open session was reopened")
	}
	if open, err := st.LeadSessions(ctx, "web"); err != nil || len(open) != 2 || open[0].Closed() {
		t.Errorf("open sessions after reopening = %+v, %v; want both", open, err)
	}

	// Deleted, and reset away: their ids stay taken.
	if _, err := st.DeleteLeadSession(ctx, "web", second.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseLeadSession(ctx, "web", first.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveLeadSessions(ctx, "web"); err != nil {
		t.Fatal(err)
	}
	if all, err := st.AllLeadSessions(ctx, "web"); err != nil || len(all) != 0 {
		t.Errorf("after the reset = %+v, %v; want the closed one gone too", all, err)
	}
	if third, err := st.AddUntitledLeadSession(ctx, "web", now); err != nil || third.ID != "session-3" {
		t.Errorf("a new session after the reset = %+v, %v; want session-3", third, err)
	}
}

// A session opened with no topic is untitled until its conversation names it,
// and a name the user gives it is never overwritten by that.
func TestAnUntitledSessionKeepsTheUsersName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	now := time.Now()
	ls, err := st.AddUntitledLeadSession(ctx, "web", now)
	if err != nil || ls.ID != "session" || ls.Topic != state.UntitledTopic || !ls.Untitled {
		t.Fatalf("AddUntitledLeadSession() = %+v, %v", ls, err)
	}
	if again, err := st.AddUntitledLeadSession(ctx, "web", now); err != nil || again.ID != "session-2" {
		t.Errorf("a second untitled session = %+v, %v", again, err)
	}
	if named, err := st.NameLeadSession(ctx, "web", ls.ID, "Prod: login 500s"); err != nil || !named {
		t.Fatalf("NameLeadSession() = %v, %v", named, err)
	}
	if got, err := st.LeadSession(ctx, "web", ls.ID); err != nil || got.Topic != "Prod: login 500s" || got.Untitled {
		t.Errorf("after naming = %+v, %v", got, err)
	}
	if named, err := st.NameLeadSession(ctx, "web", ls.ID, "Something else"); err != nil || named {
		t.Errorf("naming a named session = %v, %v; want it left alone", named, err)
	}

	// Renamed by the user before its first turn ended: theirs stays.
	mine, err := st.AddUntitledLeadSession(ctx, "web", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RenameLeadSession(ctx, "web", mine.ID, "Mine"); err != nil {
		t.Fatal(err)
	}
	if named, err := st.NameLeadSession(ctx, "web", mine.ID, "Not mine"); err != nil || named {
		t.Errorf("naming a session the user named = %v, %v", named, err)
	}
	if got, _ := st.LeadSession(ctx, "web", mine.ID); got.Topic != "Mine" || got.Untitled {
		t.Errorf("the user's session = %+v", got)
	}
	if sessions, err := st.LeadSessions(ctx, "web"); err != nil || len(sessions) != 3 || !sessions[1].Untitled {
		t.Errorf("LeadSessions() = %+v, %v; want the second still untitled", sessions, err)
	}
}
