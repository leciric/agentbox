package chat

import (
	"slices"
	"strings"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// limitErr is Claude Code's usage limit, as the adapter fails a turn with it.
var limitErr = &acp.Error{Code: acp.CodeInternalError, Message: "Internal error: You've hit your session limit · resets 3pm"}

func (f *fakeTool) setTurnErr(err *acp.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turnErr = err
}

// checkMoved checks that the adapter was launched on the old account and then
// on the new one, and that the second resumed the first's session.
func checkMoved(t *testing.T, f *fakeTool, th api.ChatThread) {
	t.Helper()
	if got := f.launchedOn(); !slices.Equal(got, []string{"old", "new"}) {
		t.Errorf("launched on %q, want old then new", got)
	}
	resumed := f.called(acp.MethodSessionResume)
	if len(resumed) != 1 || !strings.Contains(string(resumed[0]), `"sessionId":"session-1"`) || len(f.called(acp.MethodSessionNew)) != 1 {
		t.Errorf("resumed %s, started %d new sessions", resumed, len(f.called(acp.MethodSessionNew)))
	}
	if !slices.ContainsFunc(th.Items, func(it api.ChatItem) bool {
		return it.Kind == "notice" && strings.Contains(it.Text, `account "new"`)
	}) {
		t.Errorf("no notice names the new account: %v", kinds(th))
	}
}

// TestAnIdleChatRestartsOnItsNewAccount: the adapter reads its token only as
// it starts, so a chat moved to another account restarts on it at once,
// resuming the same session, rather than waiting for something else to.
func TestAnIdleChatRestartsOnItsNewAccount(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, _ := newManager(t, openStore(t), f)
	a := testAgent
	a.ClaudeAccount = "old"
	if _, err := m.Send(a, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the first turn", turnsEnded(1))
	a.ClaudeAccount = "new"
	m.AccountChanged(a)
	th := waitThread(t, m, a, "the restarted session", sessionReady)
	checkMoved(t, f, th)
	// Telling it again changes nothing: it is on that account already.
	m.AccountChanged(a)
	if _, err := m.Send(a, "and now?"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the second turn", turnsEnded(2))
	if n := len(f.launchedOn()); n != 2 {
		t.Errorf("the adapter started %d times, want 2", n)
	}
}

// TestABusyChatRestartsOnItsNewAccountBeforeItsNextTurn: a restart would end
// the running turn, so the move waits for the next one.
func TestABusyChatRestartsOnItsNewAccountBeforeItsNextTurn(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		if text == "hi" {
			close(entered)
			<-release
		}
		return answerHello(f, s, text)
	})
	f.resume = true
	m, _ := newManager(t, openStore(t), f)
	a := testAgent
	a.ClaudeAccount = "old"
	if _, err := m.Send(a, "hi"); err != nil {
		t.Fatal(err)
	}
	<-entered
	a.ClaudeAccount = "new"
	m.AccountChanged(a)
	close(release)
	waitThread(t, m, a, "the turn to end", turnsEnded(1))
	if n := len(f.launchedOn()); n != 1 {
		t.Fatalf("the adapter started %d times while its turn ran, want 1", n)
	}
	if _, err := m.Send(a, "again"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, a, "the second turn", turnsEnded(2))
	checkMoved(t, f, th)
	if last := th.Items[len(th.Items)-1]; last.Kind != "assistant" || last.Text != "Hello" {
		t.Errorf("the last item is %+v", last)
	}
}

// TestALimitedChatCarriesOnAtOnceOnItsNewAccount: a turn waiting out the old
// account's usage limit has nothing to wait for on the new one.
func TestALimitedChatCarriesOnAtOnceOnItsNewAccount(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	f.setPromptErr(limitErr)
	m, clock := limitManager(t, f)
	a := testAgent
	a.ClaudeAccount = "old"
	if _, err := m.Send(a, "go"); err != nil {
		t.Fatal(err)
	}
	if th := waitThread(t, m, a, "the turn to fail", turnsEnded(1)); !th.Session.Limited || th.Session.ResumeAt == nil {
		t.Fatalf("the chat isn't waiting out the limit: %+v", th.Session)
	}
	f.setPromptErr(nil)
	a.ClaudeAccount = "new"
	m.AccountChanged(a)
	th := waitThread(t, m, a, "the resumed turn", turnsEnded(2))
	checkMoved(t, f, th)
	if prompts := f.called(acp.MethodSessionPrompt); len(prompts) != 2 || !strings.Contains(string(prompts[1]), resumeNudge) {
		t.Errorf("prompts %s, want the original and the nudge", prompts)
	}
	if th.Session.Limited || th.Session.ResumeAt != nil {
		t.Errorf("the session still looks limited: %+v", th.Session)
	}
	if _, pending := clock.waiting(); pending {
		t.Error("the old account's wait is still armed")
	}
}

// TestATurnThatHitsTheOldAccountsLimitCarriesOnAtOnce: moved mid-turn, and the
// turn then fails on the old account's limit, the chat doesn't wait for it to
// reset: the resume restarts on the new account straight away.
func TestATurnThatHitsTheOldAccountsLimitCarriesOnAtOnce(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		if text == "go" {
			close(entered)
			<-release
		}
		return answerHello(f, s, text)
	})
	f.resume = true
	f.setTurnErr(limitErr)
	m, clock := limitManager(t, f)
	a := testAgent
	a.ClaudeAccount = "old"
	if _, err := m.Send(a, "go"); err != nil {
		t.Fatal(err)
	}
	<-entered
	a.ClaudeAccount = "new"
	m.AccountChanged(a)
	f.setTurnErr(nil) // read as each prompt arrives: the running one still fails
	close(release)
	th := waitThread(t, m, a, "the turn to fail", func(th api.ChatThread) bool { return th.Session.ResumeAt != nil })
	if !th.Session.ResumeAt.Equal(noon) {
		t.Errorf("it carries on at %v, want now (%v)", th.Session.ResumeAt, noon)
	}
	clock.advance(0)
	th = waitThread(t, m, a, "the resumed turn", turnsEnded(2))
	checkMoved(t, f, th)
}

// TestAChatWithNoAdapterJustTakesTheAccount: nothing runs to restart, and the
// next adapter starts on the new account.
func TestAChatWithNoAdapterJustTakesTheAccount(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, _ := newManager(t, openStore(t), f)
	a := testAgent
	a.ClaudeAccount = "old"
	m.AccountChanged(a) // a chat never opened: nothing held
	if _, err := m.Thread(a); err != nil {
		t.Fatal(err)
	}
	a.ClaudeAccount = "new"
	m.AccountChanged(a)
	th, _ := m.Thread(a)
	if th.Session.State != api.ChatOff || len(th.Items) != 0 {
		t.Errorf("state %s, items %v", th.Session.State, kinds(th))
	}
	if _, err := m.Send(a, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the turn", turnsEnded(1))
	if got := f.launchedOn(); !slices.Equal(got, []string{"new"}) {
		t.Errorf("launched on %q, want new", got)
	}
}
