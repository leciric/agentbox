package chat

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// fakeClock is the chat's clock inside a test: time only moves when the test
// moves it, so waiting out a five-hour limit takes none.
type fakeClock struct {
	mu     sync.Mutex
	at     time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock *fakeClock
	due   time.Time
	f     func()
	done  bool
}

func newClock(at time.Time) *fakeClock { return &fakeClock{at: at} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) After(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, due: c.at.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.done {
		return false
	}
	t.done = true
	return true
}

// advance moves the clock on and runs whatever fell due, off the clock's own
// lock: what runs takes the conversation's, and may schedule the next wait.
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.done && !t.due.After(c.at) {
			t.done = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

// waiting is how long the pending wake-up still has to run, and whether there
// is one at all.
func (c *fakeClock) waiting() (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.timers {
		if !t.done {
			return t.due.Sub(c.at), true
		}
	}
	return 0, false
}

func (f *fakeTool) sessionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions
}

func (f *fakeTool) setPromptErr(err *acp.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promptErr = err
}

// noon is the fixed "now" the tests below reason from: a limit that resets at
// 3pm is three hours off, whatever time of day the suite really runs at.
var noon = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// The samples are what a usage limit really looks like by the time it reaches
// AgentBox: the adapter rejects the prompt with "Internal error: " and Claude
// Code's own sentence, and forwards that same sentence as an ordinary
// assistant message (it only keeps it back from clients that take its typed
// session failures, which AgentBox doesn't).
func TestUsageLimitIsRecognisedAndItsResetRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		failure string
		said    string
		until   time.Time
	}{{
		name:    "the session limit, resetting within the day",
		failure: "Internal error: You've hit your session limit · resets 3pm",
		until:   time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC),
	}, {
		name:    "the sentence arrives as the assistant message instead",
		failure: "Internal error: error_during_execution",
		said:    "You've hit your usage limit · resets at 10:30pm (UTC)",
		until:   time.Date(2026, 9, 20, 22, 30, 0, 0, time.UTC),
	}, {
		name:    "a weekly limit, days off",
		failure: "Internal error: You've hit your weekly limit · resets Sep 23, 9am",
		until:   time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC),
	}, {
		name:    "the epoch form",
		failure: "Internal error: Claude AI usage limit reached|1758380400",
		until:   time.Unix(1758380400, 0).UTC(),
	}, {
		name:    "a 429 from the API, with its own kind on the error",
		failure: `Internal error: API Error: 429 {"type":"error","error":{"type":"rate_limit_error","message":"Number of requests has exceeded your rate limit. Please try again in 60 seconds."}}: {"errorKind":"rate_limit"}`,
		until:   noon.Add(60 * time.Second),
	}, {
		name:    "no reset time anywhere",
		failure: "Internal error: You've reached your usage limit for this account",
	}, {
		name:    "a limit that needs a person, not a wait",
		failure: "Internal error: You're out of usage credits",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			until, limited := UsageLimit(tc.failure, tc.said, noon)
			if !limited {
				t.Fatalf("a usage limit went unnoticed: %q / %q", tc.failure, tc.said)
			}
			if !until.Equal(tc.until) {
				t.Errorf("resets at %v, want %v", until, tc.until)
			}
		})
	}
}

// Every other way a turn fails is one that waiting doesn't fix, and resuming
// on its own would have AgentBox carrying on with work the tool never refused.
func TestOtherFailuresAreNotUsageLimits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ failure, said string }{
		{failure: `Internal error: API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired."}}`},
		{failure: `Internal error: API Error: 529 {"type":"overloaded_error"}`},
		{failure: "Internal error: Your credit balance is too low to access the Anthropic API"},
		{failure: "Claude Code didn't start: exec: \"claude-agent-acp\": executable file not found in $PATH"},
		{failure: "the chat was stopped"},
		{failure: "Internal error: error_during_execution", said: "I've added a rate limit to the API: you've hit your quota if you send more than 60 requests a minute."},
	} {
		if until, limited := UsageLimit(tc.failure, tc.said, noon); limited {
			t.Errorf("read as a usage limit (resets %v): %q / %q", until, tc.failure, tc.said)
		}
	}
}

// A reset time the message names in the past — the limit ran out while the
// turn was still failing — means wait a moment, not until tomorrow.
func TestAResetTimeJustGoneMeansNow(t *testing.T) {
	t.Parallel()
	until, limited := UsageLimit("Internal error: You've hit your session limit · resets 11:45am", "", noon)
	if !limited || !until.Equal(noon) {
		t.Errorf("resets at %v (limited %v), want %v", until, limited, noon)
	}
}

