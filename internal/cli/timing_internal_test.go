package cli

import (
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

// TestRenderTiming: the turns' time split into model, tools, starting and
// the rest, each agent, and the slowest tools, calls and commands, with an
// exit status nobody said as "?".
func TestRenderTiming(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local)
	at := since.Add(time.Hour)
	totals := api.TimingTotals{Turns: 4, Answered: 4, TurnMS: 600_000, StartMS: 6_000, ModelMS: 300_000, ModelCalls: 20,
		FirstTokenMS: 8_000, TTFTMS: 30_000, ToolMS: 240_000, ToolCalls: 40, Commands: 2, CommandMS: 200_000, FailedCommands: 1}
	r := api.TimingReport{Since: &since, TimingTotals: totals,
		Agents:    []api.AgentTiming{{Ref: "acme/agent-1", TimingTotals: totals, LastAt: at}, {Ref: "acme/agent-2", LastAt: at}},
		Tools:     []api.ToolTiming{{Name: "Bash", Calls: 30, TotalMS: 200_000, MaxMS: 150_000, Failed: 2}, {Name: "Grep", Subagent: "Explore", Calls: 10, TotalMS: 4_000, MaxMS: 900}},
		SlowCalls: []api.TimedCall{{Agent: "acme/agent-1", Name: "Bash", Title: "go test\n./...", Status: "failed", At: at, MS: 150_000}},
		SlowCommands: []api.TimedCommand{
			{Agent: "acme/agent-1", Command: "go test ./...", Jobs: 4, ExitCode: 1, At: at, MS: 150_000},
			{Agent: "acme/agent-1", Command: "npm test", ExitCode: -1, At: at, MS: 50_000},
		},
	}
	var b strings.Builder
	renderTiming(&b, r)
	out := strings.Join(strings.Fields(strings.ReplaceAll(b.String(), "\n", " ¶ ")), " ")
	for _, want := range []string{
		"Since Nov 20 09:00: 4 chat turns over 10m00s, 2 heavy commands over 3m20s.",
		"¶ Model 5m00s 50% 20 requests; first token in 2.0s a turn, 1.5s a request ¶",
		"¶ Tools 4m00s 40% 40 calls ¶",
		"¶ Starting 6.0s 1% ",
		"¶ Other 54s 9% ",
		"¶ Heavy commands 3m20s 2 runs, 1 failed;",
		"¶ acme/agent-1 4 10m00s 5m00s 4m00s 3m20s (2) Nov 20 10:00 ¶",
		"¶ Bash 30 3m20s 6.7s 2m30s 2 ¶",
		"¶ Explore: Grep 10 4.0s 400ms 900ms 0 ¶",
		"¶ 2m30s Bash acme/agent-1 Nov 20 10:00 go test ./... (failed) ¶",
		"¶ 2m30s 4 1 acme/agent-1 Nov 20 10:00 go test ./... ¶",
		"¶ 50s - ? acme/agent-1 Nov 20 10:00 npm test",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	b.Reset()
	renderTiming(&b, api.TimingReport{})
	if got := b.String(); got != "Nothing timed since the oldest kept.\n" {
		t.Errorf("an empty report = %q", got)
	}
}

func TestHumanMS(t *testing.T) {
	t.Parallel()
	for ms, want := range map[int64]string{0: "0ms", 350: "350ms", 8_200: "8.2s", 42_000: "42s", 372_000: "6m12s", 3_720_000: "1h02m"} {
		if got := humanMS(ms); got != want {
			t.Errorf("humanMS(%d) = %q, want %q", ms, got, want)
		}
	}
}
