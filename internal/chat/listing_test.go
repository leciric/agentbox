package chat

import (
	"testing"
	"time"

	"agentbox/internal/acp"
)

// A conversation whose lock is held — storing a turn's items while the
// database is busy — must not hold an agent listing with it: State,
// Background and Progress answer from what the last flush kept.
func TestListingDoesNotWaitForABusyConversation(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"go test ./...","kind":"execute","status":"in_progress"}`)
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, openStore(t), f)
	defer close(release)
	if _, err := m.Send(testAgent, "run the tests"); err != nil {
		t.Fatal(err)
	}
	waitProgress(t, m, "the tool call", func(p Progress, ok bool) bool { return ok && p.ToolRunning })
	want := m.State(testAgent.Ref())

	c := m.existing(testAgent.Ref())
	c.mu.Lock()
	done := make(chan struct{})
	var state string
	var p Progress
	var ok bool
	go func() {
		defer close(done)
		state = m.State(testAgent.Ref())
		_ = m.Background(testAgent.Ref())
		p, ok = m.Progress(testAgent.Ref())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		c.mu.Unlock()
		t.Fatal("the listing waited for the conversation's lock")
	}
	c.mu.Unlock()
	if state != want || !ok || !p.ToolRunning || p.RunningTool != "go test ./..." {
		t.Errorf("read while locked: state %q (want %q), progress %+v %t", state, want, p, ok)
	}
}
