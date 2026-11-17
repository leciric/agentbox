package chat

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/state"
)

// An adapter running where the agent's paths don't reach — the Home chat's on
// the user's computer — starts its session in its own folder with the MCP
// servers it was given, and the chat says where it runs.
func TestAProcessElsewhereStartsItsSessionThere(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, _ := newManager(t, openStore(t), f)
	m.Launch = func(ctx context.Context, a state.Agent, status func(string)) (*Process, error) {
		p, err := f.launch(ctx, a, status)
		if err != nil {
			return nil, err
		}
		p.Cwd = "/Users/dev/.local/share/agentbox/home-chat"
		p.MCPServers = []acp.McpServer{{Name: "agentbox", Command: "/Applications/AgentBox.app/agentbox", Args: []string{"home-host", "mcp"}, Env: []acp.EnvVariable{}}}
		p.Notice = "The Home chat runs on your computer."
		return p, nil
	}
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn", turnsEnded(1))
	started := f.called(acp.MethodSessionNew)
	if len(started) != 1 || !strings.Contains(string(started[0]), `"cwd":"/Users/dev/.local/share/agentbox/home-chat"`) ||
		!strings.Contains(string(started[0]), `"command":"/Applications/AgentBox.app/agentbox"`) {
		t.Fatalf("session/new = %s", started)
	}
	var said []string
	for _, it := range th.Items {
		if it.Kind == "notice" {
			said = append(said, it.Text)
		}
	}
	if len(said) != 1 || said[0] != "The Home chat runs on your computer." {
		t.Errorf("notices = %q", said)
	}
}

// An adapter on another machine than the one before can't resume its session:
// it isn't asked to, and the new session is told the conversation.
func TestAMovedProcessIsToldTheConversation(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	f.resume = true
	m, _ := newManager(t, openStore(t), f)
	moved := false
	m.Launch = func(ctx context.Context, a state.Agent, status func(string)) (*Process, error) {
		p, err := f.launch(ctx, a, status)
		if err != nil {
			return nil, err
		}
		p.Moved = moved
		return p, nil
	}
	if _, err := m.Send(testAgent, "remember the word pelican"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn", turnsEnded(1))
	moved = true
	m.Relaunch(testAgent.Ref())
	if _, err := m.Send(testAgent, "which word?"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the second turn", turnsEnded(2))
	if n := len(f.called(acp.MethodSessionResume)); n != 0 {
		t.Errorf("tried to resume %d times", n)
	}
	var notice string
	for _, it := range th.Items {
		if it.Kind == "notice" {
			notice = it.Text
		}
	}
	if !strings.Contains(notice, "it runs on another machine now") || !strings.Contains(notice, "told the conversation above") {
		t.Errorf("notice = %q", notice)
	}
	prompts := f.called(acp.MethodSessionPrompt)
	if len(prompts) != 2 || !strings.Contains(string(prompts[1]), "pelican") {
		t.Errorf("the second prompt wasn't told the conversation: %s", prompts)
	}
}
