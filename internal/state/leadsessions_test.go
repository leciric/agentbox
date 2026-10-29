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
	// Its id is free again, its conversation gone with it.
	if again, err := st.AddLeadSession(ctx, "web", "Prod", time.Now()); err != nil || again.ID != ls.ID {
		t.Errorf("reopened = %+v, %v", again, err)
	}
}