func TestResumeDelay(t *testing.T) {
	t.Parallel()
	reset := noon.Add(2 * time.Hour)
	for _, tc := range []struct {
		name  string
		until time.Time
		try   int
		want  time.Duration
	}{
		{name: "the reset time it named", until: reset, try: 1, want: 2*time.Hour + resumeSlack},
		{name: "no reset time: the fallback wait", try: 1, want: resumeWait},
		{name: "no reset time, again: twice as long", try: 2, want: 2 * resumeWait},
		{name: "and again", try: 3, want: 4 * resumeWait},
		{name: "capped", try: 6, want: resumeMaxWait},
		{name: "a reset time already behind us", until: noon.Add(-time.Minute), try: 1, want: resumeWait},
		{name: "the same reset time, after it was wrong", until: noon.Add(time.Second), try: 2, want: 2 * resumeWait},
	} {
		if got := resumeDelay(tc.until, noon, tc.try); got != tc.want {
			t.Errorf("%s: waits %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The whole of it: a turn fails on the limit, the chat says when it resets and
// when it will carry on, and at that moment it nudges the same session — not a
// new one — back into the work it was already doing.
func TestATurnCutOffByAUsageLimitCarriesOnWhenTheLimitResets(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.setPromptErr(&acp.Error{Code: acp.CodeInternalError, Message: "Internal error: You've hit your session limit · resets 3pm"})
	m, clock := limitManager(t, f)

	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to fail", turnsEnded(1))
	if !th.Session.Limited {
		t.Fatalf("the session doesn't say it is limited: %+v", th.Session)
	}
	if want := noon.Add(3 * time.Hour); th.Session.LimitedUntil == nil || !th.Session.LimitedUntil.Equal(want) {
		t.Errorf("the limit resets at %v, want %v", th.Session.LimitedUntil, want)
	}
	if want := noon.Add(3*time.Hour + resumeSlack); th.Session.ResumeAt == nil || !th.Session.ResumeAt.Equal(want) {
		t.Errorf("it carries on at %v, want %v", th.Session.ResumeAt, want)
	}

	// Nothing happens before the limit resets.
	clock.advance(2 * time.Hour)
	if n := len(f.called(acp.MethodSessionPrompt)); n != 1 {
		t.Fatalf("%d prompts while the limit was still on", n)
	}

	f.setPromptErr(nil)
	clock.advance(time.Hour + resumeSlack)
	th = waitThread(t, m, testAgent, "the resumed turn", turnsEnded(2))
	prompts := f.called(acp.MethodSessionPrompt)
	if len(prompts) != 2 {
		t.Fatalf("%d prompts, want the original and the nudge", len(prompts))
	}
	if !strings.Contains(string(prompts[1]), resumeNudge) {
		t.Errorf("the resumed turn said %s, want the nudge", prompts[1])
	}
	// The same session, resumed where it was, rather than a new one.
	if n := f.sessionCount(); n != 1 {
		t.Errorf("%d sessions were started: the turn should carry on in the one it was cut off in", n)
	}
	if th.Session.Limited || th.Session.ResumeAt != nil {
		t.Errorf("the session still looks limited after it carried on: %+v", th.Session)
	}
	if _, pending := clock.waiting(); pending {
		t.Error("a wake-up is still armed after the turn carried on")
	}
}

// Without a reset time there is nothing to wait for but the fallback, and a
// limit still refusing when that runs out is waited out for longer.
func TestALimitWithNoResetTimeIsRetriedWithBackoff(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.setPromptErr(&acp.Error{Code: acp.CodeInternalError, Message: "Internal error: You've reached your usage limit for this account"})
	m, clock := limitManager(t, f)

	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to fail", turnsEnded(1))
	if wait, ok := clock.waiting(); !ok || wait != resumeWait {
		t.Fatalf("waits %v (armed %v), want the %v fallback", wait, ok, resumeWait)
	}

	clock.advance(resumeWait)
	waitThread(t, m, testAgent, "the second failure", turnsEnded(2))
	if wait, ok := clock.waiting(); !ok || wait != 2*resumeWait {
		t.Fatalf("waits %v (armed %v) after failing again, want %v", wait, ok, 2*resumeWait)
	}
}

// A limit that keeps refusing isn't the rolling window everybody hits, and a
// chat that carried itself on for ever would be a loop nobody asked for. This
// is also what a resumed turn resetting its own count of tries would break.
func TestAChatStopsWaitingAfterEnoughTries(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.setPromptErr(&acp.Error{Code: acp.CodeInternalError, Message: "Internal error: You've reached your usage limit for this account"})
	m, clock := limitManager(t, f)

	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to fail", turnsEnded(1))
	for try := 1; try <= resumeAttempts; try++ {
		clock.advance(resumeMaxWait)
		waitThread(t, m, testAgent, "another try", turnsEnded(try+1))
	}

	if _, pending := clock.waiting(); pending {
		t.Error("it is still waiting after every try was used up")
	}
	if n := len(f.called(acp.MethodSessionPrompt)); n != resumeAttempts+1 {
		t.Errorf("%d prompts, want the original and %d tries", n, resumeAttempts)
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(th.Items, func(it api.ChatItem) bool {
		return it.Kind == "notice" && strings.Contains(it.Text, "stopped waiting")
	}) {
		t.Error("the chat never says it has stopped waiting")
	}
	if th.Session.ResumeAt != nil {
		t.Errorf("it still carries on at %v", th.Session.ResumeAt)
	}
	if !th.Session.Limited {
		t.Error("the session no longer says why it stopped")
	}
}

// Stopping the chat is how you say you don't want it carried on, and a chat
// waiting out a limit has no running turn to cancel: cancelling has to reach
// the pending wake-up itself.
func TestCancellingAChatCallsOffThePendingResume(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.setPromptErr(&acp.Error{Code: acp.CodeInternalError, Message: "Internal error: You've hit your session limit · resets 3pm"})
	m, clock := limitManager(t, f)

	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to fail", turnsEnded(1))
	s, err := m.Cancel(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if s.ResumeAt != nil {
		t.Errorf("the session still carries on at %v after it was cancelled", s.ResumeAt)
	}
	if _, pending := clock.waiting(); pending {
		t.Error("the wake-up is still armed after the chat was cancelled")
	}
	clock.advance(4 * time.Hour)
	if n := len(f.called(acp.MethodSessionPrompt)); n != 1 {
		t.Errorf("%d prompts: a cancelled chat carried itself on anyway", n)
	}
}

// Stopping the agent ends its session; nothing may wake it up hours later.
func TestStoppingTheAgentCallsOffThePendingResume(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.setPromptErr(&acp.Error{Code: acp.CodeInternalError, Message: "Internal error: You've hit your session limit · resets 3pm"})
	m, clock := limitManager(t, f)

	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to fail", turnsEnded(1))
	m.Stop(testAgent.Ref(), "the agent was stopped")
	if _, pending := clock.waiting(); pending {
		t.Error("the wake-up is still armed after the agent was stopped")
	}
	clock.advance(4 * time.Hour)
	if n := len(f.called(acp.MethodSessionPrompt)); n != 1 {
		t.Errorf("%d prompts: a stopped agent carried itself on anyway", n)
	}
}

// With the setting off the limit is still shown — it is why the turn failed —
// but nothing is scheduled, and the chat waits for you.
func TestTheResumeCanBeTurnedOff(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	if err := store.SetFlag(context.Background(), state.SettingResumeAfterLimit, false); err != nil {
		t.Fatal(err)
	}
	f := newFakeTool(answerHello)
	f.setPromptErr(&acp.Error{Code: acp.CodeInternalError, Message: "Internal error: You've hit your session limit · resets 3pm"})
	m, _ := newManager(t, store, f)
	clock := newClock(noon)
	m.Now, m.After = clock.Now, clock.After

	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to fail", turnsEnded(1))
	if !th.Session.Limited || th.Session.LimitedUntil == nil {
		t.Errorf("the limit isn't shown at all: %+v", th.Session)
	}
	if th.Session.ResumeAt != nil {
		t.Errorf("it carries on at %v with the setting off", th.Session.ResumeAt)
	}
	if _, pending := clock.waiting(); pending {
		t.Error("a wake-up was armed with the setting off")
	}
}

// Codex and OpenCode word their own refusals their own way, and none of this
// has been checked against them.
func TestOnlyClaudeCodeIsCarriedOn(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.setPromptErr(&acp.Error{Code: acp.CodeInternalError, Message: "Internal error: You've hit your session limit · resets 3pm"})
	m, clock := limitManager(t, f)
	a := testAgent
	a.AI = "codex"

	if _, err := m.Send(a, "go"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, a, "the turn to fail", turnsEnded(1))
	if th.Session.Limited || th.Session.ResumeAt != nil {
		t.Errorf("a Codex chat was treated as usage-limited: %+v", th.Session)
	}
	if _, pending := clock.waiting(); pending {
		t.Error("a wake-up was armed for a Codex chat")
	}
}

// A message of your own while the chat waits is the work moving again: the
// wake-up goes, and the nudge never lands on top of what you asked for.
func TestYourOwnMessageCallsOffThePendingResume(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.setPromptErr(&acp.Error{Code: acp.CodeInternalError, Message: "Internal error: You've hit your session limit · resets 3pm"})
	m, clock := limitManager(t, f)

	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to fail", turnsEnded(1))
	f.setPromptErr(nil)
	if _, err := m.Send(testAgent, "never mind, do this instead"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the second turn", turnsEnded(2))
	if _, pending := clock.waiting(); pending {
		t.Error("the wake-up is still armed after a message of your own")
	}
	clock.advance(4 * time.Hour)
	if n := len(f.called(acp.MethodSessionPrompt)); n != 2 {
		t.Errorf("%d prompts: the nudge landed on top of the message", n)
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(th.Items, func(it api.ChatItem) bool { return it.Text == resumeNudge }) {
		t.Error("the nudge was sent even though the chat had already moved on")
	}
}

// limitManager is a chat manager on a clock the test moves itself.
func limitManager(t *testing.T, f *fakeTool) (*Manager, *fakeClock) {
	t.Helper()
	m, _ := newManager(t, openStore(t), f)
	clock := newClock(noon)
	m.Now, m.After = clock.Now, clock.After
	return m, clock
}
