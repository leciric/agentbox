package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// end says, as the in-agent API's DELETE /v1/self/heavy/{key}, that key
// ended, with body ("" for none, as an agentbox before timing sends).
func (p *pressureTest) end(t *testing.T, key, body string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodDelete, "/v1/self/heavy/"+key, strings.NewReader(body))
	r.SetPathValue("key", key)
	w := httptest.NewRecorder()
	if err := p.srv.endHeavy(p.instance)(w, r); err != nil {
		t.Fatal(err)
	}
}

// A heavy command is timed as it ends: as it says, or, from an agentbox
// that says nothing, from when it started, its exit status unknown. One the
// daemon never heard start, and that says nothing, isn't.
func TestAHeavyCommandIsTimedAsItEnds(t *testing.T) {
	t.Parallel()
	p := newPressureTest(t)
	ctx := context.Background()
	p.start(t, "k1")
	p.end(t, "k1", `{"command":"GOFLAGS=-p=2 go test ./internal/...","jobs":4,"exitCode":2,"durationMS":1500}`)
	p.start(t, "k2")
	time.Sleep(20 * time.Millisecond)
	p.end(t, "k2", "")
	p.end(t, "k3", "")

	r, err := p.client.Timing(ctx, api.TimingQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Commands != 2 || r.FailedCommands != 1 || len(r.SlowCommands) != 2 || len(r.Agents) != 1 || r.Agents[0].Ref != p.ref {
		t.Fatalf("report = %+v", r)
	}
	said, old := r.SlowCommands[0], r.SlowCommands[1]
	if said.Command != "GOFLAGS=-p=2 go test ./internal/..." || said.Jobs != 4 || said.ExitCode != 2 || said.MS != 1500 || said.Agent != p.ref {
		t.Errorf("the command that said how it ended = %+v", said)
	}
	if old.Command != "go test ./k2" || old.ExitCode != -1 || old.MS < 20 || old.Jobs != 0 {
		t.Errorf("the command that said nothing = %+v", old)
	}
	rows, err := p.srv.store.SlowestTimings(ctx, state.TimingCommand, state.TimingFilter{}, 10)
	if err != nil || len(rows) != 2 || rows[0].Name != "go" || rows[1].Name != "go" {
		t.Errorf("rows = %+v, %v; want both named go", rows, err)
	}
}

// The report adds turns up in total and by agent, and is narrowed by
// project, agent and since.
func TestTimingReport(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	now := time.Now()
	sec := func(n int) time.Duration { return time.Duration(n) * time.Second }
	d.srv.store.RecordTiming(
		state.TimingRow{Kind: state.TimingTurn, Project: "acme", Agent: "agent-1", At: now.Add(-time.Minute), Wall: sec(60), Model: sec(30), ModelCalls: 3, FirstToken: sec(2), TTFT: sec(4), Tools: sec(20), ToolCalls: 5},
		state.TimingRow{Kind: state.TimingTurn, Project: "acme", Agent: "agent-2", At: now.Add(-2 * time.Minute), Wall: sec(30), Model: sec(10), ModelCalls: 1, FirstToken: sec(1), TTFT: sec(1)},
		state.TimingRow{Kind: state.TimingTurn, Project: "acme", Agent: "agent-2", At: now.Add(-3 * time.Hour), Wall: sec(300)},
		state.TimingRow{Kind: state.TimingTool, Project: "acme", Agent: "agent-1", At: now.Add(-time.Minute), Wall: sec(15), Name: "Bash", Detail: "go test ./...", Status: "completed"},
		state.TimingRow{Kind: state.TimingTool, Project: "acme", Agent: "agent-1", At: now.Add(-time.Minute), Wall: sec(5), Name: "Read", Status: "completed"},
	)
	r, err := d.client.Timing(ctx, api.TimingQuery{Project: "acme", Since: now.Add(-time.Hour), Top: 1})
	if err != nil {
		t.Fatal(err)
	}
	if r.Turns != 2 || r.TurnMS != 90_000 || r.ModelMS != 40_000 || r.ModelCalls != 4 || r.FirstTokenMS != 3_000 || r.ToolMS != 20_000 || r.ToolCalls != 5 || r.Answered != 2 {
		t.Errorf("totals = %+v", r.TimingTotals)
	}
	if len(r.Agents) != 2 || r.Agents[0].Ref != "acme/agent-1" || r.Since == nil {
		t.Errorf("agents = %+v, since %v", r.Agents, r.Since)
	}
	if len(r.Tools) != 1 || r.Tools[0].Name != "Bash" || len(r.SlowCalls) != 1 || r.SlowCalls[0].Title != "go test ./..." || r.SlowCalls[0].MS != 15_000 {
		t.Errorf("top 1: tools %+v, calls %+v", r.Tools, r.SlowCalls)
	}
	r, err = d.client.Timing(ctx, api.TimingQuery{Agent: "agent-2"})
	if err != nil || r.Turns != 2 || len(r.Agents) != 1 || r.Agents[0].Ref != "acme/agent-2" || len(r.SlowCommands) != 0 {
		t.Errorf("agent-2 alone, since the oldest: %+v, %v", r, err)
	}
	if _, err := d.client.Timing(ctx, api.TimingQuery{Top: -1}); err != nil {
		t.Errorf("a negative top is the default: %v", err)
	}
}

func TestCommandName(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"go test ./...":                     "go",
		"GOFLAGS=-p=2 CGO_ENABLED=0 go vet": "go",
		"./gradlew assembleDebug":           "gradlew",
		"/usr/bin/make -j4":                 "make",
		"'npm' --prefix desktop test":       "npm",
		"--flag=x run":                      "--flag=x",
		"":                                  "",
	} {
		if got := commandName(in); got != want {
			t.Errorf("commandName(%q) = %q, want %q", in, got, want)
		}
	}
}
