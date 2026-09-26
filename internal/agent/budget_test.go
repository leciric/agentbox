package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const gib = int64(1) << 30

func TestSuggestBudget(t *testing.T) {
	for _, c := range []struct {
		name string
		host HostResources
		want Budget
		why  string
	}{
		{
			// The machine that froze: 30 GB, 16 cores, zram.
			"30 GiB with zram", HostResources{Memory: 30 * gib, Swap: 16 * gib, SwapKind: "zram", Cores: 16},
			Budget{Memory: "20GiB", Swap: "8GiB", CPU: 12},
			"Leaves this host 10.0 GiB of its 30.0 GiB of memory and 4 of its 16 cores, and lets agents use 8.0 GiB of its 16.0 GiB of zram swap.",
		},
		{
			// A third of 16 is less than 6 GiB: the host keeps 6.
			"16 GiB, disk swap", HostResources{Memory: 16 * gib, Swap: 4 * gib, SwapKind: "disk", Cores: 8},
			Budget{Memory: "10GiB", Swap: "2GiB", CPU: 6},
			"Leaves this host 6.0 GiB of its 16.0 GiB of memory and 2 of its 8 cores, and lets agents use 2.0 GiB of its 4.0 GiB of swap.",
		},
		{
			// Too small to leave 6 GiB and have 1 GiB for agents: half.
			"6 GiB, no swap", HostResources{Memory: 6 * gib, Cores: 2},
			Budget{Memory: "3GiB", CPU: 1},
			"Leaves this host 3.0 GiB of its 6.0 GiB of memory and 1 of its 2 cores. This host has no swap, so agents are only ever held at the hard limit: without swap, a soft one stalls them instead of freeing memory.",
		},
		{
			// Swap is capped at half of the agents' memory.
			"64 GiB, huge swap", HostResources{Memory: 64 * gib, Swap: 64 * gib, SwapKind: "disk", Cores: 32},
			Budget{Memory: "42GiB", Swap: "21GiB", CPU: 24},
			"",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, why := SuggestBudget(c.host)
			if got != c.want {
				t.Errorf("SuggestBudget = %+v, want %+v", got, c.want)
			}
			if c.why != "" && why != c.why {
				t.Errorf("why = %q\nwant  %q", why, c.why)
			}
			if err := got.Validate(c.host); err != nil {
				t.Errorf("the suggestion doesn't validate: %v", err)
			}
		})
	}
}

func TestBudgetValidate(t *testing.T) {
	host := HostResources{Memory: 30 * gib, Swap: 8 * gib, Cores: 16}
	for _, c := range []struct {
		b    Budget
		want string
	}{
		{Budget{Memory: "20GiB", Swap: "4GiB", CPU: 12}, ""},
		{Budget{Memory: "50%", Swap: "4GiB", CPU: 12}, "a size like 20GiB"},
		{Budget{Memory: "40GiB", Swap: "4GiB", CPU: 12}, "more than this host has"},
		{Budget{Memory: "100MiB", Swap: "4GiB", CPU: 12}, "at least 512MiB"},
		{Budget{Memory: "20GiB", Swap: "", CPU: 12}, "never 0"},
		{Budget{Memory: "20GiB", Swap: "0", CPU: 12}, "never 0"},
		{Budget{Memory: "20GiB", Swap: "4GiB", CPU: 0}, "between 1 and 16"},
		{Budget{Memory: "20GiB", Swap: "4GiB", CPU: 17}, "between 1 and 16"},
	} {
		err := c.b.Validate(host)
		if c.want == "" {
			if err != nil {
				t.Errorf("%+v: %v", c.b, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: got %v, want an error with %q", c.b, err, c.want)
		}
	}
	// A host with no swap has nothing to give: no swap is fine there.
	if err := (Budget{Memory: "3GiB", CPU: 1}).Validate(HostResources{Memory: 6 * gib, Cores: 2}); err != nil {
		t.Errorf("no swap on a host with none: %v", err)
	}
}

func TestBudgetValues(t *testing.T) {
	b := Budget{Memory: "20GiB", Swap: "8GiB", CPU: 12}
	off := budgetValues(false, b, 16*gib, 0)
	for name, want := range map[string]string{"memory.high": "max", "memory.max": "max", "memory.swap.max": "max", "cpu.max": "max 100000"} {
		if off[name] != want {
			t.Errorf("off: %s = %q, want %q", name, off[name], want)
		}
	}
	on := budgetValues(true, b, 16*gib, 0)
	for name, want := range map[string]string{
		"memory.max": "21474836480", "memory.swap.max": "8589934592", "cpu.max": "1200000 100000",
		"memory.high": "19327352832", // 90% of 20 GiB
	} {
		if on[name] != want {
			t.Errorf("on: %s = %q, want %q", name, on[name], want)
		}
	}
	// The thrash case: memory.high with nothing to reclaim into stalls every
	// process over it, so it is lifted to max when the host has no swap...
	if v := budgetValues(true, Budget{Memory: "3GiB", CPU: 1}, 0, 0)["memory.high"]; v != "max" {
		t.Errorf("no host swap: memory.high = %q, want max", v)
	}
	// ...and while the budget's own swap is nearly used up.
	if v := budgetValues(true, b, 16*gib, 8*gib-100<<20)["memory.high"]; v != "max" {
		t.Errorf("swap nearly full: memory.high = %q, want max", v)
	}
	if v := budgetValues(true, b, 16*gib, 4*gib)["memory.high"]; v == "max" {
		t.Error("swap half used: memory.high should still be held below memory.max")
	}
}

func TestApplyBudgetWritesTheParentCgroup(t *testing.T) {
	dir := t.TempDir()
	old := BudgetDir
	BudgetDir = dir
	t.Cleanup(func() { BudgetDir = old })

	if err := BudgetReady(); err == nil || !strings.Contains(err.Error(), "memory.high") {
		t.Fatalf("BudgetReady with no files = %v, want it to name the missing controller file", err)
	}
	for _, name := range budgetFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("max\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := BudgetReady(); err != nil {
		t.Fatalf("BudgetReady = %v", err)
	}
	b := Budget{Memory: "2GiB", Swap: "1GiB", CPU: 2}
	if err := ApplyBudget(true, b); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		out, _ := os.ReadFile(filepath.Join(dir, name))
		return string(out)
	}
	if got := read("memory.max"); got != "2147483648" {
		t.Errorf("memory.max = %q", got)
	}
	if got := read("cpu.max"); got != "200000 100000" {
		t.Errorf("cpu.max = %q", got)
	}
	if err := ApplyBudget(false, b); err != nil {
		t.Fatal(err)
	}
	if got := read("memory.max"); got != "max" {
		t.Errorf("off: memory.max = %q, want max", got)
	}

	if err := os.Chmod(filepath.Join(dir, "cpu.max"), 0o444); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if err := BudgetReady(); err == nil || !strings.Contains(err.Error(), "isn't yours to write") {
			t.Errorf("BudgetReady with a read-only cpu.max = %v", err)
		}
	}
}

