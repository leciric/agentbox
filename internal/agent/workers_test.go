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

// TestStartTakesTheCPUShareOff: a stopped agent whose machine an earlier
// release gave a share of the VM's cores (limits.cpu) has it taken off before
// it boots, so its first process sees every core; nothing sets one again,
// however many agents run beside it.
func TestStartTakesTheCPUShareOff(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	inc, calls := loggingIncus(t, fmt.Sprintf(`net='"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}'
case "$1" in
  start) touch %[1]q ;;
  list) s=Stopped; [ -f %[1]q ] && s=Running
    echo '[
    {"name":"ab-hello-stack-agent-01","status":"'$s'","config":{"limits.cpu":"2"},'"$net"'},
    {"name":"ab-hello-stack-agent-02","status":"Running","config":{},'"$net"'},
    {"name":"ab-hello-stack-agent-03","status":"Running","config":{},'"$net"'}]' ;;
  query) echo '{"config":{"limits.cpu":"2"},"expanded_config":{"limits.cpu":"2"},"devices":{}}' ;;
esac
exit 0`, started))
	f := setup(t, inc)
	addAgents(t, f.st, "agent-01", "agent-02", "agent-03")
	a, err := f.st.Agent(context.Background(), "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	all := calls()
	unset := slices.Index(all, "config unset ab-hello-stack-agent-01 limits.cpu")
	start := slices.Index(all, "start ab-hello-stack-agent-01")
	if unset < 0 || start < 0 || unset > start {
		t.Errorf("want limits.cpu unset before the start, got:\n  %s", strings.Join(all, "\n  "))
	}
	for _, call := range all {
		if strings.HasPrefix(call, "config set ") && strings.Contains(call, "limits.cpu") {
			t.Errorf("a CPU share was set: %s", call)
		}
	}
}

// TestShellsSetNoWorkerCounts: an agent's shells, its login shells through
// the env file and its Bash tool's through BASH_ENV, leave the worker counts
// to its tools, which see every core: only a heavy command is given counts,
// as it starts. BASH_ENV still brings a heavy command's join, and an agent
// made before gets bash_env again when its next chat starts.
func TestShellsSetNoWorkerCounts(t *testing.T) {
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
	counts := []string{"GOMAXPROCS", "CARGO_BUILD_JOBS", "VITEST_MAX_WORKERS", "MAKEFLAGS", "GOFLAGS", "TURBO_CONCURRENCY", "AGENTBOX_CPUS", "AGENTBOX_WORKERS", "nproc"}
	setsCounts := func(text string) string {
		for _, c := range counts {
			if strings.Contains(text, c) {
				return c
			}
		}
		return ""
	}
	const bashEnv = "/home/dev/.config/agentbox/bash_env"
	check := func(when string) {
		t.Helper()
		if got := inAgent(t, files, a.Instance, "/home/dev/.claude/settings.json"); !strings.Contains(got, `"BASH_ENV": "`+bashEnv+`"`) {
			t.Errorf("%s: settings.json doesn't point BASH_ENV at bash_env:\n%s", when, got)
		}
		got := inAgent(t, files, a.Instance, bashEnv)
		if !strings.Contains(got, agent.HeavyEnvFile) {
			t.Errorf("%s: bash_env doesn't read the heavy command's join:\n%s", when, got)
		}
		if c := setsCounts(got); c != "" {
			t.Errorf("%s: bash_env sets %s:\n%s", when, c, got)
		}
	}
	check("made")
	if got := inAgent(t, files, a.Instance, "/home/dev/.config/agentbox/env"); setsCounts(got) != "" {
		t.Errorf("the env file sets %s:\n%s", setsCounts(got), got)
	}

	if err := os.Remove(filepath.Join(files, a.Instance, bashEnv)); err != nil {
		t.Fatal(err)
	}
	if err := f.m.PrepareChatModel(ctx, a, "", 0); err != nil {
		t.Fatal(err)
	}
	check("after a chat started")
}
