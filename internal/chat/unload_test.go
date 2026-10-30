package chat

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// workedConversation is n settled turns the way an agent's look: a message,
// a thought, six tool calls with a few KiB of output each, an edit, an answer.
func workedConversation(n int) []api.ChatItem {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	text := func(size int) string { return strings.Repeat("x", size) }
	var items []api.ChatItem
	add := func(it api.ChatItem) {
		at = at.Add(time.Second)
		it.CreatedAt, it.UpdatedAt = at, at
		items = append(items, it)
	}
	for i := range n {
		turn := fmt.Sprintf("u%d", i)
		add(api.ChatItem{ID: turn, Turn: turn, Kind: "user", Text: text(400), Result: &api.ChatTurnResult{State: "done", EndedAt: at}})
		add(api.ChatItem{ID: turn + "-th", Turn: turn, Kind: "thought", Text: text(2 << 10)})
		for j := range 6 {
			id := fmt.Sprintf("%s-t%d", turn, j)
			add(api.ChatItem{ID: id, Turn: turn, Kind: "tool", Tool: &api.ChatTool{CallID: id, Name: "Bash", Title: "go test ./internal/chat", Kind: "execute", Status: "completed", Output: text(3 << 10)}})
		}
		add(api.ChatItem{ID: turn + "-e", Turn: turn, Kind: "tool", Tool: &api.ChatTool{CallID: turn + "-e", Name: "Edit", Kind: "edit", Status: "completed",
			Diffs: []api.ChatDiff{{Path: "internal/chat/chat.go", OldText: text(6 << 10), NewText: text(6 << 10)}}}})
		add(api.ChatItem{ID: turn + "-said", Turn: turn, Kind: "assistant", Text: text(1500)})
	}
	return items
}

func liveHeap() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func TestUnloadLetsIdleTranscriptsGo(t *testing.T) {
	store := openStore(t)
	const agents, turns = 40, 60
	all := make([]state.Agent, agents)
	for i := range all {
		all[i] = state.Agent{Project: fmt.Sprintf("p%d", i%10), Name: fmt.Sprintf("agent-%02d", i), AI: "claude", Worktree: "/work"}
		storeConversation(t, store, all[i], workedConversation(turns))
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m := &Manager{Store: store, Now: func() time.Time { return now }}

	before := liveHeap()
	for _, a := range all {
		if _, err := m.Thread(a); err != nil {
			t.Fatal(err)
		}
	}
	loaded := liveHeap()

	if n := m.Unload(UnloadAfter); n != 0 {
		t.Fatalf("Unload right after reading every chat let %d go; want none", n)
	}
	now = now.Add(UnloadAfter + time.Second)
	// One chat read again just now stays.
	if _, err := m.Thread(all[0]); err != nil {
		t.Fatal(err)
	}
	if n := m.Unload(UnloadAfter); n != agents-1 {
		t.Fatalf("Unload let %d chats go; want the %d idle ones", n, agents-1)
	}
	unloaded := liveHeap()
	t.Logf("%d chats of %d turns: %.1f MiB live with every transcript loaded, %.1f MiB after Unload (%.1f MiB before reading any)",
		agents, turns, float64(loaded)/(1<<20), float64(unloaded)/(1<<20), float64(before)/(1<<20))
	if loaded-before < 20<<20 || unloaded > before+(loaded-before)/10 {
		t.Errorf("heap %d before, %d loaded, %d unloaded: Unload should give back most of what loading took", before, loaded, unloaded)
	}

	// Read again, it is whole.
	th, err := m.Thread(all[5])
	if err != nil {
		t.Fatal(err)
	}
	if want := turns * 10; len(th.Items) != want || th.Items[len(th.Items)-1].ID != fmt.Sprintf("u%d-said", turns-1) {
		t.Fatalf("after Unload, Thread() = %d items; want all %d back from the store", len(th.Items), want)
	}
}

func TestUnloadKeepsAConversationWithWorkUnderWay(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	storeConversation(t, store, testAgent, longConversation(3))
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m := &Manager{Store: store, Now: func() time.Time { return now }}
	if _, err := m.Thread(testAgent); err != nil {
		t.Fatal(err)
	}
	c := m.existing(testAgent.Ref())
	c.mu.Lock()
	c.queued = []queuedNotice{{text: "later"}}
	c.mu.Unlock()
	now = now.Add(time.Hour)
	if n := m.Unload(UnloadAfter); n != 0 {
		t.Fatalf("Unload let go of a chat with a notice waiting")
	}
	c.mu.Lock()
	c.queued = nil
	c.mu.Unlock()
	if n := m.Unload(UnloadAfter); n != 1 {
		t.Fatalf("Unload = %d once nothing waits; want 1", n)
	}
}
