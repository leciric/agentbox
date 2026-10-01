package chat

import (
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// waitProgress waits for the agent's running turn to satisfy cond.
func waitProgress(t *testing.T, m *Manager, what string, cond func(Progress, bool) bool) Progress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, ok := m.Progress(testAgent.Ref())
		if cond(p, ok) {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %+v, %t", what, p, ok)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A running turn's clock moves with whatever its adapter sends, a tool call
// still running is said, and a stall marked on it comes off the moment the
// adapter sends anything again, and with the turn.
func TestProgressFollowsTheAdapter(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	clock := time.Date(2026, 9, 29, 17, 58, 0, 0, time.UTC)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	set := func(t time.Time) { mu.Lock(); clock = t; mu.Unlock() }

	release, update := make(chan struct{}), make(chan string)
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"go test ./...","kind":"execute","status":"in_progress"}`)
		for {
			select {
			case u := <-update:
				f.update(s, u)
			case <-release:
				return acp.PromptResponse{StopReason: "end_turn"}
			}
		}
	})
	m, _ := newManager(t, openStore(t), f)
	m.Now = now
	if _, ok := m.Progress(testAgent.Ref()); ok {
		t.Fatal("progress with no conversation at all")
	}
	if _, err := m.Send(testAgent, "run the tests"); err != nil {
		t.Fatal(err)
	}
	p := waitProgress(t, m, "the tool call", func(p Progress, ok bool) bool { return ok && p.ToolRunning })
	if !p.LastProgress.Equal(clock) || p.Turn == "" || p.Waiting || p.Stalled != nil {
		t.Fatalf("progress = %+v", p)
	}

	// Silence moves nothing; the daemon marks it stalled, once.
	set(clock.Add(20 * time.Minute))
	if again, _ := m.Progress(testAgent.Ref()); !again.LastProgress.Equal(p.LastProgress) {
		t.Errorf("the clock moved on its own: %v", again.LastProgress)
	}
	from := p.LastProgress
	if m.SetStalled(testAgent, "another turn", &from) {
		t.Error("marked a turn that isn't running")
	}
	if !m.SetStalled(testAgent, p.Turn, &from) {
		t.Fatal("didn't mark the running turn")
	}
	if m.SetStalled(testAgent, p.Turn, &from) {
		t.Error("marked it twice")
	}
	if got, _ := m.Progress(testAgent.Ref()); got.Stalled == nil || !got.Stalled.Equal(from) {
		t.Errorf("stalled = %v", got.Stalled)
	}
	th := waitThread(t, m, testAgent, "the mark", func(th api.ChatThread) bool { return th.Session.StalledSince != nil })
	if th.Session.State != api.ChatRunning {
		t.Errorf("a stalled turn's chat is %q, want still running", th.Session.State)
	}

	// The tool says something: progress, and the mark comes off by itself.
	update <- `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed"}`
	p = waitProgress(t, m, "the update", func(p Progress, _ bool) bool { return p.Stalled == nil })
	if !p.LastProgress.Equal(clock) || p.ToolRunning {
		t.Errorf("after the update, progress = %+v", p)
	}
	waitThread(t, m, testAgent, "the mark to come off", func(th api.ChatThread) bool { return th.Session.StalledSince == nil })

	// A mark doesn't outlive its turn.
	if !m.SetStalled(testAgent, p.Turn, &from) {
		t.Fatal("didn't mark it again")
	}
	close(release)
	waitProgress(t, m, "the turn to end", func(_ Progress, ok bool) bool { return !ok })
	th = waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if th.Session.StalledSince != nil {
		t.Errorf("the ended turn is still marked stalled since %v", th.Session.StalledSince)
	}
	if m.SetStalled(testAgent, p.Turn, nil) {
		t.Error("cleared a turn that has ended")
	}
}

// Waiting on a permission request is said, and the answer restarts the clock:
// however long the question waited, it wasn't the turn's to spend.
func TestProgressWaitsOnAPerson(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	clock := time.Date(2026, 9, 29, 17, 58, 0, 0, time.UTC)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	release := make(chan struct{})
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.ask(s, `{"toolCallId":"t1","title":"rm -rf build","kind":"execute","status":"pending"}`)
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, openStore(t), f)
	m.Now = now
	if _, err := m.Send(testAgent, "clean up"); err != nil {
		t.Fatal(err)
	}
	defer close(release)
	waitProgress(t, m, "the question", func(p Progress, ok bool) bool { return ok && p.Waiting })
	th := waitThread(t, m, testAgent, "the question", func(th api.ChatThread) bool { return th.Session.State == api.ChatWaiting })

	mu.Lock()
	clock = clock.Add(3 * time.Hour)
	mu.Unlock()
	if _, err := m.Answer(testAgent, find(th, "permission", 0).ID, "allow-once"); err != nil {
		t.Fatal(err)
	}
	p := waitProgress(t, m, "the answer", func(p Progress, _ bool) bool { return !p.Waiting })
	if !p.LastProgress.Equal(now()) {
		t.Errorf("after the answer, the clock is at %v, want %v", p.LastProgress, now())
	}
}

// An AI tool that exits in the middle of a turn is reported, with what the
// chat says about it; one stopped on purpose, or exiting between turns, isn't.
func TestLostIsCalledWhenTheToolExitsMidTurn(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var lost []string
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Working on it"}}`)
		f.crash()
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, openStore(t), f)
	m.Lost = func(a state.Agent, why string) {
		mu.Lock()
		defer mu.Unlock()
		lost = append(lost, a.Ref()+": "+why)
	}
	got := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lost...)
	}
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to fail", func(th api.ChatThread) bool {
		return turnsEnded(1)(th) && th.Session.State == api.ChatError
	})
	deadline := time.Now().Add(5 * time.Second)
	for len(got()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if l := got(); len(l) != 1 || !strings.HasPrefix(l[0], testAgent.Ref()+": Claude Code exited") || !strings.Contains(l[0], "fake tool: something broke") {
		t.Fatalf("lost = %q", l)
	}

	// A turn that ends, then the tool exiting with nothing running: not lost.
	f.setTurn(answerHello)
	if _, err := m.Send(testAgent, "again"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the second turn", turnsEnded(2))
	f.crash()
	waitThread(t, m, testAgent, "the exit", func(th api.ChatThread) bool { return th.Session.State == api.ChatError })

	// Stopped on purpose mid-turn: not lost either.
	release := make(chan struct{})
	f.setTurn(func(*fakeTool, string, string) acp.PromptResponse {
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	defer close(release)
	if _, err := m.Send(testAgent, "and again"); err != nil {
		t.Fatal(err)
	}
	waitProgress(t, m, "the third turn", func(_ Progress, ok bool) bool { return ok })
	waitThread(t, m, testAgent, "the third turn to run", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	m.Stop(testAgent.Ref(), "the agent was stopped")
	waitThread(t, m, testAgent, "the third turn", turnsEnded(3))
	m.Wait() // for any report still on its way
	if l := got(); len(l) != 1 {
		t.Errorf("lost = %q, want only the first", l)
	}
}
