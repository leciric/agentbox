package chat

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// idleManager is a chat manager whose chats sleep after 15 idle minutes, on a
// clock the test moves itself.
func idleManager(t *testing.T, f *fakeTool) (*Manager, *fakeClock) {
	t.Helper()
	m, clock := limitManager(t, f)
	m.IdleStop = func(state.Agent) (time.Duration, bool) { return 15 * time.Minute, true }
	return m, clock
}

// idleTurn sends a message and waits for its turn to end, and for the
// adapter's idle wait to have started.
func idleTurn(t *testing.T, m *Manager, clock *fakeClock, a state.Agent, text string, n int) {
	t.Helper()
	if _, err := m.Send(a, text); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the turn", turnsEnded(n))
	waitFor(t, "the idle wait", func() bool { _, ok := clock.waiting(); return ok })
}

// TestAnIdleLeadSleepsAndResumesOnItsNextMessage: after 15 idle minutes the
// lead's adapter is stopped, the chat still reads as ready, and the next
// message starts it again on the same session.
func TestAnIdleLeadSleepsAndResumesOnItsNextMessage(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, clock := idleManager(t, f)
	idleTurn(t, m, clock, testLead, "hi", 1)

	clock.advance(time.Second)
	clock.advance(14 * time.Minute)
	if m.Asleep(testLead.Ref()) {
		t.Fatal("the chat slept after 14 idle minutes")
	}
	clock.advance(time.Minute)
	if !m.Asleep(testLead.Ref()) {
		t.Fatal("the chat didn't sleep after 15 idle minutes")
	}
	th, err := m.Thread(testLead)
	if err != nil {
		t.Fatal(err)
	}
	if th.Session.State != api.ChatReady {
		t.Errorf("an asleep chat reads as %q, want ready", th.Session.State)
	}
	if n := len(th.Items); n != 2 {
		t.Errorf("sleeping added to the conversation: %s", summary(th))
	}
	if _, _, ready := m.Context(testLead.Ref()); !ready {
		t.Error("an asleep chat isn't ready for a compaction")
	}

	if _, err := m.Send(testLead, "still there?"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the second turn", turnsEnded(2))
	if n := len(f.launchedOn()); n != 2 {
		t.Errorf("the adapter started %d times, want 2", n)
	}
	resumed := f.called(acp.MethodSessionResume)
	if len(resumed) != 1 || !strings.Contains(string(resumed[0]), `"sessionId":"session-1"`) || len(f.called(acp.MethodSessionNew)) != 1 {
		t.Errorf("resumed %s, started %d new sessions", resumed, len(f.called(acp.MethodSessionNew)))
	}
	if m.Asleep(testLead.Ref()) {
		t.Error("the chat still reads as asleep after its turn")
	}
}

// TestAWakingNoticeResumesAnAsleepLead: an agent's finish, a schedule or a
// project's message reaches the lead as a notice that acts, and that wakes it
// too.
func TestAWakingNoticeResumesAnAsleepLead(t *testing.T) {
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
	if err := m.Notice(testLead, "agent-01 finished", NoticeOptions{Act: true, Hidden: true}); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the notice's turn", turnsEnded(2))
	if n := len(f.called(acp.MethodSessionResume)); n != 1 {
		t.Errorf("resumed %d times, want 1", n)
	}
}

// TestActivityPutsOffSleeping: the wait counts from the last thing the chat
// did, not from when the adapter started.
func TestActivityPutsOffSleeping(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, clock := idleManager(t, f)
	idleTurn(t, m, clock, testLead, "hi", 1)
	clock.advance(time.Second)
	clock.advance(10 * time.Minute)
	idleTurn(t, m, clock, testLead, "again", 2)
	clock.advance(5 * time.Minute)
	if m.Asleep(testLead.Ref()) {
		t.Fatal("the chat slept 5 minutes after its last turn")
	}
	clock.advance(10 * time.Minute)
	if !m.Asleep(testLead.Ref()) {
		t.Fatal("the chat didn't sleep 15 minutes after its last turn")
	}
}

// TestWhatKeepsAChatAwake: a tool that can't resume, a chat the setting
// doesn't reach, one with the setting off, a schedule due soon, and work
// left running in the background all keep the adapter up.
func TestWhatKeepsAChatAwake(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		setup   func(f *fakeTool, m *Manager)
		turn    func(f *fakeTool, sessionID, text string) acp.PromptResponse
		applies bool
	}{
		{name: "no resume", setup: func(f *fakeTool, _ *Manager) { f.resume = false }},
		{name: "not a lead", setup: func(_ *fakeTool, m *Manager) {
			m.IdleStop = func(state.Agent) (time.Duration, bool) { return 0, false }
		}},
		{name: "turned off", setup: func(_ *fakeTool, m *Manager) {
			m.IdleStop = func(state.Agent) (time.Duration, bool) { return 0, true }
		}},
		{name: "schedule due", setup: func(_ *fakeTool, m *Manager) {
			m.StayUp = func(state.Agent) bool { return true }
		}},
		{name: "background work", turn: watchingTurn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			turn := tc.turn
			if turn == nil {
				turn = answerHello
			}
			f := newFakeTool(turn)
			f.resume = true
			m, clock := idleManager(t, f)
			if tc.setup != nil {
				tc.setup(f, m)
			}
			if _, err := m.Send(testLead, "hi"); err != nil {
				t.Fatal(err)
			}
			waitThread(t, m, testLead, "the turn", turnsEnded(1))
			clock.advance(time.Second)
			clock.advance(time.Hour)
			clock.advance(time.Hour)
			if m.Asleep(testLead.Ref()) {
				t.Fatal("the chat slept")
			}
			if th, _ := m.Thread(testLead); th.Session.State != api.ChatReady {
				t.Errorf("the chat is %q, want ready", th.Session.State)
			}
			if n := len(f.launchedOn()); n != 1 {
				t.Errorf("the adapter started %d times, want 1", n)
			}
		})
	}
}

