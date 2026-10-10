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
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// loggingIncus is fakeIncus that also writes down every command it was given.
// Taking an earlier release's limits off is nothing but incus calls, so what
// it really did is exactly the list of commands it ran. (recordingIncus, next door, keeps the files
// AgentBox writes *into* an agent instead.)
func loggingIncus(t *testing.T, script string) (incus.Client, func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	path := filepath.Join(dir, "incus")
	body := fmt.Sprintf("#!/bin/sh\nfor arg in \"$@\"; do printf '%%s ' \"$arg\" >> %q; done\nprintf '\\n' >> %q\n%s", log, log, script)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return incus.Client{Bin: path}, func() []string {
		t.Helper()
		data, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		var calls []string
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				calls = append(calls, line)
			}
		}
		return calls
	}
}

// oneCall finds the single call starting with prefix, and fails when there
// isn't exactly one: a change made twice is as wrong as one never made.
func oneCall(t *testing.T, calls []string, prefix string) string {
	t.Helper()
	var found []string
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			found = append(found, call)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %q call, got %d:\n  %s", prefix, len(found), strings.Join(calls, "\n  "))
	}
	return found[0]
}

func noCall(t *testing.T, calls []string, substring string) {
	t.Helper()
	for _, call := range calls {
		if strings.Contains(call, substring) {
			t.Errorf("no call should mention %q, but one did: %s", substring, call)
		}
	}
}

// createScript answers the incus calls a Create makes: the base image is
// ready, the new instance has no configuration or devices of its own, and
// every agent-NN gets an address so WaitReady finishes.
const createScript = `case "$1" in
  list) echo '[
    {"name":"ab-hello-stack-agent-01","status":"Running","config":{},"expanded_config":{},"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}},"memory":{"usage":1073741824}}},
    {"name":"ab-hello-stack-agent-02","status":"Running","config":{},"expanded_config":{},"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}},"memory":{"usage":1073741824}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots*) echo '[]' ;;
      *) echo '{"config": {}, "expanded_config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0`

// TestCreateSetsNoLimits: a new agent's machine is made with nothing capped —
// the VM it runs in is what agents share.
func TestCreateSetsNoLimits(t *testing.T) {
	inc, calls := loggingIncus(t, createScript)
	f := setup(t, inc)
	if _, err := f.m.Create(context.Background(), "hello-stack", agent.CreateOptions{AI: "none"}); err != nil {
		t.Fatal(err)
	}
	noCall(t, calls(), "limits.")
	noCall(t, calls(), "raw.lxc")
}

// TestSaveBaseDropsOldLimits: a base saved from a machine an earlier release
// capped doesn't pass the caps on to every agent made from it.
func TestSaveBaseDropsOldLimits(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-base-next","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.9"}]}}}}]' ;;
  query) echo '{"config":{"limits.cpu":"12","limits.memory":"16GiB","limits.cpu.priority":"5","limits.memory.swap":"false","user.agentbox.saved-from":"hello-stack/agent-01"},"expanded_config":{},"devices":{"worktree":{"type":"disk"}}}' ;;
esac
exit 0`)
	f := setup(t, inc)
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01"}

	if _, err := f.m.SaveBase(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	const next = "ab-hello-stack-base-next"
	var unset []string
	for _, call := range calls() {
		if after, ok := strings.CutPrefix(call, "config unset "+next+" "); ok {
			unset = append(unset, after)
		}
	}
	want := []string{"limits.cpu", "limits.memory", "limits.cpu.priority", "limits.memory.swap"}
	if !slices.Equal(unset, want) {
		t.Errorf("the base was stripped of %v, want %v", unset, want)
	}
	// And the devices that belong to one agent still go, as they always did.
	oneCall(t, calls(), "config device remove "+next+" worktree")
}

// TestDropOldLimits: on daemon start, every agent's machine loses what an
// earlier release capped it at, and a machine that isn't an agent's is left
// alone.
func TestDropOldLimits(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[
    {"name":"ab-hello-stack-agent-01","status":"Running","config":{"limits.cpu":"2","limits.memory":"8GiB","raw.lxc":"lxc.cgroup.dir.container=agentbox/ab-hello-stack-agent-01"}},
    {"name":"ab-hello-stack-agent-02","status":"Stopped","config":{}},
    {"name":"someone-elses","status":"Running","config":{"limits.cpu":"1"}}]' ;;
esac
exit 0`)
	f := setup(t, inc)
	ctx := context.Background()
	for _, name := range []string{"agent-01", "agent-02"} {
		if err := f.st.AddAgent(ctx, state.Agent{
			Project: "hello-stack", Name: name, Instance: "ab-hello-stack-" + name,
			Branch: "agentbox/" + name, Worktree: filepath.Join(t.TempDir(), name),
			Status: state.AgentReady, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := f.m.DropOldLimits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Errorf("changed %d machines, want 1", changed)
	}
	var unset []string
	for _, call := range calls() {
		if after, ok := strings.CutPrefix(call, "config unset "); ok {
			unset = append(unset, strings.TrimSpace(after))
		}
	}
	// limits.cpu is the agent's CPU share now, which BalanceCPU replaces.
	want := []string{"ab-hello-stack-agent-01 limits.memory", "ab-hello-stack-agent-01 raw.lxc"}
	if !slices.Equal(unset, want) {
		t.Errorf("unset %q, want %q", unset, want)
	}
	noCall(t, calls(), "someone-elses")
}

// TestParseBytes checks the sizes AgentBox reads, the way Incus writes them.
func TestParseBytes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in   string
		want int64
	}{
		{"8GiB", 8 << 30},
		{"4096MiB", 4096 << 20},
		{"512KiB", 512 << 10},
		{"2GB", 2_000_000_000},
		{"1024", 1024},
		{" 8GiB ", 8 << 30},
	} {
		got, err := agent.ParseBytes(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseBytes(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "lots", "8 gigs", "-1GiB", "0", "GiB"} {
		if _, err := agent.ParseBytes(bad); err == nil {
			t.Errorf("ParseBytes(%q) was accepted", bad)
		}
	}
}
