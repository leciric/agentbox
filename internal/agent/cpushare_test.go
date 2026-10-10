package agent_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

func TestCPUShare(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ cores, running, want int }{
		{10, 0, 10},
		{10, 1, 10},
		{10, 2, 5},
		{10, 3, 4}, // rounded up: the shares overlap rather than leave a core to nobody
		{10, 4, 3},
		{10, 6, 2},
		{10, 20, 2}, // never below two
		{16, 5, 4},
		{1, 3, 1}, // nor above what there is
		{0, 1, 1},
	} {
		if got := agent.CPUShare(c.cores, c.running); got != c.want {
			t.Errorf("CPUShare(%d cores, %d running) = %d, want %d", c.cores, c.running, got, c.want)
		}
	}
}

// addAgents puts agents into the store, each with the instance Create gives it.
func addAgents(t *testing.T, st *state.Store, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := st.AddAgent(context.Background(), state.Agent{
			Project: "hello-stack", Name: name, Instance: "ab-hello-stack-" + name,
			Branch: "agentbox/" + name, Worktree: filepath.Join(t.TempDir(), name),
			Status: state.AgentReady, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// configCalls are the calls that changed an instance's configuration.
func configCalls(calls []string) []string {
	var out []string
	for _, call := range calls {
		if strings.HasPrefix(call, "config set ") || strings.HasPrefix(call, "config unset ") {
			out = append(out, strings.TrimSpace(call))
		}
	}
	return out
}

// TestBalanceCPU: with three agents running on ten cores, each running or
// paused machine gets four, one that already has it is left alone, and so are
// a stopped agent and a machine that isn't an agent's.
func TestBalanceCPU(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[
    {"name":"ab-hello-stack-agent-01","status":"Running","config":{}},
    {"name":"ab-hello-stack-agent-02","status":"Running","config":{"limits.cpu":"4"}},
    {"name":"ab-hello-stack-agent-03","status":"Running","config":{"limits.cpu":"12"}},
    {"name":"ab-hello-stack-agent-04","status":"Frozen","config":{"limits.cpu":"10"}},
    {"name":"ab-hello-stack-agent-05","status":"Stopped","config":{"limits.cpu":"1"}},
    {"name":"someone-elses","status":"Running","config":{}}]' ;;
esac
exit 0`)
	f := setup(t, inc)
	f.m.Cores = 10
	addAgents(t, f.st, "agent-01", "agent-02", "agent-03", "agent-04", "agent-05")

	changed, err := f.m.BalanceCPU(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"config set ab-hello-stack-agent-01 limits.cpu=4",
		"config set ab-hello-stack-agent-03 limits.cpu=4",
		"config set ab-hello-stack-agent-04 limits.cpu=4",
	}
	if got := configCalls(calls()); !slices.Equal(got, want) {
		t.Errorf("changed %q, want %q", got, want)
	}
	if changed != 3 {
		t.Errorf("changed = %d, want 3", changed)
	}
	noCall(t, calls(), "someone-elses")
}

// TestBalanceCPUAloneHasEveryCore: an agent left running alone has its share
// taken off rather than set to every core, so it runs as it always did.
func TestBalanceCPUAloneHasEveryCore(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[
    {"name":"ab-hello-stack-agent-01","status":"Running","config":{"limits.cpu":"5"}},
    {"name":"ab-hello-stack-agent-02","status":"Stopped","config":{"limits.cpu":"5"}}]' ;;
esac
exit 0`)
	f := setup(t, inc)
	f.m.Cores = 10
	addAgents(t, f.st, "agent-01", "agent-02")
	if _, err := f.m.BalanceCPU(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"config unset ab-hello-stack-agent-01 limits.cpu"}
	if got := configCalls(calls()); !slices.Equal(got, want) {
		t.Errorf("changed %q, want %q", got, want)
	}
}

// TestStartSetsTheShareBeforeBooting: a stopped agent starting beside two
// running ones boots with a third of the cores, so its first process sees
// them, and the share is set before the machine starts.
func TestStartSetsTheShareBeforeBooting(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	inc, calls := loggingIncus(t, fmt.Sprintf(`net='"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}'
case "$1" in
  start) touch %[1]q ;;
  list) s=Stopped; [ -f %[1]q ] && s=Running
    echo '[
    {"name":"ab-hello-stack-agent-01","status":"'$s'","config":{"limits.cpu":"2"},'"$net"'},
    {"name":"ab-hello-stack-agent-02","status":"Running","config":{},'"$net"'},
    {"name":"ab-hello-stack-agent-03","status":"Running","config":{},'"$net"'}]' ;;
  query) echo '{"config":{},"expanded_config":{},"devices":{}}' ;;
esac
exit 0`, started))
	f := setup(t, inc)
	f.m.Cores = 12
	addAgents(t, f.st, "agent-01", "agent-02", "agent-03")
	a, err := f.st.Agent(context.Background(), "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	all := calls()
	set := slices.Index(all, "config set ab-hello-stack-agent-01 limits.cpu=4")
	start := slices.Index(all, "start ab-hello-stack-agent-01")
	if set < 0 || start < 0 || set > start {
		t.Errorf("want limits.cpu=4 set before the start, got:\n  %s", strings.Join(all, "\n  "))
	}
}

// TestClaudeAgentsReadTheWorkerCounts: a Claude Code agent's shells set the
// worker counts from its share, its login shells through the env file and its
// Bash tool's through BASH_ENV, which also brings a heavy phase's lease; and
// an agent made before that gets both when its next chat starts.
func TestClaudeAgentsReadTheWorkerCounts(t *testing.T) {
	inc, files := recordingIncus(t, oneRunningAgent)
	f := setup(t, inc)
	ctx := context.Background()
	if err := f.m.Creds.SaveClaudeToken("personal", "sk-ant-oat01-personal"); err != nil {
		t.Fatal(err)
	}
	a, err := f.m.Create(ctx, "hello-stack", agent.CreateOptions{AI: "claude", ClaudeAccount: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	const bashEnv = "/home/dev/.config/agentbox/bash_env"
	check := func(when string) {
		t.Helper()
		if got := inAgent(t, files, a.Instance, "/home/dev/.claude/settings.json"); !strings.Contains(got, `"BASH_ENV": "`+bashEnv+`"`) {
			t.Errorf("%s: settings.json doesn't point BASH_ENV at bash_env:\n%s", when, got)
		}
		if got := inAgent(t, files, a.Instance, bashEnv); !strings.Contains(got, "GOMAXPROCS") || !strings.Contains(got, agent.HeavyEnvFile) {
			t.Errorf("%s: bash_env doesn't set the worker counts and read the lease:\n%s", when, got)
		}
	}
	check("made")
	if got := inAgent(t, files, a.Instance, "/home/dev/.config/agentbox/env"); !strings.Contains(got, "MAKEFLAGS") {
		t.Errorf("the env file doesn't set the worker counts:\n%s", got)
	}

	if err := os.Remove(filepath.Join(files, a.Instance, bashEnv)); err != nil {
		t.Fatal(err)
	}
	if err := f.m.PrepareChatModel(ctx, a, "", 0); err != nil {
		t.Fatal(err)
	}
	check("after a chat started")
}
