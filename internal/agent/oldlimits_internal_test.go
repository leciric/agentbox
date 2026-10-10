package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const gib = int64(1) << 30

// stepArgs runs steps against a stand-in incus command and returns the
// command line each one ran.
func stepArgs(t *testing.T, steps []incusStep) [][]string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "log")
	c := fakeIncus(t, `for a; do printf '%s\037' "$a"; done >> `+log+`; printf '\036' >> `+log)
	for _, step := range steps {
		if err := step(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	out, _ := os.ReadFile(log)
	var all [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\036"), "\036") {
		if line != "" {
			all = append(all, strings.Split(strings.TrimSuffix(line, "\037"), "\037"))
		}
	}
	return all
}

// What an earlier release set on a machine comes off, key by key, and its
// place in the shared budget's cgroup with it; a machine with none of it is
// left alone, and so is anything else in raw.lxc. limits.cpu stays: it's the
// agent's CPU share now, which the daemon replaces (cpushare.go).
func TestOldLimitSteps(t *testing.T) {
	if steps := oldLimitSteps("i", map[string]string{"raw.idmap": "both 1000 1000"}, map[string]map[string]string{"worktree": {"type": "disk"}}); steps != nil {
		t.Errorf("nothing of an earlier release's: %d steps", len(steps))
	}
	placement := "lxc.cgroup.dir.container=agentbox/i\nlxc.cgroup.dir.monitor=agentbox/i.monitor"
	steps := stepArgs(t, oldLimitSteps("i", map[string]string{
		"limits.cpu": "2", "limits.memory": "8GiB", "limits.cpu.priority": "5", "limits.memory.swap": "false",
		"user.agentbox.cpu.configured": "2", "nvidia.runtime": "true", "raw.lxc": placement,
	}, map[string]map[string]string{"agentbox-gpu": {"type": "gpu"}}))
	want := [][]string{
		{"config", "device", "remove", "i", "agentbox-gpu"},
		{"config", "unset", "i", "limits.memory"},
		{"config", "unset", "i", "limits.cpu.priority"},
		{"config", "unset", "i", "limits.memory.swap"},
		{"config", "unset", "i", "user.agentbox.cpu.configured"},
		{"config", "unset", "i", "nvidia.runtime"},
		{"config", "unset", "i", "raw.lxc"},
	}
	if !slices.EqualFunc(steps, want, slices.Equal) {
		t.Errorf("steps = %q, want %q", steps, want)
	}

	mine := "lxc.apparmor.profile=unconfined"
	steps = stepArgs(t, oldLimitSteps("i", map[string]string{"raw.lxc": mine + "\n" + placement}, nil))
	if len(steps) != 1 || !slices.Equal(steps[0], []string{"config", "set", "i", "raw.lxc=" + mine}) {
		t.Errorf("raw.lxc with something of somebody else's: %q", steps)
	}
}

// A machine an earlier release started inside the shared budget is measured
// there until it restarts.
func TestAgentCgroupFindsTheOldBudget(t *testing.T) {
	root := t.TempDir()
	if got := agentCgroup(root, "a"); got != filepath.Join(root, "lxc.payload.a") {
		t.Errorf("outside: %s", got)
	}
	if err := os.MkdirAll(filepath.Join(root, oldBudgetCgroup, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := agentCgroup(root, "a"); got != filepath.Join(root, oldBudgetCgroup, "a") {
		t.Errorf("inside: %s", got)
	}
}

// Starting or restoring one machine reads its own configuration and devices
// and takes off what an earlier release left there; one with nothing left is
// only read.
func TestDropOldLimitsFromOneMachine(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	m := &Manager{Incus: fakeIncus(t, `case "$1" in
  query) case "$2" in
    */capped) echo '{"config":{"limits.memory":"8GiB"},"devices":{"agentbox-gpu":{"type":"gpu"}}}' ;;
    *) echo '{"config":{},"devices":{}}' ;;
  esac ;;
  *) echo "$*" >> `+log+` ;;
esac`)}
	ctx := context.Background()
	if err := m.dropOldLimits(ctx, "plain"); err != nil {
		t.Fatal(err)
	}
	if out, _ := os.ReadFile(log); len(out) > 0 {
		t.Errorf("a machine with nothing to take off was changed: %q", out)
	}
	if err := m.dropOldLimits(ctx, "capped"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(log)
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	want := []string{"config device remove capped agentbox-gpu", "config unset capped limits.memory"}
	if !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}
