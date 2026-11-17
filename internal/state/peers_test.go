package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentbox/internal/state"
)

func TestPeerAgents(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if _, err := st.PeerAgent(ctx, "pawly", "agent-01"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("an agent made here: %v", err)
	}
	want := state.PeerAgent{Project: "pawly", Agent: "agent-01", OriginID: "env_1", OriginName: "laptop", OriginProject: "pawly-app", CreatedAt: time.UnixMilli(1000)}
	if err := st.AddPeerAgent(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := st.PeerAgent(ctx, "pawly", "agent-01")
	if err != nil || got != want {
		t.Errorf("PeerAgent = %+v, %v; want %+v", got, err, want)
	}
}

// A remote agent's later notice keeps what an earlier one said and it
// doesn't.
func TestRemoteAgents(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	for _, a := range []state.RemoteAgent{
		{Project: "pawly", MachineID: "env_2", MachineName: "vps", Agent: "agent-03", Title: "Reminders", Branch: "agentbox/reminders", Status: state.RemoteCreating, UpdatedAt: time.UnixMilli(1)},
		{Project: "pawly", MachineID: "env_2", MachineName: "vps", Agent: "agent-03", Status: state.RemoteDone, Summary: "opened #12", PRURL: "https://github.com/o/r/pull/12", UpdatedAt: time.UnixMilli(2)},
		{Project: "other", MachineID: "env_2", MachineName: "vps", Agent: "agent-01", UpdatedAt: time.UnixMilli(3)},
	} {
		if err := st.SaveRemoteAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.RemoteAgents(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Reminders" || got[0].Branch != "agentbox/reminders" || got[0].Status != state.RemoteDone ||
		got[0].PRURL != "https://github.com/o/r/pull/12" || !got[0].UpdatedAt.Equal(time.UnixMilli(2)) {
		t.Errorf("RemoteAgents = %+v", got)
	}
}
