package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// TestSharedBudget goes through the setting the way Settings does: off with a
// suggestion, refused until the cgroup is set up, then on — which writes the
// budget into the cgroup and the host's reserve into its slices, and puts
// every agent's raw.lxc in it — resized live, and off again. Not parallel: it
// points agent.BudgetDir at a cgroup root of its own.
func TestSharedBudget(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, agent.BudgetCgroup)
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
	if b.HostMemory != agent.HostMemory() {
		t.Errorf("HostMemory = %d", b.HostMemory)
	}
	// Off until the user turns it on, and Setup says where.
	if status, err := d.client.Setup(ctx); err != nil {
		t.Fatal(err)
	} else {
		detail := ""
		for _, c := range status.Checks {
			if c.ID == "budget" {
				detail = c.Detail
			}
		}
		if !strings.HasPrefix(detail, "off: turn it on in Settings") {
			t.Errorf("Setup's budget check says %q", detail)
		}
	}

	// Refused, and clearly, before the cgroup exists.
	if _, err := patchSettings(t, d, `{"sharedBudget":true}`); err == nil || !strings.Contains(err.Error(), "can't be turned on yet") {
		t.Fatalf("turning it on with no cgroup = %v", err)
	}

	for _, path := range agent.BudgetPaths() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("max\n"), 0o644); err != nil {
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
	// The agents are held a safety margin short of the host's memory, and
	// what's past their 2 GiB is reserved for the host's apps.
	ceiling := func(agents int64) string { return strconv.FormatInt(max(agents, agent.HostMemory()-2<<30), 10) }
	reserve := func() int64 {
		var sum int64
		for _, slice := range []string{"user.slice", "system.slice"} {
			b, _ := os.ReadFile(filepath.Join(root, slice, "memory.low"))
			n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
			sum += n
		}
		return sum
	}
	if read("memory.max") != ceiling(2<<30) || read("cpu.max") != "100000 100000" || read("memory.swap.max") != "1073741824" {
		t.Errorf("the cgroup has memory.max=%s cpu.max=%s memory.swap.max=%s", read("memory.max"), read("cpu.max"), read("memory.swap.max"))
	}
	if got, want := reserve(), max(agent.HostMemory()-2<<30, 0); got != want {
		t.Errorf("reserved %d for the host's apps, want %d", got, want)
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
	if read("memory.max") != ceiling(3<<30) {
		t.Errorf("resized: memory.max=%s", read("memory.max"))
	}
	if got, want := reserve(), max(agent.HostMemory()-3<<30, 0); got != want {
		t.Errorf("resized: reserved %d, want %d", got, want)
	}

	// The agents in it thrashing together reach Settings, though the one
	// agent inside is well under the bar on its own.
	cgroups := t.TempDir()
	d.srv.thrash.Root = cgroups
	inside := filepath.Join(cgroups, agent.BudgetCgroup)
	if err := os.MkdirAll(filepath.Join(inside, "ab-p-a1"), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	for i := range 7 {
		n := int64(i) * 100_000
		for dir, files := range map[string]map[string]string{
			inside: {
				"memory.pressure":     "some avg10=50.00 avg60=40.00 avg300=10.00 total=1\n",
				"memory.stat":         fmt.Sprintf("workingset_refault_file %d\n", n),
				"memory.events":       fmt.Sprintf("high 0\nmax %d\n", n),
				"memory.events.local": fmt.Sprintf("high 0\nmax %d\n", n),
				"memory.max":          "3221225472\n",
			},
			filepath.Join(inside, "ab-p-a1"): {
				"memory.pressure": "some avg10=0.00 avg60=5.00 avg300=0.00 total=1\n",
				"memory.max":      "max\n",
			},
		} {
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		d.srv.thrash.Sample(ctx, at)
		at = at.Add(agent.ThrashInterval)
	}
	s, err = patchSettings(t, d, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if short := s.SharedBudget.Shortage; short == nil || short.Pressure != 40 || short.Limit != 3<<30 {
		t.Errorf("Shortage = %+v, want the agents together at 40%%", short)
	}

	// Off: nothing holds the agents still inside, and their raw.lxc goes.
	s, err = patchSettings(t, d, `{"sharedBudget":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if s.SharedBudget.On || read("memory.max") != "max" || read("cpu.max") != "max 100000" || reserve() != 0 {
		t.Errorf("off: %+v memory.max=%s cpu.max=%s reserve=%d", s.SharedBudget, read("memory.max"), read("cpu.max"), reserve())
	}
}
