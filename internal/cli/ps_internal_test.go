package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

func TestRenderProcesses(t *testing.T) {
	now := time.Now()
	agents := []api.AgentProcesses{
		{Ref: "pawly/agent-01", Title: "Fix login", Running: true, Memory: 3 << 30, Killable: 2 << 30, Processes: 5, Trees: []api.ProcessTree{
			{PID: 412, Label: "nest start --watch", Memory: 2 << 30, CPU: 0.4, Processes: 3, Started: now.Add(-134 * time.Minute), IdleSeconds: 7800, Heavy: true, Idle: true,
				Members: []api.ProcessMember{{PID: 412, Command: "node nest start --watch", Memory: 1 << 30}, {PID: 413, Command: "node dist/main", Memory: 1 << 30}}},
			{PID: 1840, Label: "go test ./...", Memory: 1 << 30, Processes: 3, Started: now.Add(-time.Minute),
				Workers: &api.HeavyWorkers{Workers: 4, CPU: 6, Cores: 12, Running: 2, Memory: true, Limit: "memory", Free: 2254857830}},
			{PID: 1910, Label: "npm test", Memory: 1 << 29, Processes: 2, Started: now.Add(-time.Minute),
				Workers: &api.HeavyWorkers{Workers: 6, CPU: 6, Cores: 12, Running: 2, Limit: "cpu"}},
			{PID: 214, Label: "Claude Code", Kind: api.ProcessTreeTool, Protected: true, Memory: 600 << 20, CPU: 3, Processes: 2, Started: now.Add(-3 * time.Hour), IdleSeconds: 9000},
		}},
		{Ref: "pawly/agent-02", Error: "permission denied"},
	}
	var buf bytes.Buffer
	renderProcesses(&buf, agents, true, now)
	got := buf.String()
	for _, want := range []string{
		`pawly/agent-01  "Fix login"  3.0 GiB in 5 processes, 2.0 GiB of it can be killed`,
		"412", "2.0 GiB", "2h14m", "2h10m", "nest start --watch  (heavy, idle)",
		"Claude Code  (protected)",
		"go test ./...  (4 workers: 2.1 GiB free)",
		"npm test  (6 workers: 12 cores among 2)",
		"413 node dist/main",
		"pawly/agent-02  couldn't read its processes: permission denied",
		"agentbox kill <project/agent> <pid>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output doesn't have %q:\n%s", want, got)
		}
	}
	// A protected tree's idle time isn't news.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "Claude Code") && strings.Contains(line, "2h30m") {
			t.Errorf("the tool's tree shows its idle time: %q", line)
		}
	}
	buf.Reset()
	renderProcesses(&buf, nil, false, now)
	if !strings.Contains(buf.String(), "No agent has a running machine") {
		t.Errorf("empty: %q", buf.String())
	}
}
