package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// fakeMachine is a machine's processes as a test sets them, by instance.
type fakeMachine struct {
	trees  map[string][]agent.ProcessTree
	killed []string
}

func (f *fakeMachine) read(instance string) ([]agent.ProcessTree, error) {
	trees, ok := f.trees[instance]
	if !ok {
		return nil, agent.ErrMachineNotRunning
	}
	return trees, nil
}

func (f *fakeMachine) kill(_ context.Context, instance string, pid int, _ time.Duration) (agent.Killed, error) {
	for _, t := range f.trees[instance] {
		if t.Root().MachinePID != pid {
			continue
		}
		if t.Protected() {
			return agent.Killed{}, fmt.Errorf("pid %d (%s) is the AI tool's own: %w", pid, t.Root().Name, agent.ErrProtectedProcess)
		}
		f.killed = append(f.killed, fmt.Sprintf("%s/%d", instance, pid))
		return agent.Killed{Tree: t, Forced: 1}, nil
	}
	return agent.Killed{}, fmt.Errorf("pid %d: %w", pid, agent.ErrNoSuchProcess)
}

func proc(pid int, name, cmd string, mb int64, ticks uint64, started time.Time) agent.MachineProcess {
	return agent.MachineProcess{PID: pid + 1000, MachinePID: pid, Name: name, Command: cmd, RSS: mb << 20, CPUTicks: ticks, StartTicks: uint64(pid), Started: started}
}

