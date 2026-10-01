package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// budgetRoot points the old budget's cgroup at a directory of the test's own,
// with every file the budget wrote at the kernel's defaults, and returns the
// cgroup root it is under.
func budgetRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := OldBudgetDir
	OldBudgetDir = filepath.Join(root, oldBudgetCgroup)
	t.Cleanup(func() { OldBudgetDir = old })
	for name, value := range map[string]string{
		oldBudgetCgroup + "/memory.max": "max\n", oldBudgetCgroup + "/memory.swap.max": "max\n", oldBudgetCgroup + "/cpu.max": "max 100000\n",
		"user.slice/memory.low": "0\n", "system.slice/memory.low": "0\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// What the budget used to set — on the disk, on its agents and as the host's
// reserve — is put back on daemon start; a QoS the host set itself, and files
// already at their defaults, are left alone.
func TestResetLegacyBudget(t *testing.T) {
	root := budgetRoot(t)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(root, name))
		return string(b)
	}
	// cgroupfs files take one line a write and read back every line; a
	// plain file keeps only the last write, which is enough to see what was
	// written, one device at a time.
	write("io.cost.qos", "259:4 enable=1 ctrl=user rpct=95.00 rlat=5000 wpct=95.00 wlat=10000 min=5.00 max=150.00\n")
	write("agentbox/io.max", "253:0 rbps=max wbps=16777216 riops=max wiops=max\n")
	write("agentbox/io.weight", "default 10\n")
	write("agentbox/memory.high", "15461882265\n")
	write("agentbox/memory.max", "31138512896\n")
	write("agentbox/memory.swap.max", "8589934592\n")
	write("agentbox/cpu.max", "1200000 100000\n")
	write("user.slice/memory.low", "9663676416\n")
	write("system.slice/memory.low", "2147483648\n")
	reset, err := ResetLegacyBudget()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(reset)
	if want := []string{
		"io.cost on 259:4", "the budget's cpu.max", "the budget's io.max on 253:0", "the budget's io.weight", "the budget's memory.high",
		"the budget's memory.max", "the budget's memory.swap.max", "the reserve on system.slice", "the reserve on user.slice",
	}; !slices.Equal(reset, want) {
		t.Errorf("reset %q, want %q", reset, want)
	}
	for name, want := range map[string]string{
		"io.cost.qos": "259:4 enable=0", "agentbox/io.max": "253:0 rbps=max wbps=max riops=max wiops=max",
		"agentbox/io.weight": "default 100", "agentbox/memory.high": "max",
		"agentbox/memory.max": "max", "agentbox/memory.swap.max": "max", "agentbox/cpu.max": "max 100000",
		"user.slice/memory.low": "0", "system.slice/memory.low": "0",
	} {
		if got := read(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	// The host's own io.cost, and what's at its defaults, stay.
	own := "259:4 enable=1 ctrl=user rpct=90.00 rlat=2000 wpct=90.00 wlat=4000 min=50.00 max=100.00\n"
	write("io.cost.qos", own)
	write("agentbox/io.max", "253:0 rbps=max wbps=max riops=max wiops=max\n")
	write("agentbox/io.weight", "default 100\n")
	write("agentbox/memory.high", "max\n")
	if reset, err := ResetLegacyBudget(); err != nil || len(reset) != 0 {
		t.Errorf("reset %q (%v) on a host with nothing of the budget's", reset, err)
	}
	if got := read("io.cost.qos"); got != own {
		t.Errorf("the host's own io.cost.qos became %q", got)
	}

	// The hard disk targets are the budget's too.
	if !legacyQoS(nestedKeyed("8:0 enable=1 ctrl=user rpct=95.00 rlat=75000 wpct=95.00 wlat=150000 min=5.00 max=150.00")["8:0"]) {
		t.Error("the budget's hard disk QoS isn't recognised")
	}
	if legacyQoS(nestedKeyed("8:0 enable=0 ctrl=user rpct=95.00 rlat=75000 wpct=95.00 wlat=150000 min=5.00 max=150.00")["8:0"]) {
		t.Error("io.cost already off is reset again")
	}
	if strings.Contains(read("agentbox/io.max"), "16777216") {
		t.Error("io.max still capped")
	}
}
