package agent_test

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// TestAgentsDontCreditTheAIToolUnlessTurnedOn: a new Claude Code agent's
// settings.json hides Claude's attribution and its ~/.gitconfig points git at
// the hook that strips it. Turned on, its next chat leaves Claude Code's
// default, and its next start points git back at the repository's hooks.
func TestAgentsDontCreditTheAIToolUnlessTurnedOn(t *testing.T) {
	inc, files := recordingIncus(t, oneRunningAgent)
	f := setup(t, inc)
	ctx := context.Background()
	if err := f.m.Creds.SaveClaudeToken("personal", "sk-ant-oat01-personal"); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", ClaudeAccount: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	const settings, gitconfig = "/home/dev/.claude/settings.json", "/home/dev/.gitconfig"
	if got := inAgent(t, files, a.Instance, settings); !strings.Contains(got, `"attribution"`) || !strings.Contains(got, `"includeCoAuthoredBy": false`) {
		t.Errorf("a new agent's settings.json credits Claude:\n%s", got)
	}
	if got := inAgent(t, files, a.Instance, gitconfig); !strings.Contains(got, "hooksPath = \"/home/dev/.config/agentbox/git-hooks\"") {
		t.Errorf("a new agent's .gitconfig has no hooksPath:\n%s", got)
	}
	if got := inAgent(t, files, a.Instance, "/home/dev/.config/agentbox/git-hooks/commit-msg"); !strings.Contains(got, "co-authored-by") {
		t.Errorf("a new agent has no commit-msg hook:\n%s", got)
	}

	if err := f.st.SetFlag(ctx, state.SettingCreditAITool, true); err != nil {
		t.Fatal(err)
	}
	if err := f.m.PrepareChatModel(ctx, a, "", 0); err != nil {
		t.Fatal(err)
	}
	if got := inAgent(t, files, a.Instance, settings); strings.Contains(got, `"attribution"`) || strings.Contains(got, "includeCoAuthoredBy") {
		t.Errorf("turned on, the next chat's settings.json still hides Claude's attribution:\n%s", got)
	}
	if _, err := f.m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got := inAgent(t, files, a.Instance, gitconfig); strings.Contains(got, "hooksPath") {
		t.Errorf("turned on, the next start left hooksPath in .gitconfig:\n%s", got)
	}
}
