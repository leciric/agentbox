package chat

import (
	"strings"
	"testing"

	"agentbox/internal/acp"
)

// TestAToolUpdateRestartsTheChatBetweenTurns: a newer Claude Code mid-turn
// leaves that turn alone, and the next one starts on it, resuming the session.
func TestAToolUpdateRestartsTheChatBetweenTurns(t *testing.T) {
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
	if _, err := m.Send(a, "hi"); err != nil {
		t.Fatal(err)
	}
	<-entered
	m.ToolUpdated("codex") // another tool's update is nothing to this chat
	m.ToolUpdated(a.AI)
	close(release)
	waitThread(t, m, a, "the turn to end", turnsEnded(1))
	if n := len(f.launchedOn()); n != 1 {
		t.Fatalf("the adapter started %d times while its turn ran, want 1", n)
	}
	if _, err := m.Send(a, "again"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the second turn", turnsEnded(2))
	if n := len(f.launchedOn()); n != 2 {
		t.Fatalf("the adapter started %d times, want 2: once more for the new version", n)
	}
	resumed := f.called(acp.MethodSessionResume)
	if len(resumed) != 1 || !strings.Contains(string(resumed[0]), `"sessionId":"session-1"`) {
		t.Errorf("resumed %s, want session-1", resumed)
	}
	// Once restarted, a third turn runs on the same adapter.
	if _, err := m.Send(a, "and again"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the third turn", turnsEnded(3))
	if n := len(f.launchedOn()); n != 2 {
		t.Errorf("the adapter started %d times, want 2", n)
	}
}
