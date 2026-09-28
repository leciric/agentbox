package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// budgetIncus answers list and query from files, and logs every config call.
const budgetIncus = `#!/bin/sh
case "$1" in
  list) cat "$(dirname "$0")/instances" ;;
  query) cat "$(dirname "$0")/details" ;;
  config) echo "$*" >> "$(dirname "$0")/log" ;;
esac
exit 0
`

// budgetManager is a Manager with a real store, an incus that answers from
// files next to it, and a BudgetDir of its own, set up as root would.
func budgetManager(t *testing.T, instances, details string) (*Manager, func() string) {
	t.Helper()
	old := BudgetDir
	BudgetDir = t.TempDir()
	t.Cleanup(func() { BudgetDir = old })
	for _, name := range budgetFiles {
		if err := os.WriteFile(filepath.Join(BudgetDir, name), []byte("max\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	for name, body := range map[string]string{"incus": budgetIncus, "instances": instances, "details": details} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a1", "a2"} {
		if err := st.AddAgent(ctx, state.Agent{
			Project: "p", Name: name, Instance: "ab-p-" + name, AI: "none",
			Branch: "agentbox/" + name, Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return &Manager{Store: st, Incus: incus.Client{Bin: filepath.Join(dir, "incus")}}, func() string {
		b, _ := os.ReadFile(filepath.Join(dir, "log"))
		return string(b)
	}
}

func TestApplySharedBudgetPlacesEveryAgent(t *testing.T) {
	// a1 has a memory limit, so it's let into swap inside the budget; a2 has
	// none, and gets no swap setting at all.
	instances := `[{"name":"ab-p-a1","status":"Running","config":{"limits.memory":"8GiB","limits.memory.swap":"false"},"expanded_config":{"limits.memory":"8GiB"}},` +
		`{"name":"ab-p-a2","status":"Stopped","config":{},"expanded_config":{}}]`
	m, log := budgetManager(t, instances, `{"config":{},"devices":{}}`)
	ctx := context.Background()

	if on, b, err := m.SharedBudget(ctx); err != nil || on || b.Memory == "" || b.CPU < 1 {
		t.Fatalf("SharedBudget = %v %+v %v, want it off with the suggestion", on, b, err)
	}
	for key, value := range map[string]string{state.SettingSharedBudgetMemory: "2GiB", state.SettingSharedBudgetCPU: "1"} {
		if err := m.Store.SetSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Store.SetFlag(ctx, state.SettingSharedBudget, true); err != nil {
		t.Fatal(err)
	}
	pending, err := m.ApplySharedBudget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// a1 runs, but not in this test's BudgetDir: it moves in when it restarts.
	if pending != 1 {
		t.Errorf("pending = %d, want 1", pending)
	}
	got := log()
	for _, want := range []string{
		"config set ab-p-a1 raw.lxc=lxc.cgroup.dir.container=agentbox/ab-p-a1",
		"config set ab-p-a2 raw.lxc=lxc.cgroup.dir.container=agentbox/ab-p-a2",
		"config set ab-p-a1 limits.memory.swap=true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ab-p-a2 limits.memory.swap") {
		t.Errorf("an agent with no memory limit got a swap setting:\n%s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(BudgetDir, "memory.max")); string(b) != "2147483648" {
		t.Errorf("memory.max = %q", b)
	}
	if err := os.MkdirAll(filepath.Join(BudgetDir, "ab-p-a1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !InBudget("ab-p-a1") || InBudget("ab-p-a2") || RunningOutsideBudget("ab-p-nothing") {
		t.Error("InBudget/RunningOutsideBudget don't follow the cgroup directories")
	}
}

func TestApplySharedBudgetSaysTheSettingIsSaved(t *testing.T) {
	m, _ := budgetManager(t, `not json`, `{}`)
	_, err := m.ApplySharedBudget(context.Background())
	if err == nil || !strings.Contains(err.Error(), "is saved") {
		t.Errorf("ApplySharedBudget with a broken incus = %v, want it to say the setting is saved", err)
	}
}

func TestEnsureBudgetPlacement(t *testing.T) {
	// Made before the budget was turned on: moved in at this start.
	m, log := budgetManager(t, `[]`, `{"config":{},"devices":{}}`)
	ctx := context.Background()
	if err := m.Store.SetFlag(ctx, state.SettingSharedBudget, true); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureBudgetPlacement(ctx, "ab-p-a1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log(), "config set ab-p-a1 raw.lxc=lxc.cgroup.dir.container=agentbox/ab-p-a1") {
		t.Errorf("not placed: %s", log())
	}

	// Already where it belongs: nothing to do.
	m, log = budgetManager(t, `[]`, `{"config":{"raw.lxc":"lxc.cgroup.dir.container=agentbox/ab-p-a1\nlxc.cgroup.dir.monitor=agentbox/ab-p-a1.monitor"},"devices":{}}`)
	if err := m.Store.SetFlag(ctx, state.SettingSharedBudget, true); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureBudgetPlacement(ctx, "ab-p-a1"); err != nil {
		t.Fatal(err)
	}
	if log() != "" {
		t.Errorf("an agent already in place was changed: %s", log())
	}
}

func TestBudgetDescribeAndHost(t *testing.T) {
	for b, want := range map[Budget]string{
		{Memory: "20GiB", Swap: "8GiB", CPU: 12}: "20GiB of memory, 8GiB of swap and 12 cores",
		{Memory: "3GiB", CPU: 1}:                 "3GiB of memory, no swap and 1 core",
		{Memory: "20GiB", Swap: "8GiB", CPU: 12, DiskWeight: 10, DiskWrite: "64MiB"}: "20GiB of memory, 8GiB of swap and 12 cores; disk weight 10, writes up to 64MiB/s",
		{Memory: "20GiB", Swap: "8GiB", CPU: 12, DiskWeight: 25, DiskWrite: "max"}:   "20GiB of memory, 8GiB of swap and 12 cores; disk weight 25, no write ceiling",
	} {
		if got := b.Describe(); got != want {
			t.Errorf("Describe(%+v) = %q, want %q", b, got, want)
		}
	}
	h := ReadHostResources()
	if h.Memory <= 0 || h.Cores < 1 {
		t.Errorf("ReadHostResources = %+v", h)
	}
	_ = BudgetSupport() // depends on the machine; it mustn't panic
}
