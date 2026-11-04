package state_test

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/state"
)

// A lead session's settings are written before its first turn makes its
// agent: what its composer chose, or what it inherited. Making the agent
// keeps them. A worker of a reused name starts on nothing of its
// predecessor's.
func TestAddingALeadKeepsItsChatSettings(t *testing.T) {
	t.Parallel()
	st := openStore(t)
	ctx := context.Background()
	if err := st.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	chosen := state.Chat{Options: map[string]string{"mode": "acceptEdits"}, Tool: "claude"}
	for _, a := range []state.Agent{
		{Project: "p", Name: state.LeadSessionName("prod"), Role: state.RoleLead, Interface: state.InterfaceChat},
		{Project: "p", Name: "agent-01"},
	} {
		if err := st.SaveChat(ctx, "p", a.Name, chosen); err != nil {
			t.Fatal(err)
		}
		a.Instance, a.CreatedAt = "ab-p-"+a.Name, time.Now()
		if err := st.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		got, err := st.Chat(ctx, "p", a.Name)
		if err != nil {
			t.Fatal(err)
		}
		if kept := got.Options["mode"] == "acceptEdits"; kept != a.IsLead() {
			t.Errorf("%s's settings after AddAgent = %+v (lead %v)", a.Name, got, a.IsLead())
		}
	}
}
