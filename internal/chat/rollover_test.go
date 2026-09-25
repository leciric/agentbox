package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
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
	if kinds := kinds(th); !slices.Equal(kinds, []string{"user", "assistant", "compaction"}) {
		t.Errorf("items %v: the hidden prompt left something in the conversation beyond its card", kinds)
	}
	if card := find(th, "compaction", 0); card.Compaction == nil || card.Compaction.State != api.ChatCompactionDone || card.Text != RolloverNotice {
		t.Errorf("card = %+v %q, want done, saying %q", card.Compaction, card.Text, RolloverNotice)
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
		return askErr
	})
	if !errors.Is(err, told) || errors.Is(err, ErrBusy) {
		t.Fatalf("Compact() = %v, want only what settle said: the rollover happened", err)
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
	card := find(th, "compaction", 0)
	if card.Compaction == nil || card.Compaction.State != api.ChatCompactionFailed || !strings.Contains(card.Compaction.Error, "the context is full") {
		t.Errorf("card = %+v, want failed, saying why", card.Compaction)
	}
	if !strings.Contains(card.Text, "fresh session") {
		t.Errorf("card says %q, want the conversation to say it carried on", card.Text)
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
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant", "compaction", "notice"}) {
		t.Fatalf("items %v, want the rollover's card and then the notice that waited", got)
	}
	if text := find(th, "notice", 0).Text; text != "agent-02 finished" {
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

// The card is up before the consolidation starts and says it runs, so the user
// sees the compaction as it happens rather than only once it is over.
func TestTheCompactionCardRunsLive(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, rec := newManager(t, store, f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))

	finish, err := m.BeginCompact(context.Background(), testAgent, "[AgentBox] sum it up", func(string, error) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	card := find(th, "compaction", 0)
	if card.Compaction == nil || card.Compaction.State != api.ChatCompactionRunning {
		t.Fatalf("card = %+v, want it running before the consolidation does", card.Compaction)
	}
	if !slices.ContainsFunc(rec.all(), func(ev api.ChatEvent) bool {
		return ev.Item != nil && ev.Item.ID == card.ID && ev.Item.Compaction.State == api.ChatCompactionRunning
	}) {
		t.Error("the running card wasn't published")
	}
	if _, err := m.BeginCompact(context.Background(), testAgent, "again", func(string, error) error { return nil }); !errors.Is(err, ErrBusy) {
		t.Errorf("a second BeginCompact() = %v, want ErrBusy", err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	th, _ = m.Thread(testAgent)
	if got := find(th, "compaction", 0); got.ID != card.ID || got.Compaction.State != api.ChatCompactionDone {
		t.Errorf("card = %+v, want the same card, done", got.Compaction)
	}
}

// A message sent while the chat compacts is held, not dropped and not sent to
// the session being thrown away: it waits under the card, and becomes the
// fresh session's first turn once the roll is done.
func TestAMessageSentMidCompactionGoesToTheFreshSession(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))

	release := make(chan struct{})
	var mu sync.Mutex
	var prompts []string // session: text, of every prompt from here on
	f.setTurn(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		mu.Lock()
		prompts = append(prompts, sessionID+": "+text)
		mu.Unlock()
		if strings.HasPrefix(text, "[AgentBox] sum") {
			<-release
			f.update(sessionID, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"{\"summary\":\"s\"}"}}`)
		} else {
			f.update(sessionID, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Fresh"}}`)
		}
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	finish, err := m.BeginCompact(context.Background(), testAgent, "[AgentBox] sum it up", func(string, error) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- finish() }()

	held, err := m.Send(testAgent, "what next?")
	if err != nil {
		t.Fatal(err)
	}
	if held.Kind != "aside" || held.Delivery != api.ChatAsideHeld {
		t.Errorf("the message came back as %s/%s, want a held aside", held.Kind, held.Delivery)
	}
	th, _ := m.Thread(testAgent)
	if card := find(th, "compaction", 0); card.Compaction.Waiting != 1 {
		t.Errorf("the card counts %d waiting, want 1", card.Compaction.Waiting)
	}
	if th.Session.State == api.ChatRunning {
		t.Error("a turn started while the chat compacted")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, testAgent, "the held message's turn", turnsEnded(2))

	mu.Lock()
	got := slices.Clone(prompts)
	mu.Unlock()
	if want := []string{"session-1: [AgentBox] sum it up", "session-2: what next?"}; !slices.Equal(got, want) {
		t.Errorf("prompts %q, want the consolidation on the old session and the message on the fresh one", got)
	}
	if k := kinds(th); !slices.Equal(k, []string{"user", "assistant", "compaction", "user", "assistant"}) {
		t.Errorf("items %v, want the held message as the turn after the card", k)
	}
	if card := find(th, "compaction", 0); card.Compaction.State != api.ChatCompactionDone {
		t.Errorf("card = %+v, want done", card.Compaction)
	}
	if msg := find(th, "user", 1); msg.ID != held.ID || msg.Delivery != "" || msg.Text != "what next?" {
		t.Errorf("the turn's message is %+v, want the held one, delivered", msg)
	}
}

// Idle is the daemon's cue to compact without anyone waiting: it fires once a
// turn has ended with nothing following it.
func TestIdleFiresWhenATurnEnds(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	idle := make(chan state.Agent, 1)
	m.Idle = func(a state.Agent) { idle <- a }
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-idle:
		if a.Ref() != testAgent.Ref() {
			t.Errorf("Idle(%s), want %s", a.Ref(), testAgent.Ref())
		}
		if _, _, ready := m.Context(testAgent.Ref()); !ready {
			t.Error("Idle fired, but the chat isn't ready to compact")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Idle never fired")
	}
}

// A card a previous daemon left running can't be finished by this one: it is
// shown as failed, the way a turn left running is.
func TestARunningCardFromAnotherDaemonFails(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))
	if _, err := m.BeginCompact(context.Background(), testAgent, "sum it up", func(string, error) error { return nil }); err != nil {
		t.Fatal(err)
	}
	m.Close()

	m2, _ := newManager(t, store, newFakeTool(answerHello))
	th, err := m2.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if card := find(th, "compaction", 0); card.Compaction == nil || card.Compaction.State != api.ChatCompactionFailed {
		t.Errorf("card = %+v, want failed", card.Compaction)
	}
}
