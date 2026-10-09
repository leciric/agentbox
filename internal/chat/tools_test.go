package chat

import (
	"strings"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

func sessionReady(th api.ChatThread) bool { return th.Session.State == api.ChatReady }

// TestReloadToolsResumesTheSession: "Reload tools" restarts the adapter, which
// reads its MCP servers only as it starts, and the new one resumes the same
// session, so the conversation carries on.
func TestReloadToolsResumesTheSession(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn", turnsEnded(1))
	m.ToolsChanged(testAgent.Project, "")
	if th, _ := m.Thread(testAgent); !th.Session.ToolsChanged || th.Session.NoResume {
		t.Errorf("toolsChanged %v, noResume %v", th.Session.ToolsChanged, th.Session.NoResume)
	}
	session, err := m.ReloadTools(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if session.ToolsChanged || session.State != api.ChatStarting {
		t.Errorf("after reloading: state %s, toolsChanged %v", session.State, session.ToolsChanged)
	}
	th := waitThread(t, m, testAgent, "the restarted session", sessionReady)
	if n := len(f.called(acp.MethodInitialize)); n != 2 {
		t.Errorf("the adapter started %d times, want 2", n)
	}
	resumed := f.called(acp.MethodSessionResume)
	if len(resumed) != 1 || !strings.Contains(string(resumed[0]), `"sessionId":"session-1"`) || len(f.called(acp.MethodSessionNew)) != 1 {
		t.Errorf("resumed %s, started %d new sessions", resumed, len(f.called(acp.MethodSessionNew)))
	}
	if notice := find(th, "notice", 0).Text; !strings.Contains(notice, "resumes this conversation") {
		t.Errorf("notice = %q", notice)
	}
	if find(th, "notice", 1).Kind != "" {
		t.Errorf("a resumed session left a second notice: %v", kinds(th))
	}
	if _, err := m.Send(testAgent, "and now?"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the second turn", turnsEnded(2))
	if n := len(f.called(acp.MethodInitialize)); n != 2 {
		t.Errorf("the adapter started %d times, want 2: the next turn restarted it again", n)
	}
}

// TestReloadToolsIsRefusedMidTurn: a restart would end the running turn.
func TestReloadToolsIsRefusedMidTurn(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		<-release
		return answerHello(f, s, text)
	})
	f.resume = true
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	if _, err := m.ReloadTools(testAgent); err == nil || !strings.Contains(err.Error(), "middle of a turn") {
		t.Errorf("reloading mid-turn: %v", err)
	}
	close(release)
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if n := len(f.called(acp.MethodInitialize)); n != 1 {
		t.Errorf("the adapter started %d times, want 1", n)
	}
}

// TestChangedToolsReloadBeforeTheNextTurn: a change to the project's
// connectors marks the chats it reaches, and the next message restarts the
// adapter first, resuming the session.
func TestChangedToolsReloadBeforeTheNextTurn(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn", turnsEnded(1))
	m.ToolsChanged(testAgent.Project, "agent-02")
	m.ToolsChanged("another", "")
	if th, _ := m.Thread(testAgent); th.Session.ToolsChanged {
		t.Error("another agent's or project's change marked this chat")
	}
	m.ToolsChanged(testAgent.Project, testAgent.Name)
	if _, err := m.Send(testAgent, "use the new tool"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the second turn", turnsEnded(2))
	if th.Session.ToolsChanged {
		t.Error("still marked after the restart")
	}
	if n := len(f.called(acp.MethodInitialize)); n != 2 {
		t.Errorf("the adapter started %d times, want 2", n)
	}
	if len(f.called(acp.MethodSessionResume)) != 1 || len(f.called(acp.MethodSessionNew)) != 1 {
		t.Errorf("%d resumed, %d new sessions", len(f.called(acp.MethodSessionResume)), len(f.called(acp.MethodSessionNew)))
	}
	if notice := find(th, "notice", 0).Text; !strings.Contains(notice, "The tools changed") {
		t.Errorf("notice = %q", notice)
	}
	if last := th.Items[len(th.Items)-1]; last.Kind != "assistant" || last.Text != "Hello" {
		t.Errorf("the last item is %+v", last)
	}
}

// TestToolsOfAnAdapterThatCantResume: nothing restarts by itself, since that
// would lose the session; a reload asked for starts a new one and says so.
func TestToolsOfAnAdapterThatCantResume(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the first turn", turnsEnded(1))
	if !th.Session.NoResume {
		t.Error("noResume isn't set")
	}
	m.ToolsChanged(testAgent.Project, "")
	if _, err := m.Send(testAgent, "again"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, testAgent, "the second turn", turnsEnded(2))
	if n := len(f.called(acp.MethodInitialize)); n != 1 || !th.Session.ToolsChanged {
		t.Errorf("the adapter started %d times, toolsChanged %v: want 1, still marked", n, th.Session.ToolsChanged)
	}
	if _, err := m.ReloadTools(testAgent); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, testAgent, "the new session", sessionReady)
	if n := len(f.called(acp.MethodSessionNew)); n != 2 {
		t.Errorf("%d new sessions, want 2", n)
	}
	if notice := find(th, "notice", 0).Text; !strings.Contains(notice, "in a new session") {
		t.Errorf("notice = %q", notice)
	}
}

// TestReloadToolsWithNoAdapter: nothing to restart, as the next one reads them.
func TestReloadToolsWithNoAdapter(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, _ := newManager(t, openStore(t), f)
	session, err := m.ReloadTools(testAgent)
	if err != nil || session.State != api.ChatOff {
		t.Errorf("state %s, %v", session.State, err)
	}
	if n := len(f.called(acp.MethodInitialize)); n != 0 {
		t.Errorf("the adapter started %d times", n)
	}
}
