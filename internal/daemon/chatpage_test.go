package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The app reads a chat a page at a time, through the query of its chat route;
// without one, the route is still the whole conversation.
func TestChatIsReadAPageAtATime(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	var rows []state.ChatItem
	for i := range 10 {
		for j, kind := range []string{"user", "assistant"} {
			id := fmt.Sprintf("m%d-%d", i, j)
			it := api.ChatItem{ID: id, Turn: fmt.Sprintf("m%d-0", i), Kind: kind, Text: "hi"}
			if kind == "user" {
				it.Result = &api.ChatTurnResult{State: "done"}
			}
			data, _ := json.Marshal(it)
			rows = append(rows, state.ChatItem{ID: id, Position: int64(len(rows)), Data: data})
		}
	}
	if err := d.srv.store.SaveChatItems(ctx, "hello-stack", state.LeadName, rows); err != nil {
		t.Fatal(err)
	}

	whole, err := d.client.Chat(ctx, "hello-stack")
	if err != nil || len(whole.Items) != 20 || whole.Older {
		t.Fatalf("Chat() = %d items, older %v, %v; want all 20", len(whole.Items), whole.Older, err)
	}
	latest, err := d.client.ChatPage(ctx, "hello-stack", "", 4)
	if err != nil || len(latest.Items) != 4 || latest.Items[0].ID != "m8-0" || !latest.Older {
		t.Fatalf("ChatPage(limit 4) = %+v, %v; want the last two turns, and more", latest.Items, err)
	}
	first, err := d.client.ChatPage(ctx, "hello-stack", "m1-0", 4)
	if err != nil || len(first.Items) != 2 || first.Items[0].ID != "m0-0" || first.Older {
		t.Fatalf("ChatPage(before m1-0) = %+v, %v; want the first turn, and nothing older", first.Items, err)
	}
	if _, err := d.client.ChatPage(ctx, "hello-stack", "", -1); err == nil {
		t.Error("a limit below 0 was taken")
	}
}
