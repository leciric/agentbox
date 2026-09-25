package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// A rollover replaces the session and leaves the conversation alone: that is
// the whole difference from Clear. What the user has on screen, and what the
// store holds, carry on down the page with one notice where the session
// changed — and the next turn starts a new session, because the stored id is
// gone.
func TestRolloverKeepsTheConversationAndStartsANewSession(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))

	if err := m.Rollover(testAgent); err != nil {
		t.Fatal(err)
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant", "notice"}) {
		t.Errorf("items %v, want the conversation with a notice after it", got)
	}
	if text := find(th, "notice", 0).Text; text != RolloverNotice {
		t.Errorf("notice = %q, want %q", text, RolloverNotice)
	}
	if th.Session.State != api.ChatOff {
		t.Errorf("session state = %q, want %q", th.Session.State, api.ChatOff)
	}
	if th.Session.ContextUsed != 0 || th.Session.ContextSize != 0 {
		t.Errorf("context = %d/%d, want the new session's unknown 0/0", th.Session.ContextUsed, th.Session.ContextSize)
	}
	// The conversation is still in the store: nothing was cleared.
	rows, err := store.ChatItems(context.Background(), testAgent.Project, testAgent.Name)
	if err != nil || len(rows) != 3 {
		t.Fatalf("stored items = %d, %v; want the 3 of the conversation", len(rows), err)
	}
	stored, err := store.Chat(context.Background(), testAgent.Project, testAgent.Name)
	if err != nil || stored.SessionID != "" {
		t.Fatalf("stored session = %q, %v; want it cleared", stored.SessionID, err)
	}

	if _, err := m.Send(testAgent, "and again"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the second turn", turnsEnded(2))
	if n := len(f.called(acp.MethodSessionNew)); n != 2 {
		t.Errorf("%d sessions started, want 2: the turn after a rollover starts a fresh one", n)
	}
	if n := len(f.called(acp.MethodSessionResume)); n != 0 {
		t.Errorf("%d sessions resumed, want none: the rolled-over session is gone", n)
	}
}

// The consolidation is a prompt the user never sees: it runs on the session
// that is about to go, and what it says comes back to the caller instead of
// into the conversation.
func TestConsolidationSaysNothingInTheConversation(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))

	f.setTurn(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		if !strings.Contains(text, "AgentBox") {
			t.Errorf("the hidden prompt was %q", text)
		}
		f.update(sessionID, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"{\"summary\":\"we "}}`)
		f.update(sessionID, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"talked\"}"}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	var got string
	if err := m.Compact(context.Background(), testAgent, "[AgentBox] sum it up", func(answer string, askErr error) error {
		got = answer
		return askErr
	}); err != nil {
		t.Fatal(err)
	}
	if want := `{"summary":"we talked"}`; got != want {
		t.Errorf("the consolidation answered %q, want %q", got, want)
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if kinds := kinds(th); !slices.Equal(kinds, []string{"user", "assistant", "notice"}) {
		t.Errorf("items %v: the hidden prompt left something in the conversation", kinds)
	}
	if text := find(th, "assistant", 0).Text; text != "Hello" {
		t.Errorf("the assistant item says %q, want only what the turn said", text)
	}
	rows, err := store.ChatItems(context.Background(), testAgent.Project, testAgent.Name)
	if err != nil || len(rows) != 3 {
		t.Fatalf("stored items = %d, %v; want 3", len(rows), err)
	}
}

// A session that can't summarise itself is exactly the session that most needs
// replacing, so the rollover happens anyway and the caller is told why it has
// nothing to store.
func TestAFailedConsolidationStillRollsOver(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))

	f.mu.Lock()
	f.promptErr = &acp.Error{Code: acp.CodeInternalError, Message: "the context is full"}
	f.mu.Unlock()
	settled, told := false, error(nil)
	err := m.Compact(context.Background(), testAgent, "[AgentBox] sum it up", func(answer string, askErr error) error {
		settled, told = true, askErr
		return nil
	})
	if err != nil {
		t.Fatalf("Compact() = %v, want the rollover to have happened", err)
	}
	if !settled || told == nil {
		t.Errorf("settle(%v): it should be called, and told what went wrong", told)
	}
	stored, err := store.Chat(context.Background(), testAgent.Project, testAgent.Name)
	if err != nil || stored.SessionID != "" {
		t.Fatalf("stored session = %q, %v; want it rolled over regardless", stored.SessionID, err)
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if text := find(th, "notice", 0).Text; text != RolloverNotice {
		t.Errorf("notice = %q, want the conversation to say it carried on", text)
	}
}

// A notice that arrives while the session is being replaced waits for it, the
// way it waits for a running turn: delivering it mid-rollover would start a
// turn on the session that is about to be thrown away.
func TestANoticeWaitsForARollover(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))

	err := m.Compact(context.Background(), testAgent, "[AgentBox] sum it up", func(answer string, askErr error) error {
		// Mid-rollover: the summary is written and the brief rewritten here,
		// and this is where an agent finishing would reach the chat.
		if err := m.Notice(testAgent, "agent-02 finished", NoticeOptions{Hidden: true}); err != nil {
			t.Errorf("Notice() = %v", err)
		}
		th, err := m.Thread(testAgent)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(kinds(th), "notice") {
			t.Errorf("the notice landed during the rollover: %v", kinds(th))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant", "notice", "notice"}) {
		t.Fatalf("items %v, want the rollover's notice and then the one that waited", got)
	}
	if text := find(th, "notice", 1).Text; text != "agent-02 finished" {
		t.Errorf("the second notice says %q, want the one that waited", text)
	}
}

// A chat with a turn running is not one to replace underneath: Compact says so
// rather than cutting the turn short.
func TestCompactWaitsForARunningTurn(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	release := make(chan struct{})
	f := newFakeTool(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	defer close(release)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool {
		return th.Session.State == api.ChatRunning
	})
	if err := m.Compact(context.Background(), testAgent, "sum it up", func(string, error) error {
		t.Error("the conversation was consolidated while a turn was running")
		return nil
	}); !errors.Is(err, ErrBusy) {
		t.Errorf("Compact() = %v, want ErrBusy", err)
	}
	if err := m.Rollover(testAgent); !errors.Is(err, ErrBusy) {
		t.Errorf("Rollover() = %v, want ErrBusy", err)
	}
	if stored, _ := store.Chat(context.Background(), testAgent.Project, testAgent.Name); stored.SessionID == "" {
		t.Error("the session was rolled over under a running turn")
	}
}
