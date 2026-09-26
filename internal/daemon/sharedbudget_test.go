package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// TestSharedBudget goes through the setting the way Settings does: off with a
// suggestion, refused until the cgroup is set up, then on — which writes the
// budget into the cgroup and puts every agent's raw.lxc in it — resized live,
// and off again. Not parallel: it points agent.BudgetDir at a directory of
// its own.
func TestSharedBudget(t *testing.T) {
	dir := t.TempDir()
	old := agent.BudgetDir
	agent.BudgetDir = dir
	t.Cleanup(func() { agent.BudgetDir = old })
	if agent.BudgetSupport() != "" {
		t.Skip(agent.BudgetSupport())
	}

	d := startTestDaemon(t, t.TempDir(), cpuBudgetIncus, testConfig{instances: cpuBudgetInstances(2, "Running", "Stopped")})
	ctx := context.Background()
	if err := d.srv.store.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a1", "a2"} {
		if err := d.srv.store.AddAgent(ctx, state.Agent{
			Project: "p", Name: name, Instance: "ab-p-" + name, AI: "none",
			Branch: "agentbox/" + name, Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	log := func() string {
		b, _ := os.ReadFile(filepath.Join(d.root, "incus.log"))
		return string(b)
	}

	s, err := patchSettings(t, d, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	b := s.SharedBudget
	if b.On || b.Chosen || b.Memory == "" || b.CPU < 1 || b.Why == "" || b.Memory != b.Suggested.Memory {
		t.Errorf("a new installation's budget = %+v, want it off and following the suggestion", b)
	}
	if !strings.Contains(b.NotReady, "only root can make") {
		t.Errorf("NotReady = %q, want it to say what's missing", b.NotReady)
	}

	// Refused, and clearly, before the cgroup exists.
	if _, err := patchSettings(t, d, `{"sharedBudget":true}`); err == nil || !strings.Contains(err.Error(), "can't be turned on yet") {
		t.Fatalf("turning it on with no cgroup = %v", err)
	}

	for _, name := range []string{"memory.high", "memory.max", "memory.swap.max", "cpu.max"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("max\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A1 runs outside the budget: it moves in when it restarts.
	s, err = patchSettings(t, d, `{"sharedBudget":true,"sharedBudgetMemory":"2GiB","sharedBudgetCPU":1,"sharedBudgetSwap":"1GiB"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !s.SharedBudget.On || s.SharedBudget.NotReady != "" || !s.SharedBudget.Chosen {
		t.Errorf("after turning it on: %+v", s.SharedBudget)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b))
	}
	if read("memory.max") != "2147483648" || read("cpu.max") != "100000 100000" || read("memory.swap.max") != "1073741824" {
		t.Errorf("the cgroup has memory.max=%s cpu.max=%s memory.swap.max=%s", read("memory.max"), read("cpu.max"), read("memory.swap.max"))
	}
	for _, inst := range []string{"ab-p-a1", "ab-p-a2"} {
		if want := "config set " + inst + " raw.lxc=lxc.cgroup.dir.container=agentbox/" + inst; !strings.Contains(log(), want) {
			t.Errorf("no %q in:\n%s", want, log())
		}
	}

	// Refused: more cores than the host has, and no swap on a host that has it.
	if _, err := patchSettings(t, d, `{"sharedBudgetCPU":100000}`); err == nil {
		t.Error("a budget of 100000 cores was taken")
	}
	if read("cpu.max") != "100000 100000" {
		t.Errorf("a refused change was applied: cpu.max=%s", read("cpu.max"))
	}

	// Resized while on: applied at once.
	if _, err := patchSettings(t, d, `{"sharedBudgetMemory":"3GiB"}`); err != nil {
		t.Fatal(err)
	}
	if read("memory.max") != "3221225472" {
		t.Errorf("resized: memory.max=%s", read("memory.max"))
	}

	// Off: nothing holds the agents still inside, and their raw.lxc goes.
	s, err = patchSettings(t, d, `{"sharedBudget":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if s.SharedBudget.On || read("memory.max") != "max" || read("cpu.max") != "max 100000" {
		t.Errorf("off: %+v memory.max=%s cpu.max=%s", s.SharedBudget, read("memory.max"), read("cpu.max"))
	}
}
