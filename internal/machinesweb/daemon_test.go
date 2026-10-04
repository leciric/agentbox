package machinesweb

import (
	"testing"
	"time"

	"agentbox/internal/api"
)

func TestFromAPI(t *testing.T) {
	p := api.Project{Name: "pawly", DisplayName: "Pawly"}
	agents := map[string]api.Agent{"agent-01": {Name: "agent-01", Branch: "agentbox/feat-x", Worktree: "/w/agent-01"}}
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	it := fromAPI(p, agents, api.MediaItem{
		ID: "abc", Agent: "pawly/agent-01", AgentTitle: "Login page", Kind: "recording", Name: "checkout",
		Text: "the happy path", Path: "/m/abc.mp4", Mime: "video/mp4", Size: 42,
		Meta: api.MediaMeta{Width: 1280, Height: 720, Duration: 2.5}, CreatedAt: at,
	})
	want := Item{
		ID: "a-abc", Source: SourceAgentBox, Kind: "recording", Created: at, Repo: "Pawly",
		Branch: "agentbox/feat-x", Worktree: "/w/agent-01", Session: "agent-01", SessionLabel: "agent-01 · Login page",
		Tool: "agentbox", Caption: "checkout — the happy path", Width: 1280, Height: 720, DurationMs: 2500,
		Bytes: 42, Mime: "video/mp4", Path: "/m/abc.mp4",
	}
	if it != want {
		t.Fatalf("got  %+v\nwant %+v", it, want)
	}
	gone := fromAPI(api.Project{Name: "pawly"}, nil, api.MediaItem{ID: "d", AgentName: "agent-09", AgentGone: true, Kind: "screenshot"})
	if gone.Repo != "pawly" || gone.Branch != "(destroyed agents)" || gone.Session != "agent-09" {
		t.Fatalf("a destroyed agent's: %+v", gone)
	}
}
