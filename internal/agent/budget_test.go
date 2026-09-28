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
			"Reserves 10.0 GiB of this host's 30.0 GiB of memory for your own apps, and keeps 4 of its 16 cores free; agents may use 8.0 GiB of its 16.0 GiB of zram swap. Agents may borrow the reserved memory while your apps aren't using it, and give it back first when they are.",
		},
		{
			// A third of 16 is less than 6 GiB: the host keeps 6.
			"16 GiB, disk swap", HostResources{Memory: 16 * gib, Swap: 4 * gib, SwapKind: "disk", Cores: 8},
			Budget{Memory: "10GiB", Swap: "2GiB", CPU: 6},
			"Reserves 6.0 GiB of this host's 16.0 GiB of memory for your own apps, and keeps 2 of its 8 cores free; agents may use 2.0 GiB of its 4.0 GiB of swap. Agents may borrow the reserved memory while your apps aren't using it, and give it back first when they are.",
		},
		{
			// Too small to leave 6 GiB and have 1 GiB for agents: half.
			"6 GiB, no swap", HostResources{Memory: 6 * gib, Cores: 2},
			Budget{Memory: "3GiB", CPU: 1},
			"Reserves 3.0 GiB of this host's 6.0 GiB of memory for your own apps, and keeps 1 of its 2 cores free. Agents may borrow the reserved memory while your apps aren't using it, and give it back first when they are.",
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
		{Budget{Memory: "20GiB", Swap: "4GiB", CPU: 12}, ""},
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
	old := BudgetDir
	BudgetDir = filepath.Join("/cg", BudgetCgroup)
	t.Cleanup(func() { BudgetDir = old })
	values := func(on bool, b Budget, host int64) map[string]string {
		out := map[string]string{}
		for _, w := range budgetValues(on, b, host) {
			out[strings.TrimPrefix(w.path, "/cg/")] = w.value
		}
		return out
	}
	b := Budget{Memory: "20GiB", Swap: "8GiB", CPU: 12}
	off := values(false, b, 30*gib)
	for name, want := range map[string]string{
		"agentbox/memory.max": "max", "agentbox/memory.swap.max": "max", "agentbox/cpu.max": "max 100000",
		"user.slice/memory.low": "0", "system.slice/memory.low": "0",
	} {
		if off[name] != want {
			t.Errorf("off: %s = %q, want %q", name, off[name], want)
		}
	}
	if _, ok := off["agentbox/memory.high"]; ok {
		t.Error("memory.high is written: the budget has none")
	}
	// The host that froze: 30 GiB, 20 for agents. The other 10 are the
	// host's apps', and agents are only held off the last 2.
	on := values(true, b, 30*gib)
	for name, want := range map[string]string{
		"agentbox/memory.max":      "30064771072", // 28 GiB
		"agentbox/memory.swap.max": "8589934592",
		"agentbox/cpu.max":         "1200000 100000",
		"user.slice/memory.low":    "8589934592", // 8 GiB
		"system.slice/memory.low":  "2147483648", // a quarter, at most 2 GiB
	} {
		if on[name] != want {
			t.Errorf("on: %s = %q, want %q", name, on[name], want)
		}
	}
	// The reserve is written before the agents' ceiling is lifted.
	if w := budgetValues(true, b, 30*gib); !strings.HasSuffix(w[0].path, "memory.low") || !strings.HasSuffix(w[1].path, "memory.low") {
		t.Errorf("written in the order %v", w)
	}
	// Agents given nearly all of it are held at what they were given, not
	// at the safety margin.
	if v := values(true, Budget{Memory: "29GiB", Swap: "1GiB", CPU: 1}, 30*gib)["agentbox/memory.max"]; v != "31138512896" {
		t.Errorf("29 of 30 GiB: memory.max = %q, want 29 GiB", v)
	}
	// A small reserve is shared the same way.
	if got := values(true, Budget{Memory: "3GiB", CPU: 1}, 6*gib); got["user.slice/memory.low"] != "2415919104" || got["system.slice/memory.low"] != "805306368" {
		t.Errorf("3 of 6 GiB: user %s, system %s", got["user.slice/memory.low"], got["system.slice/memory.low"])
	}
}

