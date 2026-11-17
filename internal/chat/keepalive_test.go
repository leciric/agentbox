package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A keepalive leaves nothing in the conversation, doesn't make the chat read
// busy, and its tokens are booked apart from turns.
func TestAKeepaliveIsHiddenAndBookedApart(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testLead, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the turn", turnsEnded(1))

	asked := make(chan string, 1)
	cost := quotaTurn(0.01)
	f.setTurn(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		asked <- text
		if got := m.State(testLead.Ref()); got != api.ChatReady {
			t.Errorf("during a keepalive the chat reads %q, want ready", got)
		}
		return cost(f, sessionID, text)
	})
	if err := m.Keepalive(context.Background(), testLead); err != nil {
		t.Fatal(err)
	}
	if text := <-asked; text != KeepalivePrompt {
		t.Errorf("the keepalive asked %q", text)
	}
	th, err := m.Thread(testLead)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant"}) {
		t.Errorf("after a keepalive the conversation is %v", got)
	}
	if text := find(th, "assistant", 0).Text; text != "Hello" {
		t.Errorf("the keepalive's answer reached the conversation: %q", text)
	}
	var kept int
	for _, r := range ledger(t, store, 2) {
		if r.Kind == state.TokensKeepalive {
			kept++
		}
	}
	if kept != 2 { // the quota's two models
		t.Errorf("the ledger has %d keepalive rows, want 2", kept)
	}
}

// A message the user sends while a keepalive is in flight waits for it, and
// then starts its own turn: it doesn't race the keepalive on the session.
func TestAMessageSentDuringAKeepaliveWaitsForIt(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testLead, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the turn", turnsEnded(1))

	inFlight, release := make(chan struct{}), make(chan struct{})
	prompts := make(chan string, 4)
	f.setTurn(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		prompts <- text
		if text == KeepalivePrompt {
			close(inFlight)
			<-release
			return acp.PromptResponse{StopReason: "end_turn"}
		}
		return answerHello(f, sessionID, text)
	})
	done := make(chan error, 1)
	go func() { done <- m.Keepalive(context.Background(), testLead) }()
	<-inFlight

	it, err := m.SendMessage(testLead, api.ChatMessageRequest{Text: "are they done?"})
	if err != nil {
		t.Fatal(err)
	}
	if it.Delivery != api.ChatAsideWaiting {
		t.Errorf("a message sent during a keepalive is %q, want waiting", it.Delivery)
	}
	if got := len(f.called(acp.MethodSessionPrompt)); got != 2 {
		t.Errorf("%d prompts reached the tool while the keepalive was in flight, want 2 (the turn and the keepalive)", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testLead, "the message's turn", turnsEnded(2))
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant", "user", "assistant"}) {
		t.Errorf("the conversation is %v", got)
	}
	if u := find(th, "user", 1); u.Text != "are they done?" || u.Delivery != "" {
		t.Errorf("the waiting message became %+v", u)
	}
	if got := <-prompts; got != KeepalivePrompt {
		t.Errorf("the first prompt was %q, want the keepalive", got)
	}
	if got := <-prompts; !strings.Contains(got, "are they done?") {
		t.Errorf("the second prompt was %q, want the message", got)
	}
}

// A keepalive the caller stops waiting for is cancelled on the session too,
// so its late answer can't land in the next turn or wake the chat by itself.
func TestATimedOutKeepaliveIsCancelled(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testLead, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the turn", turnsEnded(1))

	cancelled := make(chan struct{})
	f.setTurn(func(f *fakeTool, _, _ string) acp.PromptResponse {
		<-f.cancels
		close(cancelled)
		return acp.PromptResponse{StopReason: "cancelled"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := m.Keepalive(ctx, testLead); err == nil {
		t.Fatal("a keepalive that timed out reported no error")
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the timed-out keepalive was never cancelled on the session")
	}
}

// A keepalive that wakes an asleep lead only reads the cache back if the woken
// tool sends the prefix the session had. AgentBox's half of that: the wake
// resumes the same session with what the session was started with — the same
// directory, no MCP servers or system prompt of its own (the tool's
// configuration, which nothing on the wake writes, gives those), the same
// options — and it runs no turn, so nothing before it re-renders the brief or
// moves the worktree (EnsureLead and SyncLead run on a message, not here).
// Claude Code's half is that a resumed session keeps the context it announced
// in its transcript and appends what changed, rather than re-rendering it.
func TestAKeepaliveWakesAnAsleepLeadOnTheSameSession(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, clock := idleManager(t, f)
	idleTurn(t, m, clock, testLead, "hi", 1)
	clock.advance(time.Second)
	clock.advance(15 * time.Minute)
	if !m.Asleep(testLead.Ref()) {
		t.Fatal("the chat didn't sleep")
	}
	if err := m.Keepalive(context.Background(), testLead); err != nil {
		t.Fatalf("a keepalive on an asleep lead: %v", err)
	}

	news, resumes := f.called(acp.MethodSessionNew), f.called(acp.MethodSessionResume)
	if len(news) != 1 || len(resumes) != 1 {
		t.Fatalf("%d session/new and %d session/resume, want one of each", len(news), len(resumes))
	}
	var started, woke map[string]json.RawMessage
	if err := json.Unmarshal(news[0], &started); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(resumes[0], &woke); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"cwd", "mcpServers", "_meta", "systemPrompt"} {
		if string(started[field]) != string(woke[field]) {
			t.Errorf("the wake resumed with %s %s, the session started with %s", field, woke[field], started[field])
		}
	}
	if string(woke["mcpServers"]) != "[]" {
		t.Errorf("the wake passed MCP servers of its own: %s", woke["mcpServers"])
	}
	th, err := m.Thread(testLead)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant"}) {
		t.Errorf("waking for a keepalive left %v in the conversation", got)
	}
	if got := len(f.called(acp.MethodSessionPrompt)); got != 2 {
		t.Errorf("%d prompts, want the turn and the keepalive", got)
	}
}