// TestACompactionWakesAnAsleepLead: the cache card's "compact first" and a
// full context's compaction run a hidden prompt on the session, which wakes
// it rather than finding no session.
func TestACompactionWakesAnAsleepLead(t *testing.T) {
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
	answer, err := m.Ask(context.Background(), testLead, "summarise")
	if err != nil {
		t.Fatalf("asking an asleep chat: %v", err)
	}
	if answer == "" {
		t.Error("the hidden prompt had no answer")
	}
	if n := len(f.called(acp.MethodSessionResume)); n != 1 {
		t.Errorf("resumed %d times, want 1", n)
	}
}

// TestStopAfterSleepingIsOff: stopping an asleep chat (the agent stopped, the
// chat cleared) leaves it off, not ready.
func TestStopAfterSleepingIsOff(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, clock := idleManager(t, f)
	idleTurn(t, m, clock, testLead, "hi", 1)
	clock.advance(time.Second)
	clock.advance(15 * time.Minute)
	m.Stop(testLead.Ref(), "stopped")
	if m.Asleep(testLead.Ref()) || m.State(testLead.Ref()) != api.ChatOff {
		t.Errorf("a stopped chat is %q, asleep %v", m.State(testLead.Ref()), m.Asleep(testLead.Ref()))
	}
}

// TestAnAsleepLeadMovedToAnotherAccountSaysSo: the account switch tells an
// asleep chat what it tells an idle one, and the turn that wakes it starts
// on the new account, resuming the session.
func TestAnAsleepLeadMovedToAnotherAccountSaysSo(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, clock := idleManager(t, f)
	lead := testLead
	lead.ClaudeAccount = "old"
	idleTurn(t, m, clock, lead, "hi", 1)
	clock.advance(time.Second)
	clock.advance(15 * time.Minute)
	if !m.Asleep(lead.Ref()) {
		t.Fatal("the chat didn't sleep")
	}
	if note := m.AccountChanged(lead); note != "" {
		t.Errorf("the same account said %q", note)
	}
	lead.ClaudeAccount = "new"
	if note := m.AccountChanged(lead); !strings.Contains(note, "restarts on it and resumes this conversation") {
		t.Errorf("AccountChanged said %q", note)
	}
	if note := m.AccountChanged(lead); note != "" {
		t.Errorf("telling it again said %q", note)
	}
	if _, err := m.Send(lead, "still there?"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, lead, "the second turn", turnsEnded(2))
	if got := f.launchedOn(); len(got) != 2 || got[1] != "new" {
		t.Errorf("launched on %q, want the second on new", got)
	}
	if n := len(f.called(acp.MethodSessionResume)); n != 1 {
		t.Errorf("resumed %d times, want 1", n)
	}
}

// TestAShorterWaitReachesARunningChat: the setting is read again every few
// minutes, so shortening it doesn't wait out the longer wait already running.
func TestAShorterWaitReachesARunningChat(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, clock := idleManager(t, f)
	var mu sync.Mutex
	after := time.Hour
	m.IdleStop = func(state.Agent) (time.Duration, bool) { mu.Lock(); defer mu.Unlock(); return after, true }
	idleTurn(t, m, clock, testLead, "hi", 1)
	clock.advance(time.Second)
	mu.Lock()
	after = 5 * time.Minute
	mu.Unlock()
	clock.advance(5 * time.Minute)
	clock.advance(time.Second)
	if !m.Asleep(testLead.Ref()) {
		t.Fatal("the chat waited out the hour it started with")
	}
}