// budgetRoot points BudgetDir at a cgroup root of the test's own and makes
// every file the budget is written to there, as root would.
func budgetRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := BudgetDir
	BudgetDir = filepath.Join(root, BudgetCgroup)
	t.Cleanup(func() { BudgetDir = old })
	for _, path := range BudgetPaths() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("max\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestApplyBudgetWritesTheParentCgroup(t *testing.T) {
	root := t.TempDir()
	old := BudgetDir
	BudgetDir = filepath.Join(root, BudgetCgroup)
	t.Cleanup(func() { BudgetDir = old })

	if err := BudgetReady(); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Fatalf("BudgetReady with no cgroup = %v", err)
	}
	if err := os.Mkdir(BudgetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := BudgetReady(); err == nil || !strings.Contains(err.Error(), "memory.max") {
		t.Fatalf("BudgetReady with no files = %v, want it to name the missing controller file", err)
	}
	root = budgetRoot(t)
	if err := BudgetReady(); err != nil {
		t.Fatalf("BudgetReady = %v", err)
	}
	b := Budget{Memory: "2GiB", Swap: "1GiB", CPU: 2}
	if err := ApplyBudget(true, b); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		out, _ := os.ReadFile(filepath.Join(root, name))
		return strings.TrimSpace(string(out))
	}
	if got := read("agentbox/cpu.max"); got != "200000 100000" {
		t.Errorf("cpu.max = %q", got)
	}
	if got := read("user.slice/memory.low"); got == "max" || got == "0" {
		t.Errorf("user.slice's memory.low = %q, want the reserve", got)
	}
	if err := ApplyBudget(false, b); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"agentbox/memory.max": "max", "user.slice/memory.low": "0", "system.slice/memory.low": "0"} {
		if got := read(name); got != want {
			t.Errorf("off: %s = %q, want %q", name, got, want)
		}
	}

	if os.Geteuid() != 0 {
		// A slice not handed over is the budget not set up: without its
		// reserve, the budget would only lift the agents' ceiling.
		if err := os.Chmod(filepath.Join(root, "user.slice", "memory.low"), 0o444); err != nil {
			t.Fatal(err)
		}
		if err := BudgetReady(); err == nil || !strings.Contains(err.Error(), "user.slice/memory.low isn't yours to write") {
			t.Errorf("BudgetReady with a read-only memory.low = %v", err)
		}
		if err := ApplyBudget(true, b); err == nil {
			t.Error("applied with the reserve not handed over")
		}
		if got := read("agentbox/memory.max"); got != "max" {
			t.Errorf("the agents' ceiling moved with no reserve written: %q", got)
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

func TestBudgetSteps(t *testing.T) {
	if steps := budgetSteps("i", false, map[string]string{}); steps != nil {
		t.Errorf("off, nothing there: %v", steps)
	}
	steps := stepArgs(t, budgetSteps("i", true, map[string]string{}))
	if len(steps) != 1 || steps[0][1] != "set" || !strings.HasPrefix(steps[0][3], "raw.lxc=lxc.cgroup.dir.container=agentbox/i\n") {
		t.Errorf("on: %v", steps)
	}
	have := map[string]string{"raw.lxc": budgetRawLXC("", "i", true)}
	if steps := budgetSteps("i", true, have); steps != nil {
		t.Errorf("on, already there: %v", steps)
	}
	if steps := stepArgs(t, budgetSteps("i", false, have)); len(steps) != 1 || !slices.Equal(steps[0], []string{"config", "unset", "i", "raw.lxc"}) {
		t.Errorf("off: %v", steps)
	}
}

func TestLimitStepsLetAgentsSwapInsideTheBudget(t *testing.T) {
	want := Limits{CPU: "2", Memory: "8GiB"}
	find := func(steps []incusStep) string {
		for _, s := range stepArgs(t, steps) {
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
	// Inside, the budget's own memory.swap.max holds all of them, and swap is
	// where an agent's memory goes when the host's apps take their reserve.
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
