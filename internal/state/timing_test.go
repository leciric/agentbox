package state_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/state"
)

// Queued rows are written by themselves a moment later, as the store
// closes, or when asked; and what is older than KeepTiming goes.
func TestTimingIsQueuedAndKept(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	st := open(t, path)
	now := time.Now()
	turn := state.TimingRow{Kind: state.TimingTurn, Project: "acme", Agent: "agent-1", Turn: "t1", At: now.Add(-time.Minute), Wall: time.Minute,
		Model: 20 * time.Second, FirstToken: 2 * time.Second, TTFT: 5 * time.Second, ModelCalls: 3, Tools: 30 * time.Second, ToolCalls: 4, Status: "completed"}
	st.RecordTiming(turn)
	count := func(st *state.Store) int {
		var n int
		if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM timing`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(st); n != 0 {
		t.Fatalf("%d rows written as they were queued: it should wait", n)
	}
	// By itself, within a moment.
	deadline := time.Now().Add(5 * time.Second)
	for count(st) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the queued row was never written")
		}
		time.Sleep(20 * time.Millisecond)
	}
	rows, err := st.SlowestTimings(ctx, state.TimingTurn, state.TimingFilter{}, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	got := rows[0]
	if got.Wall != turn.Wall || got.Model != turn.Model || got.FirstToken != turn.FirstToken || got.TTFT != turn.TTFT ||
		got.ModelCalls != 3 || got.Tools != turn.Tools || got.ToolCalls != 4 || got.Status != "completed" || got.At.UnixMilli() != turn.At.UnixMilli() {
		t.Errorf("read back %+v, want %+v", got, turn)
	}

	// As the store closes.
	st.RecordTiming(state.TimingRow{Kind: state.TimingCommand, Project: "acme", Agent: "agent-1", Name: "go", At: now, Wall: time.Second, ExitCode: 1})
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = open(t, path)
	if n := count(st); n != 2 {
		t.Errorf("%d rows after a reopen, want the one queued as it closed too", n)
	}
	// A closed store takes no more: Close already wrote what it had.
	st.RecordTiming(state.TimingRow{Kind: state.TimingTool, Project: "acme", Agent: "agent-1", At: now})
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOldTimingGoes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	now := time.Now()
	st.RecordTiming(
		state.TimingRow{Kind: state.TimingTool, Project: "acme", Agent: "agent-1", Name: "Bash", At: now.Add(-state.KeepTiming - time.Hour)},
		state.TimingRow{Kind: state.TimingTool, Project: "acme", Agent: "agent-1", Name: "Read", At: now.Add(-time.Hour)},
	)
	if err := st.FlushTiming(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := st.SlowestTimings(ctx, state.TimingTool, state.TimingFilter{}, 10)
	if err != nil || len(rows) != 1 || rows[0].Name != "Read" {
		t.Errorf("rows = %+v, %v; want Read's alone", rows, err)
	}
}

// Sums add up by agent, busiest first; tools by name and by who ran them;
// and the filter narrows them all.
func TestTimingSums(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	now := time.Now()
	sec := func(n int) time.Duration { return time.Duration(n) * time.Second }
	at := func(minutes int) time.Time { return now.Add(-time.Duration(minutes) * time.Minute) }
	st.RecordTiming(
		state.TimingRow{Kind: state.TimingTurn, Project: "acme", Agent: "agent-1", At: at(50), Wall: sec(100), Start: sec(5), Model: sec(60), FirstToken: sec(3), TTFT: sec(9), ModelCalls: 4, Tools: sec(30), ToolCalls: 3},
		state.TimingRow{Kind: state.TimingTurn, Project: "acme", Agent: "agent-1", At: at(40), Wall: sec(10), Status: "failed"},
		state.TimingRow{Kind: state.TimingTurn, Project: "acme", Agent: "agent-2", At: at(30), Wall: sec(20), Model: sec(15), FirstToken: sec(1), TTFT: sec(1), ModelCalls: 1},
		state.TimingRow{Kind: state.TimingTurn, Project: "acme", Agent: "agent-2", At: at(300), Wall: sec(999)},
		state.TimingRow{Kind: state.TimingCommand, Project: "acme", Agent: "agent-2", At: at(20), Wall: sec(200), Name: "go", Detail: "go test ./...", Jobs: 4, ExitCode: 1},
		state.TimingRow{Kind: state.TimingCommand, Project: "acme", Agent: "agent-2", At: at(10), Wall: sec(50), Name: "npm", ExitCode: 0},
		state.TimingRow{Kind: state.TimingTool, Project: "acme", Agent: "agent-1", At: at(50), Wall: sec(20), Name: "Bash", Status: "completed"},
		state.TimingRow{Kind: state.TimingTool, Project: "acme", Agent: "agent-1", At: at(49), Wall: sec(5), Name: "Bash", Status: "failed"},
		state.TimingRow{Kind: state.TimingTool, Project: "acme", Agent: "agent-1", At: at(48), Wall: sec(8), Name: "Grep", Sub: "Explore", Status: "completed"},
		state.TimingRow{Kind: state.TimingTool, Project: "other", Agent: "agent-1", At: at(48), Wall: sec(90), Name: "Bash", Status: "completed"},
	)
	if err := st.FlushTiming(ctx); err != nil {
		t.Fatal(err)
	}
	f := state.TimingFilter{Project: "acme", Since: at(60)}
	sums, err := st.TimingSums(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 || sums[0].Agent != "agent-2" || sums[1].Agent != "agent-1" {
		t.Fatalf("sums = %+v, want agent-2 (its commands) then agent-1", sums)
	}
	one, two := sums[1], sums[0]
	if one.Turns != 2 || one.Answered != 1 || one.Wall != sec(110) || one.Start != sec(5) || one.Model != sec(60) || one.FirstToken != sec(3) ||
		one.TTFT != sec(9) || one.ModelCalls != 4 || one.Tools != sec(30) || one.ToolCalls != 3 || one.Commands != 0 {
		t.Errorf("agent-1 = %+v", one)
	}
	if two.Turns != 1 || two.Wall != sec(20) || two.Commands != 2 || two.FailedCommands != 1 || two.CommandTime != sec(250) {
		t.Errorf("agent-2 = %+v, want its turn since the hour began and both commands", two)
	}
	tools, err := st.ToolTimeSums(ctx, f, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "Bash" || tools[0].Calls != 2 || tools[0].Failed != 1 || tools[0].Total != sec(25) || tools[0].Max != sec(20) ||
		tools[1].Name != "Grep" || tools[1].Sub != "Explore" {
		t.Errorf("tools = %+v", tools)
	}
	commands, err := st.SlowestTimings(ctx, state.TimingCommand, f, 1)
	if err != nil || len(commands) != 1 || commands[0].Detail != "go test ./..." || commands[0].Jobs != 4 || commands[0].ExitCode != 1 {
		t.Errorf("slowest command = %+v, %v", commands, err)
	}
	// An agent name alone is that name in every project.
	calls, err := st.SlowestTimings(ctx, state.TimingTool, state.TimingFilter{Agent: "agent-1"}, 10)
	if err != nil || len(calls) != 4 || calls[0].Project != "other" {
		t.Errorf("agent-1's calls everywhere = %+v, %v", calls, err)
	}
}
