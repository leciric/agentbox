package chat

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/state"
)

const pidRace = "Error: Failed to retrieve PID of executing child process"

// exitedAtOnce is an adapter whose incus exec ended before it answered
// anything, having written stderr: what Incus' exec race looks like from here.
func exitedAtOnce(stderr string) *Process {
	r, w := io.Pipe()
	_ = w.Close()
	return &Process{
		Stdin: nopWriteCloser{}, Stdout: r, Stop: func() {},
		Wait:   func() error { return errors.New("exit status 1") },
		Stderr: func() string { return stderr + "\n" },
	}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// failingFirst launches broken, the given number of times, then the fake tool.
func failingFirst(f *fakeTool, times int, broken func() (*Process, error)) (Launcher, func() int) {
	var mu sync.Mutex
	launched := 0
	launch := func(ctx context.Context, a state.Agent, status func(string)) (*Process, error) {
		mu.Lock()
		launched++
		n := launched
		mu.Unlock()
		if n <= times {
			return broken()
		}
		return f.launch(ctx, a, status)
	}
	return launch, func() int { mu.Lock(); defer mu.Unlock(); return launched }
}

// Incus' "Failed to retrieve PID" goes away on the next try, so a start that
// hits it launches the adapter again instead of failing the turn.
func TestAStartThatHitsIncusPIDRaceIsRetried(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, _ := newManager(t, openStore(t), f)
	launch, launches := failingFirst(f, 1, func() (*Process, error) { return exitedAtOnce(pidRace), nil })
	m.Launch = launch
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if r := find(th, "user", 0).Result; r == nil || r.State != "completed" {
		t.Fatalf("the turn ended %+v, want completed: %s", r, summary(th))
	}
	if got := launches(); got != 2 {
		t.Errorf("the adapter was launched %d times, want 2", got)
	}
}

// Retried only so many times: a race that keeps happening is reported.
func TestARepeatedIncusPIDRaceFailsTheStart(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, _ := newManager(t, openStore(t), f)
	launch, launches := failingFirst(f, 100, func() (*Process, error) { return exitedAtOnce(pidRace), nil })
	m.Launch = launch
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if e := find(th, "error", 0); !strings.Contains(e.Text, "didn't start") || !strings.Contains(e.Text, "Failed to retrieve PID") {
		t.Errorf("the chat says %q", e.Text)
	}
	if got := launches(); got != 3 {
		t.Errorf("the adapter was launched %d times, want 3", got)
	}
}

// Any other failure to start is not Incus' race, and isn't retried.
func TestOtherStartFailuresAreNotRetried(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, _ := newManager(t, openStore(t), f)
	launch, launches := failingFirst(f, 100, func() (*Process, error) { return exitedAtOnce("mise ERROR no such tool"), nil })
	m.Launch = launch
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if got := launches(); got != 1 {
		t.Errorf("the adapter was launched %d times, want 1", got)
	}
}

// An agent's task is its first turn. When its tool didn't start, the next
// message ("continue") must still bring the task: the session that answers
// it was never told.
func TestAMessageWhoseStartFailedGoesWithTheNextOne(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var prompts []string
	f := newFakeTool(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		mu.Lock()
		prompts = append(prompts, text)
		mu.Unlock()
		return answerHello(f, sessionID, text)
	})
	store := openStore(t)
	m, _ := newManager(t, store, f)
	launch, _ := failingFirst(f, 1, func() (*Process, error) { return nil, errors.New("the machine is gone") })
	m.Launch = launch
	if _, err := m.Send(testAgent, "Fix the login page"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn to fail", turnsEnded(1))
	// Kept where a restart of AgentBox finds it too.
	stored, err := store.Chat(context.Background(), testAgent.Project, testAgent.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.Handoff, "Fix the login page") {
		t.Errorf("saved handoff = %q, want the undelivered task in it", stored.Handoff)
	}

	if _, err := m.Send(testAgent, "continue"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the second turn to end", turnsEnded(2))
	mu.Lock()
	got := slices.Clone(prompts)
	mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("the tool got %d prompts, want 1: %s", len(got), summary(th))
	}
	task, next := strings.Index(got[0], "Fix the login page"), strings.Index(got[0], "continue")
	if task < 0 || next < task {
		t.Errorf("the tool was sent %q, want the task ahead of the new message", got[0])
	}

	// Once delivered, it isn't sent again.
	if _, err := m.Send(testAgent, "and then"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the third turn to end", turnsEnded(3))
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 || strings.Contains(prompts[1], "Fix the login page") {
		t.Errorf("prompts = %q, want the task only in the first", prompts)
	}
}