func TestProcessesAPIListsAgentsTreesAndRefusesToKillTheTools(t *testing.T) {
	t.Parallel()
	start := time.Now().Add(-3 * time.Hour)
	f := &fakeMachine{trees: map[string][]agent.ProcessTree{
		"ab-hello-stack-agent-01": {
			{Kind: agent.TreeAgent, Label: "npx nest start --watch", Processes: []agent.MachineProcess{
				proc(777, "bash", "bash -c eval 'npx nest start --watch'", 4, 1, start),
				proc(778, "node", "node nest start --watch", 900, 2000, start),
				proc(779, "node", "node dist/main", 1400, 400, start),
			}},
			{Kind: agent.TreeTool, Label: "Claude Code", Processes: []agent.MachineProcess{proc(214, "claude", "claude", 600, 90_000, start)}},
			{Kind: agent.TreeSystem, Label: "System", Processes: []agent.MachineProcess{proc(1, "systemd", "/sbin/init", 12, 10, start)}},
		},
	}}
	heard := make(chan string, 1)
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus, testConfig{setup: func(s *Server) {
		s.processes.read, s.processes.kill, s.processes.wait, s.processes.pss = f.read, f.kill, 0, nil
		s.processTell = func(_ context.Context, a state.Agent, text string) (told, error) {
			heard <- a.Ref() + ": " + text
			return told{}, nil
		}
	}})
	a := addTestAgent(t, d)
	ctx := context.Background()

	list, err := d.client.Processes(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Agents) != 1 {
		t.Fatalf("listed %d agents, want the one with a running machine", len(list.Agents))
	}
	ap := list.Agents[0]
	if ap.Ref != a.Ref() || !ap.Running || len(ap.Trees) != 3 || ap.Processes != 5 {
		t.Fatalf("listed %+v", ap)
	}
	nest := ap.Trees[0]
	if nest.PID != 777 || nest.Kind != api.ProcessTreeAgent || nest.Protected || nest.Memory != 2304<<20 || !nest.Heavy || nest.Processes != 3 {
		t.Errorf("the dev server's tree is %+v", nest)
	}
	// It used next to no CPU in its three hours: idle all that time.
	if !nest.Idle || nest.IdleSeconds < int64(2*time.Hour/time.Second) {
		t.Errorf("the dev server is idle %ds (%v), want since it started", nest.IdleSeconds, nest.Idle)
	}
	if len(nest.Members) != 3 || nest.Members[0].PID != 777 {
		t.Errorf("members %+v, want its processes by their numbers in the machine, its root first", nest.Members)
	}
	tool := ap.Trees[1]
	if !tool.Protected || tool.Kind != api.ProcessTreeTool || tool.Heavy || tool.Idle {
		t.Errorf("the tool's tree is %+v: protected, and never flagged", tool)
	}
	if ap.Killable != nest.Memory || ap.Memory != (2304+600+12)<<20 {
		t.Errorf("killable %d of %d, want only the dev server's", ap.Killable, ap.Memory)
	}
	if other, err := d.client.Processes(ctx, "nobody"); err == nil && len(other.Agents) != 0 {
		t.Errorf("another project lists %+v", other.Agents)
	}
	one, err := d.client.AgentProcesses(ctx, a.Ref())
	if err != nil || one.Ref != a.Ref() || len(one.Trees) != 3 {
		t.Errorf("the agent's own list: %+v, %v", one, err)
	}

	// The tool's: refused, with the code that says why.
	_, err = d.client.KillProcess(ctx, a.Ref(), 214)
	var se *api.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusForbidden || se.Reason != api.ErrorProtectedProcess {
		t.Fatalf("killing the tool: %v, want 403 %s", err, api.ErrorProtectedProcess)
	}
	if _, err := d.client.KillProcess(ctx, a.Ref(), 4242); !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Errorf("killing a pid that isn't there: %v, want 404", err)
	}
	if len(f.killed) != 0 {
		t.Fatalf("killed %v", f.killed)
	}

	res, err := d.client.KillProcess(ctx, a.Ref(), 777)
	if err != nil {
		t.Fatal(err)
	}
	if res.Label != "npx nest start --watch" || res.Processes != 3 || res.Memory != 2304<<20 || res.Forced != 1 {
		t.Errorf("killed %+v", res)
	}
	select {
	case text := <-heard:
		if !strings.HasPrefix(text, a.Ref()+": The user killed `npx nest start --watch`") {
			t.Errorf("told %q", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the agent wasn't told")
	}
}

func TestProcessSamplerMeasuresCPUAndIdleBetweenReadings(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	started := now.Add(-time.Hour)
	ticks := uint64(0)
	trees := func() []agent.ProcessTree {
		return []agent.ProcessTree{{Kind: agent.TreeAgent, Label: "vite", Processes: []agent.MachineProcess{
			// A busy hour so far: a fifth of a core on average.
			{PID: 10, MachinePID: 10, StartTicks: 5, Started: started, CPUTicks: 72_000 + ticks, RSS: 1 << 20},
		}}}
	}
	ps := newProcessSampler()
	ps.now = func() time.Time { return now }
	ps.wait = 0
	ps.read = func(string) ([]agent.ProcessTree, error) { return trees(), nil }
	ps.pss = nil

	got, _ := ps.sample([]string{"m"})
	if tr := got["m"][0]; tr.CPU != 0 || tr.IdleSeconds != 0 || tr.Idle {
		t.Errorf("first reading %+v: no CPU measured yet, and busy now, as it has used plenty", tr)
	}
	// Ten seconds later, half a core.
	now, ticks = now.Add(10*time.Second), 500
	got, _ = ps.sample([]string{"m"})
	if tr := got["m"][0]; tr.CPU != 50 || tr.IdleSeconds != 0 {
		t.Errorf("second reading %+v, want 50%% and busy", tr)
	}
	// Then nothing for 40 minutes, read every minute.
	for range 40 {
		now = now.Add(time.Minute)
		got, _ = ps.sample([]string{"m"})
	}
	if tr := got["m"][0]; tr.CPU != 0 || tr.IdleSeconds != 40*60 || !tr.Idle {
		t.Errorf("after 40 quiet minutes %+v, want idle 2400s", tr)
	}

	// A machine nobody read for longer than processRecent is read twice,
	// and its CPU measured from the first of those.
	var reads []string
	ps.read = func(inst string) ([]agent.ProcessTree, error) {
		reads = append(reads, "read "+inst)
		return trees(), nil
	}
	ps.pss = func(machines ...[]agent.ProcessTree) { reads = append(reads, fmt.Sprintf("pss of %d", len(machines))) }
	ps.wait = time.Millisecond
	now = now.Add(processRecent + time.Second)
	ps.sample([]string{"m", "n"})
	if want := "read m,read n,read m,read n,pss of 2"; strings.Join(reads, ",") != want {
		t.Errorf("reads %v, want each machine read to measure from, then again, then PSS of both at once", reads)
	}

	// A second client a moment after the first gets the same figures,
	// not CPU measured over a few ticks.
	now, ticks = now.Add(time.Minute), ticks+600 // a minute at 10%
	got, _ = ps.sample([]string{"m"})
	first := got["m"][0].CPU
	now, ticks = now.Add(100*time.Millisecond), ticks+3
	got, _ = ps.sample([]string{"m"})
	if first != 10 || got["m"][0].CPU != first {
		t.Errorf("CPU %v then %v a moment later, want 10%% both times", first, got["m"][0].CPU)
	}
}

func TestKilledTextTellsWhatAndHowLong(t *testing.T) {
	t.Parallel()
	now := time.Now()
	k := agent.Killed{Tree: agent.ProcessTree{Label: "nest start --watch", Processes: []agent.MachineProcess{
		{MachinePID: 412, RSS: 2 << 30, Started: now.Add(-134 * time.Minute)},
		{MachinePID: 413, RSS: 300 << 20},
	}}}
	got := killedText(k, 412, now)
	for _, want := range []string{"The user killed `nest start --watch`", "pid 412 and the process it started", "holding 2.3 GiB", "running for 2h14m", "Don't start it again unless"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q doesn't say %q", got, want)
		}
	}
}
