package cli

import (
	"testing"

	"agentbox/internal/api"
)

func TestMediaAgentLabelsAGoneAgent(t *testing.T) {
	for _, c := range []struct {
		name string
		item api.MediaItem
		want string
	}{
		{"name only", api.MediaItem{AgentName: "agent-01"}, "agent-01"},
		{"name and title", api.MediaItem{AgentName: "agent-01", AgentTitle: "Reminders"}, "agent-01 · Reminders"},
		{"gone, no title", api.MediaItem{AgentName: "agent-01", AgentGone: true}, "agent-01 (removed)"},
		{"gone, with title", api.MediaItem{AgentName: "agent-01", AgentTitle: "Reminders", AgentGone: true}, "agent-01 · Reminders (removed)"},
		{"falls back to Agent when AgentName is unset", api.MediaItem{Agent: "pawly/agent-01"}, "agent-01"},
	} {
		if got := mediaAgent(c.item); got != c.want {
			t.Errorf("%s: mediaAgent() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDescribeAllMediaNamesTheFilter(t *testing.T) {
	for _, c := range []struct {
		n           int
		kind, agent string
		want        string
	}{
		{42, "", "", "all 42 items"},
		{42, "screenshot", "", "all 42 screenshots"},
		{42, "screenshot", "agent-12", "all 42 screenshots of agent-12"},
		{1, "recording", "", "all 1 recording"},
		{1, "", "agent-12", "all 1 item of agent-12"},
	} {
		if got := describeAllMedia(c.n, c.kind, c.agent); got != c.want {
			t.Errorf("describeAllMedia(%d, %q, %q) = %q, want %q", c.n, c.kind, c.agent, got, c.want)
		}
	}
}
