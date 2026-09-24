package api

import "testing"

// The project chat and an agent's chat are the same routes, told apart only by
// the ref, so the app and the CLI drive both through one client.
func TestChatPathSplitsProjectsFromAgents(t *testing.T) {
	for ref, want := range map[string]string{
		"pawly":          "/v1/projects/pawly/chat",
		"pawly/agent-01": "/v1/agents/pawly/agent-01/chat",
	} {
		got, err := chatPath(ref)
		if err != nil || got != want {
			t.Errorf("chatPath(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	if _, err := chatPath(""); err == nil {
		t.Error("chatPath(\"\") succeeded")
	}
	if !IsProjectChat("pawly") || IsProjectChat("pawly/agent-01") {
		t.Error("IsProjectChat doesn't tell a project from an agent")
	}
}
