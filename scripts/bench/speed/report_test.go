package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSummarize(t *testing.T) {
	s := summarize([]float64{5, 1, 3, 2, 4})
	if s.N != 5 || s.Median != 3 || s.Min != 1 || s.Max != 5 || s.P25 != 2 || s.P75 != 4 {
		t.Errorf("got %+v", s)
	}
	if s := summarize([]float64{1, 2}); s.Median != 1.5 {
		t.Errorf("median of 1, 2 = %v", s.Median)
	}
	if s := summarize(nil); s.N != 0 {
		t.Errorf("summary of nothing: %+v", s)
	}
}

func rec(mode string, load, run int, wall, model, build float64, ok bool) record {
	return record{
		Mode: mode, Task: "build", Load: load, Run: run, OK: ok, WallMS: wall, StartupMS: 1000,
		Breakdown: breakdown{ModelMS: model, ToolMS: build + 500, BuildToolMS: build, Turns: 4,
			Tokens: tokens{CacheRead: 40000}, Model: "claude-opus-5-5", Version: map[bool]string{true: "2.1.329", false: "2.1.322"}[mode == modeHost],
			Tools: map[string]toolStat{"Bash": {Calls: 2, MS: build}, "Edit": {Calls: 1, MS: 500}}},
		Env: &probeEnv{Nproc: map[bool]string{true: "16", false: "4"}[mode == modeHost], Env: map[string]string{"GOFLAGS": map[bool]string{true: "", false: "-p=4"}[mode == modeHost]}},
	}
}

func TestSummarizeAllAndReport(t *testing.T) {
	cfg := config{Runs: 3, Loads: []int{0}, LoadKind: "busy", Model: "claude-opus-5-5", Effort: "high"}
	var records []record
	for i := range 3 {
		f := float64(i)
		records = append(records,
			rec(modeHost, 0, i+1, 40000+1000*f, 25000, 12000, true),
			rec(modeExec, 0, i+1, 60000+1000*f, 26000, 30000, true),
			rec(modeAgent, 0, i+1, 90000+1000*f, 35000, 31000, i > 0),
		)
	}
	failed := rec(modeAgent, 0, 4, 1, 1, 1, false)
	failed.Error = "create: no image"
	records = append(records, failed)
	probes := []probeRun{
		{Kind: "bare-host", Run: 1, WallMS: 11000, Check: &checkRun{VetMS: 3000, BuildMS: 500, TestMS: 7500}, Env: &probeEnv{Nproc: "16"}},
		{Kind: "bare-machine", Run: 1, WallMS: 29000, Check: &checkRun{VetMS: 8000, BuildMS: 1000, TestMS: 20000}, Env: &probeEnv{Nproc: "4", Env: map[string]string{"GOFLAGS": "-p=4"}}},
		{Kind: "exec-rtt", Run: 1, WallMS: 180},
		{Kind: "exec-rtt", Run: 2, WallMS: 220},
	}
	s := summarizeAll(cfg, fixtureCommit, records, probes)
	if len(s.Cells) != 3 {
		t.Fatalf("%d cells, want 3", len(s.Cells))
	}
	host, exec, agent := s.Cells[0], s.Cells[1], s.Cells[2]
	if host.Mode != modeHost || exec.Mode != modeExec || agent.Mode != modeAgent {
		t.Fatalf("cells out of order: %s %s %s", host.Mode, exec.Mode, agent.Mode)
	}
	if host.Wall.Median != 41000 || exec.Slowdown < 1.48 || exec.Slowdown > 1.5 || host.Slowdown != 1 {
		t.Errorf("host wall %v, exec slowdown %v, host's own %v", host.Wall.Median, exec.Slowdown, host.Slowdown)
	}
	if agent.Runs != 4 || agent.OK != 2 || agent.Fails != 1 || agent.Wall.N != 3 {
		t.Errorf("agent: %d runs, %d ok, %d failed, %d timed", agent.Runs, agent.OK, agent.Fails, agent.Wall.N)
	}
	if agent.Prompt.Median != 10000 || agent.Tools["Bash"].Calls != 2 {
		t.Errorf("prompt per call %v, Bash calls per run %v", agent.Prompt.Median, agent.Tools["Bash"].Calls)
	}
	if len(exec.CPUs) != 1 || exec.CPUs[0] != "4" || exec.GOFLAGS[0] != "-p=4" {
		t.Errorf("exec's CPUs %v, GOFLAGS %v", exec.CPUs, exec.GOFLAGS)
	}
	if len(s.Probes) != 3 || s.Probes[2].Kind != "exec-rtt" || s.Probes[2].Wall.Median != 200 {
		t.Errorf("probes = %+v", s.Probes)
	}

	var out bytes.Buffer
	writeReport(&out, s)
	report := out.String()
	for _, want := range []string{
		"| build | 0 | host | 3/3 | 41.0s (40.0s–42.0s) | 1.00× |",
		"| build | 0 | exec | 3/3 | 61.0s (60.0s–62.0s) | 1.49× |",
		"| build | 0 | agent | 2/4 |",
		"| build | 0 | exec | +20.0s |",
		"| host | 0 | 1 | 11.0s | 3.0s | 0.5s | 7.5s | 16 · – |",
		"| 0 | 2 | 200 ms (180–220) |",
		"Bash 2× 12.0s",
		"Claude Code's version differs between modes (host 2.1.329, exec 2.1.322, agent 2.1.322)",
		"run 4, failed and is left out: create: no image",
		"the task was done in 2 of 4 runs",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
}

func TestRotate(t *testing.T) {
	m := []string{"a", "b", "c"}
	for i, want := range []string{"abc", "bca", "cab", "abc"} {
		if got := strings.Join(rotate(m, i), ""); got != want {
			t.Errorf("rotate %d = %s, want %s", i, got, want)
		}
	}
}
