package cli

import (
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

// TestRenderLimits: each account on a line, its windows as a percentage and
// when they reset, and a window that reset since the reading said to have
// rather than shown with a number that no longer describes it.
func TestRenderLimits(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local)
	var b strings.Builder
	renderLimits(&b, []api.ClaudeLimit{{
		Account: "default", Default: true, At: now.Add(-10 * time.Minute), Status: "allowed_warning",
		Windows: []api.ClaudeLimitWindow{
			{Name: "five_hour", Label: "5-hour", Utilization: 0.91, ResetsAt: now.Add(2 * time.Hour)},
			{Name: "seven_day", Label: "Weekly", Utilization: 0.42, ResetsAt: now.Add(72 * time.Hour)},
		},
	}, {
		Account: "work", At: now.Add(-6 * time.Hour),
		Windows: []api.ClaudeLimitWindow{{Name: "five_hour", Label: "5-hour", Utilization: 0.5, ResetsAt: now.Add(-time.Hour)}},
	}}, now)
	out := b.String()
	for _, want := range []string{"default: 5-hour 91% (resets Sep 22 14:00), Weekly 42%", "allowed warning", "work: 5-hour reset at Sep 22 11:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderLimits is missing %q:\n%s", want, out)
		}
	}
	b.Reset()
	renderLimits(&b, nil, now)
	if b.Len() != 0 {
		t.Errorf("no readings still printed %q", b.String())
	}
}
