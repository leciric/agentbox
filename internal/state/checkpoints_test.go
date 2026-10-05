package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentbox/internal/state"
)

// TestCheckpoints: a turn's checkpoint taken again replaces the old one, the
// list is oldest first, and the agent's going takes them with it.
func TestCheckpoints(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	cp := func(id string, n int, at time.Time) state.Checkpoint {
		return state.Checkpoint{Project: "p", Agent: "agent-01", ID: id, Kind: state.CheckpointTurn, Turn: "t" + id, Number: n,
			Ref: "refs/agentbox/snapshots/agent-01/" + id, Commit: "c-" + id, Head: "h", CreatedAt: at}
	}
	for _, c := range []state.Checkpoint{cp("turn-1", 1, now), cp("turn-2", 2, now.Add(time.Second)), cp("turn-1", 1, now.Add(2*time.Second))} {
		if err := st.SaveCheckpoint(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.Checkpoints(ctx, "p", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "turn-2" || list[1].ID != "turn-1" || !list[1].CreatedAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("Checkpoints() = %+v, want turn-2 then the retaken turn-1", list)
	}
	got, err := st.Checkpoint(ctx, "p", "agent-01", "turn-2")
	if err != nil || got.Commit != "c-turn-2" || got.Number != 2 || got.Turn != "tturn-2" {
		t.Fatalf("Checkpoint(turn-2) = %+v, %v", got, err)
	}
	if _, err := st.Checkpoint(ctx, "p", "agent-01", "turn-9"); !errors.Is(err, state.ErrNoCheckpoint) {
		t.Fatalf("Checkpoint(turn-9) error = %v, want ErrNoCheckpoint", err)
	}
	if err := st.DeleteCheckpoint(ctx, "p", "agent-01", "turn-2"); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveAgent(ctx, "p", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.Checkpoints(ctx, "p", "agent-01"); len(list) != 0 {
		t.Fatalf("after RemoveAgent: %+v", list)
	}
}

// TestChatHandoffAndTruncate: the handoff a rollback leaves is kept with the
// chat until cleared, and truncating keeps the items before the cut.
func TestChatHandoffAndTruncate(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if err := st.SaveChat(ctx, "p", "a", state.Chat{SessionID: "s", Handoff: "what happened"}); err != nil {
		t.Fatal(err)
	}
	if c, err := st.Chat(ctx, "p", "a"); err != nil || c.Handoff != "what happened" {
		t.Fatalf("Chat() = %+v, %v", c, err)
	}
	var items []state.ChatItem
	for i, id := range []string{"a", "b", "c"} {
		items = append(items, state.ChatItem{ID: id, Position: int64(i), Data: []byte(`{}`)})
	}
	if err := st.SaveChatItems(ctx, "p", "a", items); err != nil {
		t.Fatal(err)
	}
	if err := st.TruncateChatItems(ctx, "p", "a", 2); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ChatItems(ctx, "p", "a"); len(got) != 2 || got[1].ID != "b" {
		t.Fatalf("after truncating: %+v", got)
	}
	if err := st.ClearChat(ctx, "p", "a"); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.Chat(ctx, "p", "a"); c.Handoff != "" {
		t.Fatalf("ClearChat kept the handoff %q", c.Handoff)
	}
}
