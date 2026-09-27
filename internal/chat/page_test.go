package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// storeConversation saves items as a conversation the chat reads on load.
func storeConversation(t *testing.T, store *state.Store, a state.Agent, items []api.ChatItem) {
	t.Helper()
	rows := make([]state.ChatItem, 0, len(items))
	for i, it := range items {
		data, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, state.ChatItem{ID: it.ID, Position: int64(i), Data: data})
	}
	if err := store.SaveChatItems(context.Background(), a.Project, a.Name, rows); err != nil {
		t.Fatal(err)
	}
}

// longConversation is n settled turns, each of a message, two tool calls, a
// subagent with its own work, and an answer.
func longConversation(n int) []api.ChatItem {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var items []api.ChatItem
	add := func(it api.ChatItem) {
		at = at.Add(time.Second)
		it.CreatedAt, it.UpdatedAt = at, at
		items = append(items, it)
	}
	for i := range n {
		turn := fmt.Sprintf("u%d", i)
		add(api.ChatItem{ID: turn, Turn: turn, Kind: "user", Text: "do it", Result: &api.ChatTurnResult{State: "done", EndedAt: at}})
		add(api.ChatItem{ID: turn + "-t1", Turn: turn, Kind: "tool", Tool: &api.ChatTool{CallID: turn + "-c1", Status: "completed"}})
		add(api.ChatItem{ID: turn + "-sub", Turn: turn, Kind: "subagent", Subagent: &api.ChatSubagent{State: "done"}})
		add(api.ChatItem{ID: turn + "-sub-said", Turn: turn, Kind: "assistant", Parent: turn + "-sub", Text: "found it"})
		add(api.ChatItem{ID: turn + "-t2", Turn: turn, Kind: "tool", Tool: &api.ChatTool{CallID: turn + "-c2", Status: "completed"}})
		add(api.ChatItem{ID: turn + "-said", Turn: turn, Kind: "assistant", Text: "done"})
	}
	return items
}

func ids(items []api.ChatItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestPagesWalkBackThroughTheWholeConversation(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	all := longConversation(25)
	storeConversation(t, store, testAgent, all)
	m := &Manager{Store: store}

	whole, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole.Items) != len(all) || whole.Older {
		t.Fatalf("Thread() = %d items, older %v; want all %d and nothing older", len(whole.Items), whole.Older, len(all))
	}

	latest, err := m.Page(testAgent, "", 6)
	if err != nil {
		t.Fatal(err)
	}
	// Six messages are three turns of a message and an answer; the
	// subagent's words are under its card, and don't count.
	if got, want := ids(latest.Items), ids(all[len(all)-18:]); !slices.Equal(got, want) {
		t.Fatalf("the latest page = %v, want %v", got, want)
	}
	if !latest.Older || latest.Seq != whole.Seq || latest.Session.State != whole.Session.State {
		t.Errorf("the latest page: older %v, seq %d, session %q; want older, and the thread's seq and session", latest.Older, latest.Seq, latest.Session.State)
	}

	// Walking back a page at a time gets every item once, in order.
	got := latest.Items
	pages := 1
	for page := latest; page.Older; pages++ {
		page, err = m.Page(testAgent, page.Items[0].ID, 6)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 0 || page.Items[0].Kind != "user" {
			t.Fatalf("page %d doesn't start at a turn: %v", pages+1, ids(page.Items))
		}
		got = append(page.Items, got...)
	}
	if !slices.Equal(ids(got), ids(all)) {
		t.Errorf("the pages together = %v, want the conversation %v", ids(got), ids(all))
	}
	if pages != 9 {
		t.Errorf("25 turns came in %d pages of 3, want 9", pages)
	}

	// Without a limit, before is everything before it.
	head, err := m.Page(testAgent, "u2", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(head.Items), ids(all[:12])) || head.Older {
		t.Errorf("Page(before u2) = %v, older %v; want the first two turns and nothing older", ids(head.Items), head.Older)
	}

	// An item the conversation doesn't have, because it was cleared, is an
	// empty page.
	gone, err := m.Page(testAgent, "nowhere", 6)
	if err != nil || len(gone.Items) != 0 || gone.Older {
		t.Errorf("Page(before an unknown item) = %v, older %v, %v; want an empty page", ids(gone.Items), gone.Older, err)
	}
}

// A page never splits a turn, however much work the turn did, and what the
// app doesn't show doesn't count towards it.
func TestAPageHoldsWholeTurnsOfShownMessages(t *testing.T) {
	t.Parallel()
	item := func(id, kind string) *api.ChatItem { return &api.ChatItem{ID: id, Kind: kind} }
	items := []*api.ChatItem{item("u0", "user"), item("a0", "assistant")}
	items = append(items, item("u1", "user"))
	for i := range 200 {
		items = append(items, item(fmt.Sprintf("t%d", i), "tool"))
	}
	items = append(items, item("a1", "assistant"))
	// A lead's turns for its agents' notices are hidden, both halves.
	notice := item("n", "user")
	notice.Hidden = true
	hiddenSaid := item("n-said", "assistant")
	hiddenSaid.Hidden = true
	items = append(items, notice, hiddenSaid)

	start, end := pageOf(items, "", 1)
	if start != 2 || end != len(items) {
		t.Errorf("pageOf(limit 1) = [%d:%d], want the whole last shown turn [2:%d]", start, end, len(items))
	}
	if start, end := pageOf(items, "u1", 5); start != 0 || end != 2 {
		t.Errorf("pageOf(before u1) = [%d:%d], want [0:2]", start, end)
	}
	// An aside counts: it is a message you sent, into the running turn.
	items = append(items, &api.ChatItem{ID: "x", Kind: "aside"})
	if start, _ := pageOf(items, "", 1); items[start].ID != "n" {
		t.Errorf("the page for one message starts at %s, want the turn the aside joined, n", items[start].ID)
	}
}
