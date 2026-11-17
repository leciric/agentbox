package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// A message is published before anything about it is stored: with the
// database held by another writer (another chat saving, memory
// consolidating), the app still hears of it, and of the turn it starts, at
// once. And a chat streaming a reply stores it without holding the
// conversation, so a message sent meanwhile isn't kept waiting either.
func TestAMessageIsPublishedWhileTheDatabaseIsBusy(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	release := make(chan struct{})
	f := newFakeTool(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		for text == "Start" {
			select {
			case <-release:
				return acp.PromptResponse{StopReason: "end_turn"}
			case <-time.After(20 * time.Millisecond):
				f.update(sessionID, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"working "}}`)
			}
		}
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, rec := newManager(t, store, f)

	ctx := context.Background()
	conn, err := store.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	hold := func() (unlock func()) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			t.Fatal(err)
		}
		return func() {
			if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
				t.Fatal(err)
			}
		}
	}
	published := func(kind, text string) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			for _, ev := range rec.all() {
				if ev.Item != nil && ev.Item.Kind == kind && ev.Item.Text == text {
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("%s %q wasn't published while the database was busy", kind, text)
	}
	send := func(text string) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := m.Send(testAgent, text)
			done <- err
		}()
		return done
	}

	// Into an idle chat: it starts a turn.
	unlock := hold()
	sent := send("Start")
	published("user", "Start")
	unlock()
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to stream", func(th api.ChatThread) bool { return find(th, "assistant", 0).Text != "" })

	// Into the running turn, after the stream's next save has come due.
	unlock = hold()
	time.Sleep(saveInterval + 200*time.Millisecond)
	sent = send("also this")
	published("aside", "also this")
	unlock()
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	close(release)
	waitThread(t, m, testAgent, "the turns to end", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })

	// Nothing was lost to the saves that waited.
	m.Close()
	m.Wait()
	stored, err := store.ChatItems(ctx, testAgent.Project, testAgent.Name)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]bool{}
	for _, it := range stored {
		var item api.ChatItem
		if err := json.Unmarshal(it.Data, &item); err != nil {
			t.Fatal(err)
		}
		texts[item.Text] = true
	}
	for _, want := range []string{"Start", "also this"} {
		if !texts[want] {
			t.Errorf("stored %v, want %q among them", texts, want)
		}
	}
}