func TestBudgetRawLXC(t *testing.T) {
	on := budgetRawLXC("", "agentbox-a-1", true)
	want := "lxc.cgroup.dir.container=agentbox/agentbox-a-1\nlxc.cgroup.dir.monitor=agentbox/agentbox-a-1.monitor"
	if on != want {
		t.Errorf("on = %q", on)
	}
	if got := budgetRawLXC(on, "agentbox-a-1", true); got != on {
		t.Errorf("not idempotent: %q", got)
	}
	if got := budgetRawLXC(on, "agentbox-a-1", false); got != "" {
		t.Errorf("off = %q, want empty", got)
	}
	// Anything else in raw.lxc is somebody's, and stays.
	mine := "lxc.apparmor.profile=unconfined"
	if got := budgetRawLXC(mine+"\n"+on, "agentbox-a-1", false); got != mine {
		t.Errorf("off keeps others: %q", got)
	}
	// A copy of another agent carries that agent's placement: it's replaced.
	if got := budgetRawLXC(on, "agentbox-a-2", true); strings.Contains(got, "a-1") {
		t.Errorf("a copy kept the other agent's cgroup: %q", got)
	}
}

func TestBudgetSteps(t *testing.T) {
	if steps := budgetSteps("i", false, map[string]string{}); steps != nil {
		t.Errorf("off, nothing there: %v", steps)
	}
	steps := budgetSteps("i", true, map[string]string{})
	if len(steps) != 1 || steps[0][1] != "set" || !strings.HasPrefix(steps[0][3], "raw.lxc=lxc.cgroup.dir.container=agentbox/i\n") {
		t.Errorf("on: %v", steps)
	}
	have := map[string]string{"raw.lxc": budgetRawLXC("", "i", true)}
	if steps := budgetSteps("i", true, have); steps != nil {
		t.Errorf("on, already there: %v", steps)
	}
	if steps := budgetSteps("i", false, have); !slices.Equal(steps[0], []string{"config", "unset", "i", "raw.lxc"}) {
		t.Errorf("off: %v", steps)
	}
}

func TestLimitStepsLetAgentsSwapInsideTheBudget(t *testing.T) {
	want := Limits{CPU: "2", Memory: "8GiB"}
	find := func(steps [][]string) string {
		for _, s := range steps {
			for _, arg := range s {
				if v, ok := strings.CutPrefix(arg, limitMemorySwap+"="); ok {
					return v
				}
			}
		}
		return ""
	}
	if got := find(limitSteps("i", want, map[string]string{}, false)); got != MemorySwap {
		t.Errorf("outside the budget: swap = %q, want %q", got, MemorySwap)
	}
	// Inside, the budget's own memory.swap.max holds all of them, and an
	// agent kept out of swap would stall at the budget's memory.high.
	if got := find(limitSteps("i", want, map[string]string{}, true)); got != "true" {
		t.Errorf("inside the budget: swap = %q, want true", got)
	}
}

func TestAgentCgroupFollowsTheBudget(t *testing.T) {
	root := t.TempDir()
	if got := agentCgroup(root, "a"); got != filepath.Join(root, "lxc.payload.a") {
		t.Errorf("outside: %s", got)
	}
	if err := os.MkdirAll(filepath.Join(root, BudgetCgroup, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := agentCgroup(root, "a"); got != filepath.Join(root, BudgetCgroup, "a") {
		t.Errorf("inside: %s", got)
	}
}
